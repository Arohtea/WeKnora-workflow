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
	) (*types.AgentState, error)
}

type workflowPathResult struct {
	successEnd bool
	answers    []string
	refs       []*types.SearchResult
	steps      []types.AgentStep
	failures   []string
	usage      types.TokenUsage
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
	definition       *types.WorkflowDefinition
	eventBus         *event.EventBus
	sessionID        string
	assistantMessage string
	requestID        string
	inputQuery       string
	semaphore        chan struct{}
	stepMu           sync.Mutex
	nextStepNumber   int
	nodes               map[string]types.WorkflowNode
	outgoing            map[string][]types.WorkflowEdge
	incoming            map[string][]types.WorkflowEdge
	finalAnswerStreamed bool
	finalAnswerMu       sync.Mutex
	finalAnswerEventID  string
}

// runWorkflowQA 执行已保存的工作流，并通过现有 Agent 事件总线输出结果。
//
// @param ctx 当前问答上下文。
// @param req 当前问答请求。
// @param agentConfig 已解析的运行时配置。
// @param summaryModel 智能体顶层聊天模型。
// @param eventBus 当前请求的事件总线。
// @returns 初始化或执行器不可用时返回错误；节点级失败会记录到事件和最终摘要中。
func (s *sessionService) runWorkflowQA(
	ctx context.Context,
	req *types.QARequest,
	agentConfig *types.AgentConfig,
	summaryModel chat.Chat,
	eventBus *event.EventBus,
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
	if err := workflowruntime.NormalizeConfig(&req.CustomAgent.Config); err != nil {
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
	executor := &workflowExecutor{
		ctx:              ctx,
		runtime:          runtime,
		config:           agentConfig,
		model:            summaryModel,
		rerankModel:      rerankModel,
		definition:       definition,
		eventBus:         eventBus,
		sessionID:        req.Session.ID,
		assistantMessage: req.AssistantMessageID,
		requestID:        requestID,
		inputQuery:       inputQuery,
		semaphore:        make(chan struct{}, workflowruntime.MaxParallelNodes),
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
						"stack":      string(debug.Stack()),
					})
				result = workflowPathResult{
					failures: []string{"工作流执行时发生内部异常"},
				}
			}
		}()
		result = executor.executePath(startID, variables)
	}()
	for i := range result.steps {
		result.steps[i].Iteration = i
	}
	result.refs = dedupeWorkflowReferences(result.refs)
	result.failures = uniqueWorkflowFailures(result.failures)

	finalAnswer := ""
	if result.successEnd {
		finalAnswer = joinWorkflowAnswers(result.answers)
		if len(result.failures) > 0 {
			finalAnswer += "\n\n部分分支失败：\n- " + strings.Join(result.failures, "\n- ")
		}
		if strings.TrimSpace(finalAnswer) == "" {
			finalAnswer = "工作流已完成。"
		}
	} else {
		finalAnswer = "工作流执行失败。"
		if len(result.failures) > 0 {
			finalAnswer += "\n" + strings.Join(result.failures, "\n")
		}
		errEvent := event.Event{
			ID:        generateEventID("workflow-error"),
			Type:      event.EventError,
			SessionID: req.Session.ID,
			RequestID: requestID,
			Data: event.ErrorData{
				Error:     finalAnswer,
				Stage:     "workflow_execution",
				SessionID: req.Session.ID,
				Query:     req.Query,
			},
		}
		if err := eventBus.Emit(context.WithoutCancel(ctx), errEvent); err != nil {
			logger.Warnf(ctx, "Failed to emit workflow error event: %v", err)
		}
	}

	if len(result.refs) > 0 {
		if err := eventBus.Emit(ctx, event.Event{
			ID:        generateEventID("workflow-references"),
			Type:      event.EventAgentReferences,
			SessionID: req.Session.ID,
			RequestID: requestID,
			Data:      event.AgentReferencesData{References: result.refs},
		}); err != nil {
			logger.Warnf(ctx, "Failed to emit workflow references: %v", err)
		}
	}
	answerID := executor.finalAnswerEventID
	if answerID == "" {
		answerID = generateEventID("workflow-answer")
	}

	executor.finalAnswerMu.Lock()
	streamed := executor.finalAnswerStreamed
	executor.finalAnswerMu.Unlock()

	if streamed {
		if err := eventBus.Emit(ctx, event.Event{
			ID:        answerID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: req.Session.ID,
			RequestID: requestID,
			Data:      event.AgentFinalAnswerData{Done: true},
		}); err != nil {
			logger.Warnf(ctx, "Failed to emit workflow answer done event: %v", err)
		}
	} else {
		chunks := splitAnswerIntoStreamChunks(finalAnswer, 24)
		for _, chunk := range chunks {
			if err := eventBus.Emit(ctx, event.Event{
				ID:        answerID,
				Type:      event.EventAgentFinalAnswer,
				SessionID: req.Session.ID,
				RequestID: requestID,
				Data:      event.AgentFinalAnswerData{Content: chunk},
			}); err != nil {
				logger.Warnf(ctx, "Failed to emit workflow answer chunk: %v", err)
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err := eventBus.Emit(ctx, event.Event{
			ID:        answerID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: req.Session.ID,
			RequestID: requestID,
			Data:      event.AgentFinalAnswerData{Done: true},
		}); err != nil {
			logger.Warnf(ctx, "Failed to emit workflow answer done: %v", err)
		}
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
		},
	}
	if err := eventBus.Emit(context.WithoutCancel(ctx), complete); err != nil {
		logger.Warnf(ctx, "Failed to emit workflow completion event: %v", err)
	}
	return nil
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

func (e *workflowExecutor) executePath(
	nodeID string, variables map[string]interface{},
) workflowPathResult {
	node, ok := e.nodes[nodeID]
	if !ok {
		return workflowPathResult{failures: []string{fmt.Sprintf("节点 %s 不存在", nodeID)}}
	}

	iteration := e.nextIteration()
	callID := "workflow-" + uuid.NewString()
	toolName := types.WorkflowToolCallPrefix + node.ID
	callArgs := map[string]interface{}{"node_id": node.ID, "node_type": node.Type}
	e.emitNodeCall(node, toolName, callID, callArgs, iteration)

	startedAt := time.Now()
	if !e.acquire() {
		return workflowPathResult{failures: []string{fmt.Sprintf("%s：工作流已取消", node.Name)}}
	}
	execution, execErr := func() (workflowNodeExecution, error) {
		defer e.release()
		return e.executeNode(node, variables, callID)
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
		current := workflowPathResult{
			steps:    []types.AgentStep{step},
			usage:    execution.usage,
			failures: []string{fmt.Sprintf("%s：%s", node.Name, execution.result.Error)},
		}
		// 失败后仍要沿出边继续：用户用 nodes.<id>.status == "failed" 建的失败分支
		// 必须有机会命中，否则错误处理形同虚设。条件边照常求值，命中的分支继续跑；
		// 若没有任何条件边命中，则该分支到此为止（失败已记录）。
		e.continueAfterFailure(node, variables, &current)
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

	matched := make([]types.WorkflowEdge, 0)
	var defaultEdge *types.WorkflowEdge
	for _, edge := range e.outgoing[node.ID] {
		if edge.IsDefault || (edge.Condition == nil && len(e.outgoing[node.ID]) == 1) {
			candidate := edge
			defaultEdge = &candidate
			continue
		}
		ok, err := workflowruntime.EvaluateCondition(edge.Condition, variables)
		if err != nil {
			current.failures = append(current.failures, fmt.Sprintf("%s 路由条件：%s", node.Name, err))
			continue
		}
		if ok {
			matched = append(matched, edge)
		}
	}
	if len(matched) == 0 && defaultEdge != nil {
		matched = append(matched, *defaultEdge)
	}
	if len(matched) == 0 {
		current.failures = append(current.failures, fmt.Sprintf("%s 没有命中的路由", node.Name))
		return current
	}

	current.mergeChildren(e.runBranches(matched, variables))
	return current
}

// runBranches 并行执行命中的出边，返回各分支结果。
//
// 每个子分支使用独立的变量表副本，互不影响；节点执行会进入工具/MCP/技能等第三方
// 实现，其中任何未被捕获的 panic 都会终止整个进程——上层 QA goroutine 的 recover
// 只保护同步执行的 start 节点，因此这里必须自行兜底，把 panic 降级为一次分支失败。
func (e *workflowExecutor) runBranches(
	edges []types.WorkflowEdge, variables map[string]interface{},
) []workflowPathResult {
	children := make([]workflowPathResult, len(edges))
	var wait sync.WaitGroup
	for index, edge := range edges {
		index, edge := index, edge
		childVariables := workflowruntime.CloneVariables(variables)
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorWithFields(e.ctx, fmt.Errorf("workflow node panicked: %v", recovered),
						map[string]interface{}{
							"node_id":   edge.Target,
							"node_name": e.nodes[edge.Target].Name,
							"stack":     string(debug.Stack()),
						})
					children[index] = workflowPathResult{
						failures: []string{fmt.Sprintf("%s：执行时发生内部错误", e.nodes[edge.Target].Name)},
					}
				}
			}()
			children[index] = e.executePath(edge.Target, childVariables)
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

// continueAfterFailure 在节点失败后沿"显式条件边"继续执行。
//
// 只考虑带条件的出边：无条件的默认边代表正常路径，失败时沿它继续会把失败数据当
// 成功结果往下传。跳过默认边同时也保证了与旧行为兼容——此前失败即终止分支，没有
// 任何条件命中时依然终止，只有用户显式写出的错误分支（如 status == "failed"）才会
// 被执行。
func (e *workflowExecutor) continueAfterFailure(
	node types.WorkflowNode, variables map[string]interface{}, current *workflowPathResult,
) {
	matched := make([]types.WorkflowEdge, 0)
	for _, edge := range e.outgoing[node.ID] {
		if edge.Condition == nil || edge.IsDefault {
			continue
		}
		ok, err := workflowruntime.EvaluateCondition(edge.Condition, variables)
		if err != nil {
			current.failures = append(current.failures, fmt.Sprintf("%s 路由条件：%s", node.Name, err))
			continue
		}
		if ok {
			matched = append(matched, edge)
		}
	}
	if len(matched) == 0 {
		return
	}
	current.mergeChildren(e.runBranches(matched, variables))
}

// mergeChildren 把并行子分支的结果合并到当前路径。
func (current *workflowPathResult) mergeChildren(children []workflowPathResult) {
	for _, child := range children {
		current.successEnd = current.successEnd || child.successEnd
		current.answers = append(current.answers, child.answers...)
		current.refs = append(current.refs, child.refs...)
		current.steps = append(current.steps, child.steps...)
		current.failures = append(current.failures, child.failures...)
		current.usage.Accumulate(child.usage)
	}
}

func (e *workflowExecutor) executeNode(
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
		return e.executeRetrieval(node, variables)
	case types.WorkflowNodeTypeLLM:
		return e.executeLLM(node, variables)
	case types.WorkflowNodeTypeLLMDecision:
		return e.executeLLMDecision(node, variables)
	case types.WorkflowNodeTypeHTTP:
		return e.executeHTTP(node, variables)
	case types.WorkflowNodeTypeTool:
		return e.executeTool(node, variables, toolCallID)
	case types.WorkflowNodeTypeEnd:
		return e.executeEnd(node, variables)
	default:
		return workflowNodeExecution{}, fmt.Errorf("unsupported workflow node type %s", node.Type)
	}
}

func (e *workflowExecutor) executeRetrieval(
	node types.WorkflowNode, variables map[string]interface{},
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
		e.ctx, e.config, agenttools.ToolKnowledgeSearch, args, e.sessionID, e.rerankModel,
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

// isTerminalAnswerNode 探测指定节点是否作为生成最终回答的直连终态节点。
func (e *workflowExecutor) isTerminalAnswerNode(nodeID string) bool {
	edges := e.outgoing[nodeID]
	for _, edge := range edges {
		targetNode, exists := e.nodes[edge.Target]
		if exists && targetNode.Type == types.WorkflowNodeTypeEnd {
			var endCfg types.WorkflowEndNodeConfig
			if len(targetNode.Config) > 0 {
				_ = json.Unmarshal(targetNode.Config, &endCfg)
			}
			tmpl := strings.TrimSpace(endCfg.TextTemplate)
			if tmpl == "" || tmpl == fmt.Sprintf("{{nodes.%s.text}}", nodeID) {
				return true
			}
		}
	}
	return false
}

// executeLLM 执行通用大模型处理节点，支持自定义 Prompt 模板与流式文本生成。
//
// @param node 当前节点定义。
// @param variables 上游上下文变量表。
// @returns 节点执行结果；生成文本封装在 Text 与 Data["text"] 中。
func (e *workflowExecutor) executeLLM(
	node types.WorkflowNode, variables map[string]interface{},
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

	callCtx, cancel := context.WithTimeout(e.ctx, workflowLLMTimeout)
	defer cancel()

	isTerminal := e.isTerminalAnswerNode(node.ID)

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
			continue
		}
		if chunk.Content != "" {
			fullContent.WriteString(chunk.Content)
			if isTerminal {
				e.finalAnswerMu.Lock()
				e.finalAnswerStreamed = true
				e.finalAnswerMu.Unlock()

				_ = e.eventBus.Emit(e.ctx, event.Event{
					ID:        e.finalAnswerEventID,
					Type:      event.EventAgentFinalAnswer,
					SessionID: e.sessionID,
					RequestID: e.requestID,
					Data: event.AgentFinalAnswerData{
						Content: chunk.Content,
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

func (e *workflowExecutor) executeLLMDecision(
	node types.WorkflowNode, variables map[string]interface{},
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
	callCtx, cancel := context.WithTimeout(e.ctx, workflowLLMTimeout)
	defer cancel()
	response, err := e.model.Chat(callCtx, messages, options)
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
		retryResponse, retryErr := e.model.Chat(callCtx, retryMessages, options)
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

func (e *workflowExecutor) executeHTTP(
	node types.WorkflowNode, variables map[string]interface{},
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
	request, err := http.NewRequestWithContext(e.ctx, strings.ToUpper(cfg.Method), urlText, requestBody)
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

func (e *workflowExecutor) executeTool(
	node types.WorkflowNode, variables map[string]interface{}, toolCallID string,
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
			e.ctx, e.config, cfg.ToolName, args, e.sessionID, e.rerankModel,
		)
		return workflowExecutionFromToolResult(result), execErr
	case types.WorkflowToolKindMCP:
		result, execErr := e.runtime.ExecuteWorkflowMCPTool(
			e.ctx, e.config, cfg.ServiceID, cfg.ToolName, args,
			e.sessionID, e.assistantMessage, toolCallID, e.eventBus,
		)
		return workflowExecutionFromToolResult(result), execErr
	case types.WorkflowToolKindSkill:
		task, renderErr := workflowruntime.RenderTemplate(cfg.TaskTemplate, variables)
		if renderErr != nil {
			return workflowNodeExecution{}, renderErr
		}
		state, execErr := e.runtime.ExecuteWorkflowSkill(
			e.ctx, e.config, e.model, cfg.SkillName, task, e.sessionID, e.assistantMessage,
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

func (e *workflowExecutor) acquire() bool {
	select {
	case e.semaphore <- struct{}{}:
		return true
	case <-e.ctx.Done():
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

func uniqueWorkflowFailures(failures []string) []string {
	seen := make(map[string]struct{}, len(failures))
	out := make([]string, 0, len(failures))
	for _, failure := range failures {
		failure = strings.TrimSpace(failure)
		if failure == "" {
			continue
		}
		if _, ok := seen[failure]; ok {
			continue
		}
		seen[failure] = struct{}{}
		out = append(out, failure)
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

// splitAnswerIntoStreamChunks 按 unicode rune 切分整块答案，用于非 LLM 直出场景下的平滑流式推送。
func splitAnswerIntoStreamChunks(text string, chunkSize int) []string {
	if text == "" {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = 20
	}
	runes := []rune(text)
	if len(runes) <= chunkSize {
		return []string{text}
	}
	var chunks []string
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}
