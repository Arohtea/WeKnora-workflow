package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	workflowruntime "github.com/Tencent/WeKnora/internal/workflow"
	"github.com/google/uuid"
)

const (
	workflowHTTPTimeout       = 30 * time.Second
	workflowHTTPResponseLimit = 1 << 20
	workflowLLMTimeout        = 120 * time.Second
)

type workflowAgentRuntime interface {
	ExecuteWorkflowBuiltinTool(
		ctx context.Context,
		config *types.AgentConfig,
		toolName string,
		args json.RawMessage,
		sessionID string,
		rerankModel rerank.Reranker,
	) (*types.ToolResult, error)
	ExecuteWorkflowMCPTool(
		ctx context.Context,
		config *types.AgentConfig,
		serviceID, toolName string,
		args json.RawMessage,
		sessionID, assistantMessageID, toolCallID string,
		eventBus *event.EventBus,
	) (*types.ToolResult, error)
	ExecuteWorkflowSkill(
		ctx context.Context,
		parentConfig *types.AgentConfig,
		chatModel chat.Chat,
		skillName, task, sessionID, assistantMessageID string,
		eventBus *event.EventBus,
	) (*types.AgentState, error)
}

// workflowPathResult 是一条执行路径的结果。
//
// failures 与 handledFailures 的区别是状态归并的关键：沿失败处理分支继续执行的
// 失败进入 handledFailures（用户显式写了错误分支，语义上已被消费），其余进入
// failures（未处理失败），只有后者会把运行状态拉低到 partial/failed。
type workflowPathResult struct {
	successEnd      bool
	answers         []string
	refs            []*types.SearchResult
	steps           []types.AgentStep
	failures        []types.WorkflowNodeFailure
	handledFailures []types.WorkflowNodeFailure
	usage           types.TokenUsage
	canceled        bool
}

type workflowNodeExecution struct {
	output *types.WorkflowNodeOutput
	result *types.ToolResult
	refs   []*types.SearchResult
	usage  types.TokenUsage
}

type workflowExecutor struct {
	ctx              context.Context
	runtime          workflowAgentRuntime
	config           *types.AgentConfig
	model            chat.Chat
	rerankModel      rerank.Reranker
	modelService     interfaces.ModelService
	definition       *types.WorkflowDefinition
	eventBus         *event.EventBus
	observer         *workflowRunObserver
	sessionID        string
	assistantMessage string
	requestID        string
	inputQuery       string
	semaphore        chan struct{}
	stepMu           sync.Mutex
	nextStepNumber   int
	nodes            map[string]types.WorkflowNode
	outgoing         map[string][]types.WorkflowEdge
	incoming         map[string][]types.WorkflowEdge
	// finalAnswerEventID 是唯一一条最终答案事件的 ID；聚合后的答案只发一次，
	// 旧的流式分片语义由这个 ID 承载 Done:true 收尾。
	finalAnswerEventID string
}

// runWorkflowQA 执行已保存的工作流，并通过现有 Agent 事件总线输出结果。
//
// @param ctx 当前问答上下文。
// @param req 当前问答请求。
// @param agentConfig 已解析的运行时配置。
// @param summaryModel 智能体顶层聊天模型。
// @param eventBus 当前请求的事件总线。
// @param publishedWorkflow 本次正式运行使用的不可变发布版本。
// @returns 初始化或执行器不可用时返回错误；节点级失败会记录到事件和最终摘要中。
func (s *sessionService) runWorkflowQA(
	ctx context.Context,
	req *types.QARequest,
	agentConfig *types.AgentConfig,
	summaryModel chat.Chat,
	eventBus *event.EventBus,
	publishedWorkflow *types.WorkflowVersionRecord,
) error {
	if req == nil || req.CustomAgent == nil || req.Session == nil {
		return fmt.Errorf("workflow request is incomplete")
	}
	runtime, ok := s.agentService.(workflowAgentRuntime)
	if !ok {
		return fmt.Errorf("workflow runtime is unavailable")
	}
	if summaryModel == nil {
		return fmt.Errorf("workflow chat model is unavailable")
	}
	if eventBus == nil {
		return fmt.Errorf("workflow event bus is unavailable")
	}
	if publishedWorkflow == nil || publishedWorkflow.Version <= 0 {
		return fmt.Errorf("workflow has not been published; publish it in the editor before running")
	}
	if err := workflowruntime.ValidatePublishedConfig(&req.CustomAgent.Config); err != nil {
		return err
	}
	if req.CustomAgent.Config.Workflow == nil {
		return fmt.Errorf("workflow definition is missing")
	}
	if provider, ok := s.agentService.(interface {
		ValidateWorkflowResources(context.Context, *types.CustomAgentConfig) error
	}); ok {
		if err := provider.ValidateWorkflowResources(ctx, &req.CustomAgent.Config); err != nil {
			return err
		}
	}

	// 工作流里的知识检索此前不走 rerank：内置工具注册时 rerank 模型传 nil，
	// knowledge_search 收到 nil 就静默降级，于是同一个知识库在工作流里的检索质量
	// 系统性低于普通智能体。这里按普通智能体同样的方式解析 rerank 模型。
	// 与普通路径的差别是"未配置就降级并告警"而非直接报错：工作流可能已经上线，
	// 不能因为缺少可选配置就让所有历史工作流停止工作。
	var rerankModel rerank.Reranker
	if agentRequiresRerankModel(req.CustomAgent) {
		rerankModelID := req.CustomAgent.Config.RerankModelID
		if rerankModelID == "" {
			logger.Warnf(ctx, "Workflow agent %s runs knowledge retrieval without a rerank model; retrieval quality will be lower than a normal agent", req.CustomAgent.ID)
		} else if resolved, err := s.modelService.GetRerankModel(ctx, rerankModelID); err != nil {
			logger.Warnf(ctx, "Failed to get rerank model %s for workflow agent: %v; continuing without rerank", rerankModelID, err)
		} else {
			rerankModel = resolved
		}
	}

	releaseTurn := s.holdSandboxTurn(ctx, req.Session.ID, agentConfig.SandboxConfigID)
	defer releaseTurn()

	stagedAttachments, err := s.stageWorkflowAttachments(ctx, req, agentConfig)
	if err != nil {
		return err
	}

	inputQuery := req.Query
	if req.QuotedContext != "" {
		inputQuery += "\n\n" + req.QuotedContext
	}
	attachmentsText := ""
	if req.ImageDescription != "" {
		attachmentsText += "\n\n[用户上传图片内容]\n" + req.ImageDescription
	}
	if len(req.Attachments) > 0 {
		attachmentsText += req.Attachments.BuildPrompt()
	}
	if manifest := buildSandboxAttachmentsPrompt(stagedAttachments); manifest != "" {
		attachmentsText += manifest
	}

	definition := req.CustomAgent.Config.Workflow
	nodes := make(map[string]types.WorkflowNode, len(definition.Nodes))
	outgoing := make(map[string][]types.WorkflowEdge, len(definition.Nodes))
	incoming := make(map[string][]types.WorkflowEdge, len(definition.Nodes))
	startID := ""
	for _, node := range definition.Nodes {
		nodes[node.ID] = node
		if node.Type == types.WorkflowNodeTypeStart {
			startID = node.ID
		}
	}
	for _, edge := range definition.Edges {
		outgoing[edge.Source] = append(outgoing[edge.Source], edge)
		incoming[edge.Target] = append(incoming[edge.Target], edge)
	}
	for nodeID := range outgoing {
		outgoing[nodeID] = workflowruntime.SortedOutgoingEdges(outgoing[nodeID])
	}
	if startID == "" {
		return fmt.Errorf("workflow start node is missing")
	}

	requestID, _ := types.RequestIDFromContext(ctx)
	observer := newWorkflowRunObserver(
		ctx,
		eventBus,
		workflowRunStoreFor(s.agentService),
		publishedWorkflow,
		definition,
		agentConfig,
		workflowRunTenantID(req),
		req.CustomAgent.ID,
		workflowRunTriggerSource(req),
		req.Session.ID,
		req.AssistantMessageID,
		inputQuery,
	)
	executor := &workflowExecutor{
		ctx:                ctx,
		runtime:            runtime,
		config:             agentConfig,
		model:              summaryModel,
		rerankModel:        rerankModel,
		modelService:       s.modelService,
		definition:         definition,
		eventBus:           eventBus,
		observer:           observer,
		sessionID:          req.Session.ID,
		assistantMessage:   req.AssistantMessageID,
		requestID:          requestID,
		inputQuery:         inputQuery,
		semaphore:          make(chan struct{}, workflowruntime.MaxParallelNodes),
		nodes:              nodes,
		outgoing:           outgoing,
		incoming:           incoming,
		finalAnswerEventID: generateEventID("workflow-answer"),
	}
	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"query":            inputQuery,
			"attachments_text": attachmentsText,
		},
		"nodes": map[string]interface{}{},
	}

	startedAt := time.Now()
	var result workflowPathResult
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorWithFields(ctx, fmt.Errorf("workflow execution panicked: %v", recovered),
					map[string]interface{}{
						"session_id": req.Session.ID,
						"agent_id":   req.CustomAgent.ID,
						"run_id":     observer.runID(),
						"stack":      string(debug.Stack()),
					})
				result = workflowPathResult{
					failures: []types.WorkflowNodeFailure{{
						NodeID: startID,
						Error:  "工作流执行时发生内部异常",
					}},
				}
			}
		}()
		result = executor.executePath(startID, variables, "")
	}()
	for i := range result.steps {
		result.steps[i].Iteration = i
	}
	result.refs = dedupeWorkflowReferences(result.refs)
	result.failures = dedupeWorkflowFailures(result.failures)
	result.handledFailures = dedupeWorkflowFailures(result.handledFailures)
	canceled := result.canceled || ctx.Err() != nil
	runStatus := types.ResolveWorkflowRunStatus(result.successEnd, result.failures, canceled)

	finalAnswer := composeWorkflowAnswer(result, runStatus)
	observer.finish(runStatus, finalAnswer, result.usage, result.failures)

	if len(result.refs) > 0 {
		if err := eventBus.Emit(context.WithoutCancel(ctx), event.Event{
			ID:        generateEventID("workflow-references"),
			Type:      event.EventAgentReferences,
			SessionID: req.Session.ID,
			RequestID: requestID,
			Data:      event.AgentReferencesData{References: result.refs},
		}); err != nil {
			logger.Warnf(ctx, "Failed to emit workflow references: %v", err)
		}
	}
	if runStatus == types.WorkflowRunStatusFailed {
		emitWorkflowFailureEvent(ctx, eventBus, req, requestID, finalAnswer)
	} else if runStatus != types.WorkflowRunStatusCanceled {
		// 取消后不发送缓冲答案：用户已经点了停止，再补一段答案会覆盖新的一轮。
		emitWorkflowFinalAnswer(ctx, eventBus, req, requestID, executor.finalAnswerEventID, finalAnswer)
	}

	refs := make([]interface{}, 0, len(result.refs))
	for _, ref := range result.refs {
		refs = append(refs, ref)
	}
	var usage interface{}
	if result.usage.TotalTokens > 0 {
		usageCopy := result.usage
		usage = &usageCopy
	}
	complete := event.Event{
		ID:        generateEventID("workflow-complete"),
		Type:      event.EventAgentComplete,
		SessionID: req.Session.ID,
		RequestID: requestID,
		Data: event.AgentCompleteData{
			SessionID:       req.Session.ID,
			TotalSteps:      len(result.steps),
			FinalAnswer:     finalAnswer,
			KnowledgeRefs:   refs,
			AgentSteps:      result.steps,
			Usage:           usage,
			TotalDurationMs: time.Since(startedAt).Milliseconds(),
			MessageID:       req.AssistantMessageID,
			RequestID:       requestID,
			Extra:           workflowCompleteExtra(observer.runID(), runStatus, result),
		},
	}
	if err := eventBus.Emit(context.WithoutCancel(ctx), complete); err != nil {
		logger.Warnf(ctx, "Failed to emit workflow completion event: %v", err)
	}
	return nil
}

// startPersistentWorkflowQA 为正式聊天入口创建绑定发布快照的 durable 运行。
//
// 请求线程只负责准备输入、写入根检查点并投递唤醒任务；节点执行、分支推进、
// 重试和最终答案都由 WorkflowNodeTaskService 完成，因此草稿修改不会影响已启动运行。
func (s *sessionService) startPersistentWorkflowQA(
	ctx context.Context,
	req *types.QARequest,
	agentConfig *types.AgentConfig,
	eventBus *event.EventBus,
	publishedWorkflow *types.WorkflowVersionRecord,
) error {
	if req == nil || req.CustomAgent == nil || req.Session == nil {
		return fmt.Errorf("workflow request is incomplete")
	}
	if eventBus == nil {
		return fmt.Errorf("workflow event bus is unavailable")
	}
	if publishedWorkflow == nil || publishedWorkflow.Version <= 0 {
		return fmt.Errorf("workflow has not been published; publish it in the editor before running")
	}
	if agentConfig == nil {
		return fmt.Errorf("workflow runtime config is unavailable")
	}
	definition, publishedConfig, err := DecodeWorkflowVersionDefinition(publishedWorkflow)
	if err != nil {
		return err
	}
	if err := workflowruntime.ValidatePublishedConfig(publishedConfig); err != nil {
		return err
	}
	if provider, ok := s.agentService.(interface {
		ValidateWorkflowResources(context.Context, *types.CustomAgentConfig) error
	}); ok {
		if err := provider.ValidateWorkflowResources(ctx, publishedConfig); err != nil {
			return err
		}
	}
	startID := ""
	for _, node := range definition.Nodes {
		if node.Type == types.WorkflowNodeTypeStart {
			startID = node.ID
			break
		}
	}
	if startID == "" {
		return fmt.Errorf("workflow start node is missing")
	}

	stagedAttachments, err := s.stageWorkflowAttachments(ctx, req, agentConfig)
	if err != nil {
		return err
	}
	inputQuery := req.Query
	if req.QuotedContext != "" {
		inputQuery += "\n\n" + req.QuotedContext
	}
	attachmentsText := ""
	if req.ImageDescription != "" {
		attachmentsText += "\n\n[用户上传图片内容]\n" + req.ImageDescription
	}
	if len(req.Attachments) > 0 {
		attachmentsText += req.Attachments.BuildPrompt()
	}
	if manifest := buildSandboxAttachmentsPrompt(stagedAttachments); manifest != "" {
		attachmentsText += manifest
	}

	input := map[string]interface{}{"query": inputQuery, "attachments_text": attachmentsText}
	inputPayload := marshalRedactedWorkflowPayload(input)
	now := time.Now()
	requestID, _ := types.RequestIDFromContext(ctx)
	idempotencyKey := "chat:" + req.AssistantMessageID
	if req.AssistantMessageID == "" {
		idempotencyKey = "request:" + requestID
	}
	if idempotencyKey == "chat:" || idempotencyKey == "request:" {
		idempotencyKey = "run:" + uuid.NewString()
	}
	requestedBy, _ := types.UserIDFromContext(ctx)
	run := &types.WorkflowRun{
		ID:                 generateEventID("workflow-run"),
		TenantID:           workflowRunTenantID(req),
		AgentID:            req.CustomAgent.ID,
		WorkflowVersion:    publishedWorkflow.Version,
		DraftRevision:      publishedWorkflow.DraftRevision,
		DefinitionSnapshot: append(types.JSON(nil), publishedWorkflow.Definition...),
		ConfigSnapshot:     append(types.JSON(nil), publishedWorkflow.ConfigSnapshot...),
		InputPayload:       inputPayload,
		RunMode:            types.WorkflowRunModeProduction,
		RequestedBy:        requestedBy,
		IdempotencyKey:     idempotencyKey,
		TriggerSource:      workflowRunTriggerSource(req),
		Status:             types.WorkflowRunStatusRunning,
		SessionID:          req.Session.ID,
		MessageID:          req.AssistantMessageID,
		RequestID:          requestID,
		StartedAt:          now,
		CreatedAt:          now,
	}
	run.InputSummary, run.InputTruncated = sanitizeWorkflowSummary(inputQuery)
	branch := &types.WorkflowRunBranch{
		ID:            uuid.NewString(),
		RunID:         run.ID,
		TenantID:      run.TenantID,
		AgentID:       run.AgentID,
		CurrentNodeID: startID,
		Variables:     marshalWorkflowVariables(map[string]interface{}{"input": input, "nodes": map[string]interface{}{}}),
		Status:        types.WorkflowBranchStatusPending,
		Version:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	initiator := types.TaskInitiatorFromContext(ctx)
	pending := newWorkflowPendingNode(run, branch.ID, startID, 1, 0, initiator, nil)
	started := &types.WorkflowRunEvent{EventType: string(event.EventWorkflowRunStarted), Status: types.WorkflowRunStatusRunning}
	starter, ok := s.agentService.(interface {
		StartWorkflowRun(context.Context, *types.WorkflowRun, *types.WorkflowRunBranch, *types.TaskPendingOp, *types.WorkflowRunEvent) (*types.WorkflowRun, bool, error)
	})
	if !ok {
		return fmt.Errorf("workflow durable runner is unavailable")
	}
	persisted, created, err := starter.StartWorkflowRun(ctx, run, branch, pending, started)
	if err != nil {
		return err
	}
	if created {
		payload := types.WorkflowLifecycleEventData{
			EventID: started.EventID, Sequence: started.Sequence, RunID: persisted.ID,
			WorkflowVersion: persisted.WorkflowVersion, Status: started.Status,
			OccurredAt: started.OccurredAt, Summary: persisted.InputSummary,
		}
		if err := eventBus.Emit(context.WithoutCancel(ctx), event.Event{
			ID: started.EventID, Type: event.EventWorkflowRunStarted,
			SessionID: persisted.SessionID, RequestID: persisted.RequestID, Data: payload,
		}); err != nil {
			logger.Warnf(ctx, "failed to emit workflow run started event: %v", err)
		}
	}
	return nil
}

// workflowRunStoreFor 在 agentService 具备运行记录能力时返回其存储实现。
//
// 用窄接口断言而不是把方法加进 interfaces.AgentService：不落库的替身实现
// 仍能完整跑通工作流，观测能力缺失不应成为执行的前置条件。
//
// @param service 当前会话服务持有的智能体服务。
// @returns 运行记录存储；不支持时返回 nil。
func workflowRunStoreFor(service interface{}) workflowRunStore {
	store, ok := service.(workflowRunStore)
	if !ok {
		return nil
	}
	return store
}

// workflowRunTenantID 决定运行记录归属哪个租户。
//
// 共享智能体场景下智能体属于出借方租户，运行记录必须落在出借方，否则出借方
// 在自己的运行列表里看不到被他人使用的轨迹。
//
// @param req 当前问答请求。
// @returns 运行记录租户 ID。
func workflowRunTenantID(req *types.QARequest) uint64 {
	if req.CustomAgent != nil && req.CustomAgent.TenantID != 0 {
		return req.CustomAgent.TenantID
	}
	if req.Session != nil {
		return req.Session.TenantID
	}
	return 0
}

// workflowRunTriggerSource 推断本次运行的触发来源。
//
// @param req 当前问答请求。
// @returns types.WorkflowTrigger* 之一。
func workflowRunTriggerSource(req *types.QARequest) string {
	if req.SharedAgentReadOnly {
		return types.WorkflowTriggerShare
	}
	return types.WorkflowTriggerChat
}

// composeWorkflowAnswer 按运行状态决定对外暴露的答案文本。
//
// partial 状态把成功分支的结果与结构化失败列表一起给出：只发答案用户会以为
// 工作流完整跑通，只发失败又丢掉了已经算出来的有效内容。
//
// @param result 一次运行的路径结果。
// @param status types.WorkflowRunStatus* 之一。
// @returns 最终答案文本；failed/canceled 时为空串。
func composeWorkflowAnswer(result workflowPathResult, status string) string {
	switch status {
	case types.WorkflowRunStatusCanceled:
		return ""
	case types.WorkflowRunStatusFailed:
		if message := workflowFailureMessage(result.failures); message != "" {
			return "工作流执行失败。\n" + message
		}
		return "工作流执行失败。"
	}
	answer := joinWorkflowAnswers(result.answers)
	if message := workflowFailureMessage(result.failures); message != "" {
		answer += "\n\n部分分支失败：\n" + message
	}
	if strings.TrimSpace(answer) == "" {
		answer = "工作流已完成。"
	}
	return answer
}

// workflowCompleteExtra 组装完成事件附带的工作流运行元数据。
//
// 失败列表放在 Extra 而不是新增 AgentCompleteData 字段：前端按需读取，
// 旧的完成事件消费方不受影响，事件结构也保持单一来源。
//
// @param runID 运行记录 ID。
// @param status types.WorkflowRunStatus* 之一。
// @param result 一次运行的路径结果。
// @returns 序列化进 AgentCompleteData.Extra 的元数据。
func workflowCompleteExtra(runID, status string, result workflowPathResult) map[string]interface{} {
	extra := map[string]interface{}{
		"workflow_run_id":     runID,
		"workflow_run_status": status,
	}
	if len(result.failures) > 0 {
		extra["workflow_failures"] = result.failures
	}
	if len(result.handledFailures) > 0 {
		extra["workflow_handled_failures"] = result.handledFailures
	}
	return extra
}

// emitWorkflowFailureEvent 在没有成功终点时发出可展示的错误事件。
//
// @param ctx 当前问答上下文。
// @param eventBus 当前请求的事件总线。
// @param req 当前问答请求。
// @param requestID 当前请求 ID。
// @param message 已聚合的失败描述。
func emitWorkflowFailureEvent(
	ctx context.Context,
	eventBus *event.EventBus,
	req *types.QARequest,
	requestID, message string,
) {
	errEvent := event.Event{
		ID:        generateEventID("workflow-error"),
		Type:      event.EventError,
		SessionID: req.Session.ID,
		RequestID: requestID,
		Data: event.ErrorData{
			Error:     message,
			Stage:     "workflow_execution",
			SessionID: req.Session.ID,
			Query:     req.Query,
		},
	}
	if err := eventBus.Emit(context.WithoutCancel(ctx), errEvent); err != nil {
		logger.Warnf(ctx, "Failed to emit workflow error event: %v", err)
	}
}

// emitWorkflowFinalAnswer 以平滑的打字机流式分片发送聚合后的最终答案。
//
// 节点在执行期间进行内容缓冲以防止多并行分支乱序，在此按分支聚合后以自然分片
// 快速流式推送给前端，兼顾并发确定的答案结构与自然的打字机流式体验。
//
// @param ctx 当前问答上下文。
// @param eventBus 当前请求的事件总线。
// @param req 当前问答请求。
// @param requestID 当前请求 ID。
// @param eventID 最终答案事件 ID。
// @param answer 聚合后的答案文本。
func emitWorkflowFinalAnswer(
	ctx context.Context,
	eventBus *event.EventBus,
	req *types.QARequest,
	requestID, eventID, answer string,
) {
	if strings.TrimSpace(answer) == "" {
		return
	}
	runes := []rune(answer)
	if len(runes) <= 40 {
		if err := eventBus.Emit(context.WithoutCancel(ctx), event.Event{
			ID:        eventID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: req.Session.ID,
			RequestID: requestID,
			Data:      event.AgentFinalAnswerData{Content: answer, Done: true},
		}); err != nil {
			logger.Warnf(ctx, "Failed to emit workflow final answer: %v", err)
		}
		return
	}

	chunkSize := 24
	for i := 0; i < len(runes); i += chunkSize {
		if ctx.Err() != nil {
			return
		}
		end := i + chunkSize
		done := false
		if end >= len(runes) {
			end = len(runes)
			done = true
		}
		chunk := string(runes[i:end])
		if err := eventBus.Emit(context.WithoutCancel(ctx), event.Event{
			ID:        eventID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: req.Session.ID,
			RequestID: requestID,
			Data:      event.AgentFinalAnswerData{Content: chunk, Done: done},
		}); err != nil {
			logger.Warnf(ctx, "Failed to emit workflow final answer chunk: %v", err)
			return
		}
		if !done {
			time.Sleep(15 * time.Millisecond)
		}
	}
}

func (s *sessionService) stageWorkflowAttachments(
	ctx context.Context, req *types.QARequest, agentConfig *types.AgentConfig,
) ([]stagedSessionAttachment, error) {
	stager, ok := s.agentService.(sessionAttachmentStager)
	if !ok {
		if len(req.Attachments) > 0 && strings.TrimSpace(agentConfig.SandboxConfigID) != "" {
			return nil, fmt.Errorf("agent service does not support session attachment staging")
		}
		return nil, nil
	}
	inputStore, err := stager.sessionSandboxInputStore(ctx, req.Session.ID, agentConfig.SandboxConfigID)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox file store for workflow: %w", err)
	}
	if inputStore == nil {
		return nil, nil
	}
	attachments, err := s.messageRepo.GetSessionAttachments(ctx, req.Session.ID)
	if err != nil {
		return nil, fmt.Errorf("load session attachments for workflow: %w", err)
	}
	return stager.stageSessionAttachments(
		ctx, req.Session.ID, agentConfig.SandboxConfigID, req.Session.TenantID, attachments,
	)
}

func truncateWorkflowRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func (e *workflowExecutor) buildNodeCallPreviewArgs(
	node types.WorkflowNode, variables map[string]interface{},
) map[string]interface{} {
	args := map[string]interface{}{
		"node_id":   node.ID,
		"node_type": node.Type,
		"node_name": node.Name,
	}
	switch node.Type {
	case types.WorkflowNodeTypeRetrieval:
		var cfg types.WorkflowRetrievalNodeConfig
		if err := json.Unmarshal(node.Config, &cfg); err == nil {
			query := cfg.QueryTemplate
			if strings.TrimSpace(query) == "" {
				query = "{{input.query}}"
			}
			if rendered, err := workflowruntime.RenderTemplate(query, variables); err == nil {
				args["query"] = rendered
			}
			args["knowledge_base_ids"] = cfg.KnowledgeBaseIDs
		}
	case types.WorkflowNodeTypeLLM:
		var cfg types.WorkflowLLMNodeConfig
		if err := json.Unmarshal(node.Config, &cfg); err == nil {
			if rendered, err := workflowruntime.RenderTemplate(cfg.Prompt, variables); err == nil {
				args["prompt"] = truncateWorkflowRunes(rendered, 200)
			}
		}
	case types.WorkflowNodeTypeLLMDecision:
		var cfg types.WorkflowLLMDecisionNodeConfig
		if err := json.Unmarshal(node.Config, &cfg); err == nil {
			if rendered, err := workflowruntime.RenderTemplate(cfg.Prompt, variables); err == nil {
				args["prompt"] = truncateWorkflowRunes(rendered, 200)
			}
			args["choices"] = cfg.Choices
		}
	case types.WorkflowNodeTypeTool:
		var cfg types.WorkflowToolNodeConfig
		if err := json.Unmarshal(node.Config, &cfg); err == nil {
			args["kind"] = cfg.Kind
			if cfg.ToolName != "" {
				args["tool_name"] = cfg.ToolName
			}
			if cfg.SkillName != "" {
				args["skill_name"] = cfg.SkillName
			}
			if cfg.TaskTemplate != "" {
				if rendered, err := workflowruntime.RenderTemplate(cfg.TaskTemplate, variables); err == nil {
					args["task"] = truncateWorkflowRunes(rendered, 200)
				}
			}
		}
	case types.WorkflowNodeTypeHTTP:
		var cfg types.WorkflowHTTPNodeConfig
		if err := json.Unmarshal(node.Config, &cfg); err == nil {
			if rendered, err := workflowruntime.RenderTemplate(cfg.URL, variables); err == nil {
				args["url"] = rendered
			}
			args["method"] = cfg.Method
		}
	}
	return args
}

// executePath 从指定节点开始同步执行一条分支。
//
// @param nodeID 本次执行的起始节点 ID。
// @param variables 该分支独立的变量表。
// @param branchPath 到达该节点前的执行路径（"/" 连接的节点 ID 序列），用于轨迹归因。
// @returns 该分支的执行结果。
func (e *workflowExecutor) executePath(
	nodeID string, variables map[string]interface{}, branchPath string,
) workflowPathResult {
	node, ok := e.nodes[nodeID]
	if !ok {
		return workflowPathResult{failures: []types.WorkflowNodeFailure{{
			NodeID: nodeID,
			Error:  fmt.Sprintf("节点 %s 不存在", nodeID),
		}}}
	}
	nodePath := joinWorkflowBranchPath(branchPath, node.ID)

	iteration := e.nextIteration()
	callID := "workflow-" + uuid.NewString()
	toolName := types.WorkflowToolCallPrefix + node.ID
	callArgs := e.buildNodeCallPreviewArgs(node, variables)
	e.emitNodeCall(node, toolName, callID, callArgs, iteration)

	// 运行已取消时尚未调度的节点直接标记 skipped，不再占用并行度；
	// 与"正在执行时被取消"区分开，前端才能看出哪些节点根本没跑。
	if e.ctx.Err() != nil {
		nodeExecution := e.observer.nodeStarted(node, nodePath, e.nodeInputSummary(node, variables))
		e.observer.nodeFinished(nodeExecution, types.WorkflowNodeStatusSkipped, "", types.TokenUsage{}, "", "")
		return workflowPathResult{canceled: true}
	}
	callCtx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	nodeExecution := e.observer.nodeStarted(node, nodePath, e.nodeInputSummary(node, variables))
	startedAt := time.Now()
	if !e.acquire(callCtx) {
		// 取消时不把节点算作失败：用户主动停止与节点自身错误是两件事，
		// 混在一起会让轨迹显示成"失败"并触发错误提示。
		e.observer.nodeFinished(nodeExecution, types.WorkflowNodeStatusCanceled, "", types.TokenUsage{}, "", "")
		return workflowPathResult{canceled: true}
	}
	execution, execErr := func() (workflowNodeExecution, error) {
		defer e.release()
		return e.executeNode(callCtx, node, variables, callID)
	}()
	if execution.result == nil {
		execution.result = &types.ToolResult{Success: execErr == nil}
	}
	if execErr != nil {
		execution.result.Success = false
		if execution.result.Error == "" {
			execution.result.Error = execErr.Error()
		}
	}
	if callCtx.Err() != nil || e.ctx.Err() != nil {
		// 上下文已取消（用户主动停止生成或超时），必须立即中止该分支，
		// 严禁作为普通失败走条件重试或下游边，避免停止后后台还在继续使用工具。
		e.observer.nodeFinished(nodeExecution, types.WorkflowNodeStatusCanceled, "", execution.usage, "", "")
		return workflowPathResult{canceled: true}
	}
	if !execution.result.Success {
		if execution.result.Error == "" {
			execution.result.Error = "节点执行失败"
		}
		// 失败节点同样要写入变量表，否则 nodes.<id>.status 只可能是 "success"：
		// 编辑器把 .status 作为可用变量提供给用户，若失败时提前返回，用户基于
		// status 构建的失败分支永远不会命中，工作流会"成功"地跳过错误处理。
		e.publishFailedNodeOutput(node, execution, variables)
		e.emitNodeResult(node, toolName, callID, execution.result, iteration, time.Since(startedAt))
		step := e.syntheticStep(node, callID, callArgs, execution.result, iteration)
		failure := types.WorkflowNodeFailure{
			NodeID:     node.ID,
			NodeName:   node.Name,
			NodeType:   node.Type,
			BranchPath: nodePath,
			Error:      execution.result.Error,
		}
		e.observer.nodeFinished(
			nodeExecution, types.WorkflowNodeStatusFailed,
			execution.result.Output, execution.usage, failure.ErrorCode, failure.Error,
		)
		current := workflowPathResult{
			steps: []types.AgentStep{step},
			usage: execution.usage,
		}
		// 失败后仍要沿出边继续：用户用 nodes.<id>.status == "failed" 建的失败分支
		// 必须有机会命中，否则错误处理形同虚设。只有真正命中失败处理分支时，
		// 这次失败才算"已处理"，否则进入未处理列表并拉低运行状态。
		if e.continueAfterFailure(node, variables, nodePath, failure, &current) {
			current.handledFailures = append(current.handledFailures, failure)
		} else {
			current.failures = append(current.failures, failure)
		}
		return current
	}

	if execution.output == nil {
		execution.output = &types.WorkflowNodeOutput{
			Text:   execution.result.Output,
			Data:   execution.result.Data,
			Status: "success",
		}
	}
	if execution.output.Data == nil {
		execution.output.Data = map[string]interface{}{}
	}
	if execution.output.Status == "" {
		execution.output.Status = "success"
	}
	if execution.result.Output == "" {
		execution.result.Output = execution.output.Text
	}
	if execution.result.Data == nil {
		execution.result.Data = execution.output.Data
	}
	varsNodes, _ := variables["nodes"].(map[string]interface{})
	if varsNodes == nil {
		varsNodes = make(map[string]interface{})
		variables["nodes"] = varsNodes
	}
	varsNodes[node.ID] = execution.output

	e.emitNodeResult(node, toolName, callID, execution.result, iteration, time.Since(startedAt))
	e.observer.nodeFinished(
		nodeExecution, types.WorkflowNodeStatusSucceeded,
		execution.output.Text, execution.usage, "", "",
	)
	step := e.syntheticStep(node, callID, callArgs, execution.result, iteration)
	current := workflowPathResult{
		steps: []types.AgentStep{step},
		refs:  execution.refs,
		usage: execution.usage,
	}
	if node.Type == types.WorkflowNodeTypeEnd {
		current.successEnd = true
		if strings.TrimSpace(execution.output.Text) != "" {
			current.answers = []string{execution.output.Text}
		}
		return current
	}

	matched, routeErr := e.matchOutgoingEdges(node, variables)
	if routeErr != nil {
		current.failures = append(current.failures, *routeErr)
		return current
	}
	if len(matched) == 0 {
		// 没有命中路由不是节点失败，而是路由配置覆盖不到当前运行时数据。
		// 用独立错误码上报，前端才能把它和"节点自己报错"区分开。
		current.failures = append(current.failures, types.WorkflowNodeFailure{
			NodeID:     node.ID,
			NodeName:   node.Name,
			NodeType:   node.Type,
			BranchPath: nodePath,
			ErrorCode:  types.WorkflowErrorCodeNoMatchingBranch,
			Error:      fmt.Sprintf("%s 没有命中的路由", node.Name),
		})
		return current
	}

	current.mergeChildren(e.runBranches(matched, variables, nodePath))
	return current
}

// matchOutgoingEdges 按节点的分支模式选出本次要执行的出边。
//
// first_match（默认）：按 order/ID 稳定顺序求值，取第一条命中条件的边，后续
// 边不再求值——因此一个条件里的变量缺失不会影响已经选中的分支。没有条件边命中
// 时退回默认边（edge.IsDefault，或单出边无条件的历史兼容场景）。
//
// all_match：保留原有并行语义，所有命中条件的边与默认边一起执行。
//
// @param node 当前节点。
// @param variables 当前分支变量表。
// @returns 待执行的出边；为空表示无路由可走。第二返回值为路由条件本身求值失败时的失败记录。
func (e *workflowExecutor) matchOutgoingEdges(
	node types.WorkflowNode, variables map[string]interface{},
) ([]types.WorkflowEdge, *types.WorkflowNodeFailure) {
	edges := e.outgoing[node.ID]
	if len(edges) == 0 {
		return nil, nil
	}
	var defaultEdge *types.WorkflowEdge
	conditional := make([]types.WorkflowEdge, 0, len(edges))
	for _, edge := range edges {
		if edge.IsDefault || (edge.Condition == nil && len(edges) == 1) {
			candidate := edge
			if defaultEdge == nil {
				defaultEdge = &candidate
			}
			continue
		}
		conditional = append(conditional, edge)
	}

	if node.BranchMode == types.WorkflowBranchModeAllMatch {
		matched := make([]types.WorkflowEdge, 0, len(conditional))
		for _, edge := range conditional {
			ok, err := workflowruntime.EvaluateCondition(edge.Condition, variables)
			if err != nil {
				return nil, &types.WorkflowNodeFailure{
					NodeID:    node.ID,
					NodeName:  node.Name,
					NodeType:  node.Type,
					ErrorCode: types.WorkflowErrorCodeNoMatchingBranch,
					Error:     fmt.Sprintf("%s 路由条件：%s", node.Name, err),
				}
			}
			if ok {
				matched = append(matched, edge)
			}
		}
		if defaultEdge != nil {
			matched = append(matched, *defaultEdge)
		}
		return matched, nil
	}

	for _, edge := range conditional {
		ok, err := workflowruntime.EvaluateCondition(edge.Condition, variables)
		if err != nil {
			return nil, &types.WorkflowNodeFailure{
				NodeID:    node.ID,
				NodeName:  node.Name,
				NodeType:  node.Type,
				ErrorCode: types.WorkflowErrorCodeNoMatchingBranch,
				Error:     fmt.Sprintf("%s 路由条件：%s", node.Name, err),
			}
		}
		if ok {
			return []types.WorkflowEdge{edge}, nil
		}
	}
	if defaultEdge != nil {
		return []types.WorkflowEdge{*defaultEdge}, nil
	}
	return nil, nil
}

// joinWorkflowBranchPath 把父路径与当前节点拼接成该节点所在的执行路径。
//
// 并行分支各自只有一条从 start 出发的链，因此路径必须由调用方沿调用栈传递，
// 不能从全局状态推断，否则并发下会互相串台。
//
// @param parent 父路径；根节点传空串。
// @param nodeID 当前节点 ID。
// @returns 以 "/" 连接的节点 ID 序列。
func joinWorkflowBranchPath(parent, nodeID string) string {
	if parent == "" {
		return nodeID
	}
	return parent + "/" + nodeID
}

// nodeInputSummary 生成节点级输入摘要，用于运行记录与生命周期事件。
//
// 只取节点名与渲染前的变量规模，不落任何提示词或上游节点原文：这些内容可能
// 含用户附件与凭据，落库后不可撤回。
//
// @param node 当前节点。
// @param variables 当前分支变量表。
// @returns 节点输入摘要。
func (e *workflowExecutor) nodeInputSummary(node types.WorkflowNode, variables map[string]interface{}) string {
	nodes, _ := variables["nodes"].(map[string]interface{})
	return fmt.Sprintf("节点 %s(%s)，上游节点数 %d", node.Name, node.Type, len(nodes))
}

// runBranches 并行执行命中的出边，返回各分支结果。
//
// 每个子分支使用独立的变量表副本，互不影响；节点执行会进入工具/MCP/技能等第三方
// 实现，其中任何未被捕获的 panic 都会终止整个进程——上层 QA goroutine 的 recover
// 只保护同步执行的 start 节点，因此这里必须自行兜底，把 panic 降级为一次分支失败。
//
// 返回切片与 edges 顺序一一对应，调用方按序合并即可得到与出边顺序一致的确定性结果。
//
// @param edges 待执行的出边集合，已是稳定排序。
// @param variables 父分支变量表。
// @param branchPath 父分支的执行路径。
// @returns 与 edges 同序的分支结果。
func (e *workflowExecutor) runBranches(
	edges []types.WorkflowEdge, variables map[string]interface{}, branchPath string,
) []workflowPathResult {
	children := make([]workflowPathResult, len(edges))
	var wait sync.WaitGroup
	for index, edge := range edges {
		index, edge := index, edge
		childVariables := workflowruntime.CloneVariables(variables)
		wait.Add(1)
		go func() {
			defer wait.Done()
			if e.ctx.Err() != nil {
				children[index] = workflowPathResult{canceled: true}
				return
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					target := e.nodes[edge.Target]
					logger.ErrorWithFields(e.ctx, fmt.Errorf("workflow node panicked: %v", recovered),
						map[string]interface{}{
							"node_id":   edge.Target,
							"node_name": target.Name,
							"stack":     string(debug.Stack()),
						})
					children[index] = workflowPathResult{
						failures: []types.WorkflowNodeFailure{{
							NodeID:     edge.Target,
							NodeName:   target.Name,
							NodeType:   target.Type,
							BranchPath: joinWorkflowBranchPath(branchPath, edge.Target),
							Error:      fmt.Sprintf("%s：执行时发生内部错误", target.Name),
						}},
					}
				}
			}()
			children[index] = e.executePath(edge.Target, childVariables, branchPath)
		}()
	}
	wait.Wait()
	return children
}

// publishFailedNodeOutput 把失败节点的输出写进变量表。
//
// 失败路径原先直接 return，变量表里没有这个节点，于是 nodes.<id>.status 只可能
// 是 "success"，用户按 status 搭的失败分支永远不会命中。这里复用执行器已构造的
// output（如 HTTP 节点会带上响应体与状态码），缺失时按结果兜底，并强制标记 failed。
func (e *workflowExecutor) publishFailedNodeOutput(
	node types.WorkflowNode, execution workflowNodeExecution, variables map[string]interface{},
) {
	output := execution.output
	if output == nil {
		output = &types.WorkflowNodeOutput{}
	}
	if output.Data == nil {
		output.Data = map[string]interface{}{}
	}
	if execution.result != nil {
		if output.Text == "" {
			output.Text = execution.result.Output
		}
		// 错误信息进 Data，用户可用 {{nodes.<id>.data.error}} 在失败分支里展示原因。
		if output.Data["error"] == nil && execution.result.Error != "" {
			output.Data["error"] = execution.result.Error
		}
	}
	output.Status = "failed"
	varsNodes, _ := variables["nodes"].(map[string]interface{})
	if varsNodes == nil {
		varsNodes = make(map[string]interface{})
		variables["nodes"] = varsNodes
	}
	varsNodes[node.ID] = output
}

// continueAfterFailure 在节点失败后沿"显式条件边"继续执行，并报告失败是否被处理。
//
// 只考虑带条件的出边：无条件的默认边代表正常路径，失败时沿它继续会把失败数据当
// 成功结果往下传。跳过默认边同时也保证了与旧行为兼容——此前失败即终止分支，没有
// 任何条件命中时依然终止，只有用户显式写出的错误分支（如 status == "failed"）才会
// 被执行。
//
// @param node 失败节点。
// @param variables 当前分支变量表。
// @param branchPath 失败节点的执行路径。
// @param failure 失败快照，用于状态归并。
// @param current 当前路径结果，命中分支的结果会合并进来。
// @returns 是否命中至少一条失败处理分支（即该失败是否已被处理）。
func (e *workflowExecutor) continueAfterFailure(
	node types.WorkflowNode,
	variables map[string]interface{},
	branchPath string,
	failure types.WorkflowNodeFailure,
	current *workflowPathResult,
) bool {
	matched := make([]types.WorkflowEdge, 0)
	for _, edge := range e.outgoing[node.ID] {
		if edge.Condition == nil || edge.IsDefault {
			continue
		}
		ok, err := workflowruntime.EvaluateCondition(edge.Condition, variables)
		if err != nil {
			// 条件求值失败意味着这次失败没有匹配到有效的处理分支，
			// 保留原因供最终状态判断，避免"处理分支写错了却显示成功"。
			unmatched := failure
			unmatched.ErrorCode = types.WorkflowErrorCodeNoMatchingBranch
			unmatched.Error = fmt.Sprintf("%s 路由条件：%s", node.Name, err)
			current.failures = append(current.failures, unmatched)
			continue
		}
		if ok {
			matched = append(matched, edge)
		}
	}
	if len(matched) == 0 {
		return false
	}
	current.mergeChildren(e.runBranches(matched, variables, branchPath))
	return true
}

// mergeChildren 把并行子分支的结果合并到当前路径。
//
// 子分支按出边顺序传入，因此 answers/steps/failures 的追加顺序与出边顺序一致，
// 最终答案在分支结构不变时是确定性的。
func (current *workflowPathResult) mergeChildren(children []workflowPathResult) {
	for _, child := range children {
		current.successEnd = current.successEnd || child.successEnd
		current.canceled = current.canceled || child.canceled
		current.answers = append(current.answers, child.answers...)
		current.refs = append(current.refs, child.refs...)
		current.steps = append(current.steps, child.steps...)
		current.failures = append(current.failures, child.failures...)
		current.handledFailures = append(current.handledFailures, child.handledFailures...)
		current.usage.Accumulate(child.usage)
	}
}

// executeNode 分发到具体节点类型的执行实现。
//
// ctx 由调用方按节点派生：取消信号必须能穿透到 HTTP / LLM / MCP / Skill 这些
// 真正的阻塞调用，否则用户点停止后节点会继续跑完并写入它的结果。
//
// @param ctx 节点执行上下文。
// @param node 节点定义。
// @param variables 当前分支变量表。
// @param toolCallID 该节点对应的合成工具调用 ID。
// @returns 节点执行结果以及底层错误。
func (e *workflowExecutor) executeNode(
	ctx context.Context,
	node types.WorkflowNode,
	variables map[string]interface{},
	toolCallID string,
) (workflowNodeExecution, error) {
	switch node.Type {
	case types.WorkflowNodeTypeStart:
		return workflowNodeExecution{
			output: &types.WorkflowNodeOutput{
				Text:   e.inputQuery,
				Data:   map[string]interface{}{},
				Status: "success",
			},
			result: &types.ToolResult{Success: true, Output: e.inputQuery, Data: map[string]interface{}{}},
		}, nil
	case types.WorkflowNodeTypeRetrieval:
		return e.executeRetrieval(ctx, node, variables)
	case types.WorkflowNodeTypeLLM:
		return e.executeLLM(ctx, node, variables, toolCallID)
	case types.WorkflowNodeTypeLLMDecision:
		return e.executeLLMDecision(ctx, node, variables)
	case types.WorkflowNodeTypeHTTP:
		return e.executeHTTP(ctx, node, variables)
	case types.WorkflowNodeTypeTool:
		return e.executeTool(ctx, node, variables, toolCallID)
	case types.WorkflowNodeTypeEnd:
		return e.executeEnd(node, variables)
	default:
		return workflowNodeExecution{}, fmt.Errorf("unsupported workflow node type %s", node.Type)
	}
}

// executeRetrieval 执行知识库检索节点。
//
// @param ctx 节点执行上下文。
// @param node 检索节点定义。
// @param variables 当前分支变量表。
// @returns 节点执行结果以及底层错误。
func (e *workflowExecutor) executeRetrieval(
	ctx context.Context, node types.WorkflowNode, variables map[string]interface{},
) (workflowNodeExecution, error) {
	var cfg types.WorkflowRetrievalNodeConfig
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		return workflowNodeExecution{}, err
	}
	query := cfg.QueryTemplate
	if strings.TrimSpace(query) == "" {
		query = "{{input.query}}"
	}
	query, err := workflowruntime.RenderTemplate(query, variables)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	args, _ := json.Marshal(map[string]interface{}{
		"queries":            []string{query},
		"knowledge_base_ids": cfg.KnowledgeBaseIDs,
	})
	result, err := e.runtime.ExecuteWorkflowBuiltinTool(
		ctx, e.config, agenttools.ToolKnowledgeSearch, args, e.sessionID, e.rerankModel,
	)
	if err != nil {
		return workflowNodeExecution{result: result}, err
	}
	if result == nil {
		return workflowNodeExecution{}, fmt.Errorf("knowledge search returned no result")
	}
	if !result.Success {
		return workflowNodeExecution{result: result}, nil
	}
	items := interface{}(nil)
	if result.Data != nil {
		items = result.Data["results"]
	}
	items = limitWorkflowItems(items, cfg.TopK)
	count := workflowItemCount(items)
	data := map[string]interface{}{"count": count, "items": items}
	return workflowNodeExecution{
		output: &types.WorkflowNodeOutput{Text: result.Output, Data: data, Status: "success"},
		result: &types.ToolResult{Success: true, Output: result.Output, Data: data},
		refs:   workflowSearchReferences(items),
		usage:  types.TokenUsage{},
	}, nil
}

// executeLLM 执行通用大模型处理节点。
//
// 生成内容在流式获取时实时推送增量分片（tool_chunk 与 thought），使用户在前端
// 能够实时看到打字机过程，不再等待所有节点跑完才显示。
//
// @param ctx 节点执行上下文。
// @param node 当前节点定义。
// @param variables 上游上下文变量表。
// @param toolCallID 当前节点的工具调用跟踪 ID。
// @returns 节点执行结果；生成文本封装在 Text 与 Data["text"] 中。
func (e *workflowExecutor) executeLLM(
	ctx context.Context, node types.WorkflowNode, variables map[string]interface{}, toolCallID string,
) (workflowNodeExecution, error) {
	var cfg types.WorkflowLLMNodeConfig
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		return workflowNodeExecution{}, err
	}
	prompt, err := workflowruntime.RenderTemplate(cfg.Prompt, variables)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	system := "你是一个严谨高效的文本处理助手。"
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		renderedSystem, renderErr := workflowruntime.RenderTemplate(cfg.SystemPrompt, variables)
		if renderErr != nil {
			return workflowNodeExecution{}, renderErr
		}
		system = renderedSystem
	}
	messages := make([]chat.Message, 0, 2)
	if strings.TrimSpace(system) != "" {
		messages = append(messages, chat.Message{Role: "system", Content: system})
	}
	messages = append(messages, chat.Message{Role: "user", Content: prompt})

	temp := 0.7
	if cfg.Temperature != nil {
		temp = *cfg.Temperature
	}
	options := &chat.ChatOptions{
		Temperature:    temp,
		PromptCacheKey: e.sessionID,
	}
	if cfg.MaxTokens != nil && *cfg.MaxTokens > 0 {
		options.MaxCompletionTokens = *cfg.MaxTokens
	}

	callCtx, cancel := context.WithTimeout(ctx, workflowLLMTimeout)
	defer cancel()

	stream, err := e.model.ChatStream(callCtx, messages, options)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	if stream == nil {
		return workflowNodeExecution{}, fmt.Errorf("LLM node returned nil stream")
	}

	var fullContent strings.Builder
	var fullReasoning strings.Builder
	var usage types.TokenUsage

	for chunk := range stream {
		if chunk.ResponseType == types.ResponseTypeThinking {
			fullReasoning.WriteString(chunk.Content)
			if chunk.Content != "" && e.eventBus != nil {
				_ = e.eventBus.Emit(callCtx, event.Event{
					ID:        uuid.NewString(),
					Type:      event.EventAgentThought,
					SessionID: e.sessionID,
					RequestID: e.requestID,
					Data: event.AgentThoughtData{
						Content: chunk.Content,
					},
				})
			}
			continue
		}
		if chunk.Content != "" {
			fullContent.WriteString(chunk.Content)
			if e.eventBus != nil && toolCallID != "" {
				_ = e.eventBus.Emit(callCtx, event.Event{
					ID:        uuid.NewString(),
					Type:      event.EventAgentToolChunk,
					SessionID: e.sessionID,
					RequestID: e.requestID,
					Data: map[string]interface{}{
						"tool_call_id": toolCallID,
						"chunk":        chunk.Content,
					},
				})
			}
		}
		if chunk.Usage != nil {
			usage.Accumulate(*chunk.Usage)
		}
	}

	contentStr := fullContent.String()
	reasoningStr := fullReasoning.String()

	data := map[string]interface{}{
		"text": contentStr,
	}
	if reasoningStr != "" {
		data["reasoning_content"] = reasoningStr
	}
	return workflowNodeExecution{
		output: &types.WorkflowNodeOutput{Text: contentStr, Data: data, Status: "success"},
		result: &types.ToolResult{Success: true, Output: contentStr, Data: data},
		usage:  usage,
	}, nil
}

// executeLLMDecision 执行模型判断节点，返回一个候选标签。
//
// @param ctx 节点执行上下文。
// @param node 判断节点定义。
// @param variables 当前分支变量表。
// @returns 节点执行结果以及底层错误。
func (e *workflowExecutor) executeLLMDecision(
	ctx context.Context, node types.WorkflowNode, variables map[string]interface{},
) (workflowNodeExecution, error) {
	var cfg types.WorkflowLLMDecisionNodeConfig
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		return workflowNodeExecution{}, err
	}
	prompt, err := workflowruntime.RenderTemplate(cfg.Prompt, variables)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	choices := make([]string, 0, len(cfg.Choices))
	for _, choice := range cfg.Choices {
		choices = append(choices, strings.TrimSpace(choice))
	}
	callCtx, cancel := context.WithTimeout(ctx, workflowLLMTimeout)
	defer cancel()

	// 1. 优先解析节点独立指定的决策模型（如专门配置的 Jev 决策模型）
	decisionModel := e.model
	if cfg.ModelID != "" && e.modelService != nil {
		if resolved, resolveErr := e.modelService.GetChatModel(ctx, cfg.ModelID); resolveErr == nil && resolved != nil {
			decisionModel = resolved
		} else {
			logger.Warnf(ctx, "Failed to resolve decision model %s for node %s: %v; falling back to agent model", cfg.ModelID, node.ID, resolveErr)
		}
	}

	// 2. 如果决策模型支持 Jev 原生概率决策，直接执行原生分类并返回结构化置信度与概率
	if jevCaller, ok := decisionModel.(chat.JevDecisionCaller); ok && len(choices) >= 2 {
		decisionResult, jevErr := jevCaller.Decision(callCtx, prompt, choices)
		if jevErr == nil && decisionResult != nil && decisionResult.Choice != "" {
			data := map[string]interface{}{
				"choice":        decisionResult.Choice,
				"confidence":    decisionResult.Confidence,
				"probabilities": decisionResult.Probabilities,
				"model":         decisionResult.Model,
				"reason":        "由 Jev System One 概率决策模型评估得出",
				"engine":        "jev",
			}
			return workflowNodeExecution{
				output: &types.WorkflowNodeOutput{Text: decisionResult.Choice, Data: data, Status: "success"},
				result: &types.ToolResult{Success: true, Output: decisionResult.Choice, Data: data},
				usage:  decisionResult.Usage,
			}, nil
		}
		if jevErr != nil {
			logger.Warnf(ctx, "Jev native decision failed for node %s: %v; falling back to chat", node.ID, jevErr)
		}
	}

	system := "你是工作流路由判断器。只能从候选标签中选择一个，并且必须只返回 JSON：{" +
		"\"choice\":\"候选标签\",\"reason\":\"简短理由\"}。候选标签：" + strings.Join(choices, ", ")
	messages := []chat.Message{{Role: "system", Content: system}, {Role: "user", Content: prompt}}
	thinkingOff := false
	options := &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 256,
		Thinking:            &thinkingOff,
		Format:              json.RawMessage(`{"type":"json_object"}`),
		PromptCacheKey:      e.sessionID,
	}
	response, err := decisionModel.Chat(callCtx, messages, options)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	if response == nil {
		return workflowNodeExecution{}, fmt.Errorf("LLM decision returned no response")
	}
	usage := response.Usage
	choice, reason, parseErr := parseWorkflowDecision(response.Content, choices)
	if parseErr != nil {
		correction := "上一次输出不符合要求。请严格从候选标签中选择一个，只返回 JSON，不要 Markdown 代码块。候选标签：" +
			strings.Join(choices, ", ") + "。上一次输出：" + response.Content
		retryMessages := []chat.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: prompt},
			{Role: "user", Content: correction},
		}
		retryResponse, retryErr := decisionModel.Chat(callCtx, retryMessages, options)
		if retryErr != nil {
			return workflowNodeExecution{usage: usage}, retryErr
		}
		if retryResponse == nil {
			return workflowNodeExecution{usage: usage}, fmt.Errorf("LLM decision retry returned no response")
		}
		usage.Accumulate(retryResponse.Usage)
		choice, reason, parseErr = parseWorkflowDecision(retryResponse.Content, choices)
	}
	if parseErr != nil {
		return workflowNodeExecution{usage: usage}, fmt.Errorf("invalid LLM decision: %w", parseErr)
	}
	data := map[string]interface{}{"choice": choice, "reason": reason}
	return workflowNodeExecution{
		output: &types.WorkflowNodeOutput{Text: choice, Data: data, Status: "success"},
		result: &types.ToolResult{Success: true, Output: choice, Data: data},
		usage:  usage,
	}, nil
}

func parseWorkflowDecision(content string, choices []string) (string, string, error) {
	var payload struct {
		Choice string `json:"choice"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(agenttools.RepairJSON(content)), &payload); err != nil {
		return "", "", fmt.Errorf("response is not valid JSON: %w", err)
	}
	choice := strings.TrimSpace(payload.Choice)
	for _, allowed := range choices {
		if choice == allowed {
			return choice, strings.TrimSpace(payload.Reason), nil
		}
	}
	return "", "", fmt.Errorf("choice %q is not one of the configured labels", choice)
}

// executeHTTP 执行受 SSRF 防护约束的 HTTP 请求节点。
//
// @param ctx 节点执行上下文。
// @param node HTTP 节点定义。
// @param variables 当前分支变量表。
// @returns 节点执行结果以及底层错误。
func (e *workflowExecutor) executeHTTP(
	ctx context.Context, node types.WorkflowNode, variables map[string]interface{},
) (workflowNodeExecution, error) {
	var cfg types.WorkflowHTTPNodeConfig
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		return workflowNodeExecution{}, err
	}
	urlText, err := workflowruntime.RenderTemplate(cfg.URL, variables)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	if err := secutils.ValidateURLForSSRF(urlText); err != nil {
		return workflowNodeExecution{}, err
	}
	bodyText := cfg.BodyTemplate
	if bodyText != "" {
		bodyText, err = workflowruntime.RenderTemplate(bodyText, variables)
		if err != nil {
			return workflowNodeExecution{}, err
		}
	}
	var requestBody io.Reader
	if bodyText != "" {
		requestBody = bytes.NewBufferString(bodyText)
	}
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(cfg.Method), urlText, requestBody)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	for name, value := range cfg.Headers {
		rendered, renderErr := workflowruntime.RenderTemplate(value, variables)
		if renderErr != nil {
			return workflowNodeExecution{}, renderErr
		}
		request.Header.Set(name, rendered)
	}
	if strings.TrimSpace(cfg.IdempotencyKeyTemplate) != "" {
		idempotencyKey, renderErr := workflowruntime.RenderTemplate(cfg.IdempotencyKeyTemplate, variables)
		if renderErr != nil {
			return workflowNodeExecution{}, renderErr
		}
		if strings.TrimSpace(idempotencyKey) == "" {
			return workflowNodeExecution{}, fmt.Errorf("HTTP idempotency key rendered empty")
		}
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	client := secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
		Timeout:      workflowHTTPTimeout,
		MaxRedirects: 3,
	})
	response, err := client.Do(request)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, workflowHTTPResponseLimit+1))
	if err != nil {
		return workflowNodeExecution{}, err
	}
	if len(responseBody) > workflowHTTPResponseLimit {
		return workflowNodeExecution{}, fmt.Errorf("HTTP response exceeds %d bytes", workflowHTTPResponseLimit)
	}
	responseText := string(responseBody)
	var parsed interface{}
	if strings.TrimSpace(responseText) != "" {
		_ = json.Unmarshal(responseBody, &parsed)
	}
	data := map[string]interface{}{
		"status_code": response.StatusCode,
		"body":        responseText,
		"json":        parsed,
	}
	result := &types.ToolResult{Success: response.StatusCode >= 200 && response.StatusCode < 300, Output: responseText, Data: data}
	if !result.Success {
		result.Error = fmt.Sprintf("HTTP request returned status %d", response.StatusCode)
	}
	output := &types.WorkflowNodeOutput{Text: responseText, Data: data, Status: "success"}
	if !result.Success {
		output.Status = "failed"
	}
	return workflowNodeExecution{output: output, result: result}, nil
}

// executeTool 执行内置工具、MCP 工具或受限 Skill 小智能体。
//
// @param ctx 节点执行上下文。
// @param node 工具节点定义。
// @param variables 当前分支变量表。
// @param toolCallID 该节点对应的合成工具调用 ID。
// @returns 节点执行结果以及底层错误。
func (e *workflowExecutor) executeTool(
	ctx context.Context, node types.WorkflowNode, variables map[string]interface{}, toolCallID string,
) (workflowNodeExecution, error) {
	var cfg types.WorkflowToolNodeConfig
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		return workflowNodeExecution{}, err
	}
	renderedArgs, err := workflowruntime.RenderValue(cfg.Arguments, variables)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	if renderedArgs == nil {
		renderedArgs = map[string]interface{}{}
	}
	args, err := json.Marshal(renderedArgs)
	if err != nil {
		return workflowNodeExecution{}, err
	}
	switch cfg.Kind {
	case types.WorkflowToolKindBuiltin:
		result, execErr := e.runtime.ExecuteWorkflowBuiltinTool(
			ctx, e.config, cfg.ToolName, args, e.sessionID, e.rerankModel,
		)
		return workflowExecutionFromToolResult(result), execErr
	case types.WorkflowToolKindMCP:
		result, execErr := e.runtime.ExecuteWorkflowMCPTool(
			ctx, e.config, cfg.ServiceID, cfg.ToolName, args,
			e.sessionID, e.assistantMessage, toolCallID, e.eventBus,
		)
		return workflowExecutionFromToolResult(result), execErr
	case types.WorkflowToolKindSkill:
		task, renderErr := workflowruntime.RenderTemplate(cfg.TaskTemplate, variables)
		if renderErr != nil {
			return workflowNodeExecution{}, renderErr
		}
		state, execErr := e.runtime.ExecuteWorkflowSkill(
			ctx, e.config, e.model, cfg.SkillName, task, e.sessionID, e.assistantMessage, e.eventBus,
		)
		if execErr != nil {
			return workflowNodeExecution{}, execErr
		}
		if state == nil {
			return workflowNodeExecution{}, fmt.Errorf("Skill returned no state")
		}
		answer := state.FinalAnswer
		// 把产物文件以 sandbox:<文件名> 格式追加到答案末尾。
		// handler 层的 rewriteArtifactReferences 在写入 assistantMessage 时会把
		// sandbox: 格式替换为持久化的 resource:// 链接，前端凭此渲染下载卡片。
		if len(state.Artifacts) > 0 {
			answer = appendWorkflowArtifactLinks(answer, state.Artifacts)
		}
		data := map[string]interface{}{"final_answer": answer}
		return workflowNodeExecution{
			output: &types.WorkflowNodeOutput{Text: answer, Data: data, Status: "success"},
			result: &types.ToolResult{Success: true, Output: answer, Data: data},
			refs:   state.KnowledgeRefs,
			usage:  state.TurnUsage,
		}, nil
	default:
		return workflowNodeExecution{}, fmt.Errorf("unsupported workflow tool kind %s", cfg.Kind)
	}
}

func workflowExecutionFromToolResult(result *types.ToolResult) workflowNodeExecution {
	if result == nil {
		return workflowNodeExecution{}
	}
	status := "success"
	if !result.Success {
		status = "failed"
	}
	return workflowNodeExecution{
		output: &types.WorkflowNodeOutput{Text: result.Output, Data: result.Data, Status: status},
		result: result,
	}
}

func (e *workflowExecutor) executeEnd(
	node types.WorkflowNode, variables map[string]interface{},
) (workflowNodeExecution, error) {
	var cfg types.WorkflowEndNodeConfig
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		return workflowNodeExecution{}, err
	}
	text := strings.TrimSpace(cfg.TextTemplate)
	if text != "" {
		// 仅在显式配置了模板时渲染；若留空继承上游输出，上游输出可能包含 {{...}} 代码片段，不能二次求值
		var err error
		text, err = workflowruntime.RenderTemplate(text, variables)
		if err != nil {
			return workflowNodeExecution{}, err
		}
	} else {
		// 未配置模板时直接沿用上游节点的输出内容
		for _, edge := range e.incoming[node.ID] {
			if value, ok := workflowruntime.ResolveVariable(variables, "nodes."+edge.Source+".text"); ok {
				text = fmt.Sprint(value)
				break
			}
		}
	}
	data := map[string]interface{}{"text": text}
	return workflowNodeExecution{
		output: &types.WorkflowNodeOutput{Text: text, Data: data, Status: "success"},
		result: &types.ToolResult{Success: true, Output: text, Data: data},
	}, nil
}

// acquire 以节点执行上下文抢占并行度信号量。
//
// 用节点自己的 ctx（派生自运行上下文）而非运行上下文本身，是为了让取消能立刻
// 释放还在排队等待的节点，而不是等信号量空出来。
//
// @param ctx 节点执行上下文。
// @returns 是否成功获得执行许可。
func (e *workflowExecutor) acquire(ctx context.Context) bool {
	select {
	case e.semaphore <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (e *workflowExecutor) release() {
	select {
	case <-e.semaphore:
	default:
	}
}

func (e *workflowExecutor) nextIteration() int {
	e.stepMu.Lock()
	defer e.stepMu.Unlock()
	iteration := e.nextStepNumber
	e.nextStepNumber++
	return iteration
}

func (e *workflowExecutor) syntheticStep(
	node types.WorkflowNode,
	callID string,
	args map[string]interface{},
	result *types.ToolResult,
	iteration int,
) types.AgentStep {
	return types.AgentStep{
		Iteration: iteration,
		Thought:   "工作流节点：" + node.Name,
		ToolCalls: []types.ToolCall{{
			ID:       callID,
			Name:     types.WorkflowToolCallPrefix + node.ID,
			Args:     args,
			Result:   result,
			Duration: 0,
		}},
		Timestamp: time.Now(),
	}
}

func (e *workflowExecutor) emitNodeCall(
	node types.WorkflowNode, toolName, callID string, args map[string]interface{}, iteration int,
) {
	if args == nil {
		args = make(map[string]interface{})
	}
	args["node_name"] = node.Name
	args["node_type"] = node.Type
	args["is_workflow"] = true

	if err := e.eventBus.Emit(e.ctx, event.Event{
		ID:        callID,
		Type:      event.EventAgentToolCall,
		SessionID: e.sessionID,
		RequestID: e.requestID,
		Data: event.AgentToolCallData{
			ToolCallID: callID,
			ToolName:   toolName,
			Arguments:  args,
			Iteration:  iteration,
			Hint:       node.Name,
		},
	}); err != nil {
		logger.Warnf(e.ctx, "Failed to emit workflow node call: %v", err)
	}
}

func (e *workflowExecutor) emitNodeResult(
	node types.WorkflowNode,
	toolName, callID string,
	result *types.ToolResult,
	iteration int,
	duration time.Duration,
) {
	if result == nil {
		return
	}
	if result.Data == nil {
		result.Data = make(map[string]interface{})
	}
	result.Data["node_name"] = node.Name
	result.Data["node_type"] = node.Type
	result.Data["is_workflow"] = true

	if err := e.eventBus.Emit(e.ctx, event.Event{
		ID:        callID + "-result",
		Type:      event.EventAgentToolResult,
		SessionID: e.sessionID,
		RequestID: e.requestID,
		Data: event.AgentToolResultData{
			ToolCallID: callID,
			ToolName:   toolName,
			Output:     result.Output,
			Error:      result.Error,
			Success:    result.Success,
			Duration:   duration.Milliseconds(),
			Iteration:  iteration,
			Data:       result.Data,
		},
	}); err != nil {
		logger.Warnf(e.ctx, "Failed to emit workflow node result: %v", err)
	}
	if err := e.eventBus.Emit(e.ctx, event.Event{
		ID:        callID + "-step",
		Type:      event.EventAgentStep,
		SessionID: e.sessionID,
		RequestID: e.requestID,
		Data: event.AgentStepData{
			Iteration: iteration,
			Thought:   "工作流节点：" + node.Name,
			ToolCalls: []types.ToolCall{{ID: callID, Name: toolName, Result: result}},
			Duration:  duration.Milliseconds(),
		},
	}); err != nil {
		logger.Warnf(e.ctx, "Failed to emit workflow node step: %v", err)
	}
}

func limitWorkflowItems(value interface{}, limit int) interface{} {
	if limit <= 0 || value == nil {
		return value
	}
	switch items := value.(type) {
	case []interface{}:
		if len(items) > limit {
			return items[:limit]
		}
	case []map[string]interface{}:
		if len(items) > limit {
			return items[:limit]
		}
	}
	return value
}

func workflowItemCount(value interface{}) int {
	switch items := value.(type) {
	case []interface{}:
		return len(items)
	case []map[string]interface{}:
		return len(items)
	default:
		return 0
	}
}

func workflowSearchReferences(value interface{}) []*types.SearchResult {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var rawRows []map[string]interface{}
	if err := json.Unmarshal(data, &rawRows); err != nil {
		return nil
	}
	refs := make([]*types.SearchResult, 0, len(rawRows))
	for _, raw := range rawRows {
		id, _ := raw["id"].(string)
		if id == "" {
			id, _ = raw["chunk_id"].(string)
		}
		if id == "" {
			continue
		}
		rowData, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var row types.SearchResult
		if err := json.Unmarshal(rowData, &row); err != nil {
			continue
		}
		row.ID = id
		refs = append(refs, &row)
	}
	return refs
}

func dedupeWorkflowReferences(refs []*types.SearchResult) []*types.SearchResult {
	seen := make(map[string]struct{}, len(refs))
	out := make([]*types.SearchResult, 0, len(refs))
	for _, ref := range refs {
		if ref == nil || ref.ID == "" {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func joinWorkflowAnswers(answers []string) string {
	parts := make([]string, 0, len(answers))
	for _, answer := range answers {
		if text := strings.TrimSpace(answer); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// appendWorkflowArtifactLinks 把产物文件以 sandbox:<文件名> 格式追加到答案末尾。
//
// handler 层的 rewriteArtifactReferences 在把答案写入 assistantMessage 前，会将
// sandbox: 格式替换为 resource:// 永久链接，前端凭此渲染可下载文件卡片。
// 这里只负责追加原始引用，不做 Markdown 重写，避免 service 层依赖 handler 层代码。
//
// @param answer 当前 Skill 子 Agent 的 FinalAnswer 文本。
// @param artifacts 已由 ArtifactCollector 持久化的产物文件列表。
// @returns 追加了文件引用的答案文本。
func appendWorkflowArtifactLinks(answer string, artifacts types.MessageArtifacts) string {
	if len(artifacts) == 0 {
		return answer
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(answer, "\n"))
	b.WriteString("\n\n**生成文件：**\n")
	for _, artifact := range artifacts {
		name := strings.TrimSpace(artifact.FileName)
		if name == "" {
			continue
		}
		b.WriteString("- ![")
		b.WriteString(name)
		b.WriteString("](sandbox:")
		b.WriteString(name)
		b.WriteString(")\n")
	}
	return b.String()
}
