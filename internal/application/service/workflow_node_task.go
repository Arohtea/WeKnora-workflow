package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	workflowruntime "github.com/Tencent/WeKnora/internal/workflow"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	workflowNodeClaimStaleAfter = 5 * time.Minute
	workflowNodeTaskTimeout     = 30 * time.Minute
	workflowMaxAutoAttempts     = 3
)

// WorkflowNodeTaskService 消费工作流单节点 durable 待办。
//
// Asynq 和 Lite executor 只负责唤醒；节点、分支、变量、重试与取消状态全部从数据库
// 读取。每次 Handle 最多执行一个节点，提交成功后再为下一批待办创建唤醒任务。
type WorkflowNodeTaskService struct {
	agent       *agentService
	pendingRepo interfaces.TaskPendingOpsRepository
	enqueuer    interfaces.TaskEnqueuer
}

// NewWorkflowNodeTaskService 创建工作流节点任务处理器。
//
// @param agentService 工作流运行时能力与持久化仓储。
// @param pendingRepo durable 待办仓储。
// @param enqueuer Redis 或 Lite 模式的统一唤醒器。
// @returns 可注册到两种任务运行时的同一 TaskHandler。
func NewWorkflowNodeTaskService(
	agentSvc interfaces.AgentService,
	pendingRepo interfaces.TaskPendingOpsRepository,
	enqueuer interfaces.TaskEnqueuer,
) (interfaces.TaskHandler, error) {
	agent, ok := agentSvc.(*agentService)
	if !ok || agent == nil {
		return nil, fmt.Errorf("workflow node task requires the built-in agent service")
	}
	if pendingRepo == nil || enqueuer == nil || agent.workflowRepo == nil {
		return nil, fmt.Errorf("workflow node task dependencies are incomplete")
	}
	return &WorkflowNodeTaskService{agent: agent, pendingRepo: pendingRepo, enqueuer: enqueuer}, nil
}

// Handle 执行一个工作流节点待办。
func (s *WorkflowNodeTaskService) Handle(ctx context.Context, task *asynq.Task) error {
	var wake types.WorkflowNodeTaskPayload
	if task == nil || json.Unmarshal(task.Payload(), &wake) != nil {
		return fmt.Errorf("decode workflow node wake payload: %w", asynq.SkipRetry)
	}
	if wake.TenantID == 0 || wake.AgentID == "" || wake.RunID == "" {
		return fmt.Errorf("workflow node wake identity is incomplete: %w", asynq.SkipRetry)
	}

	ctx = types.WithExecutionTenant(ctx, wake.TenantID)
	ctx = wake.Initiator.Apply(ctx)
	if err := s.releaseDueWorkflowRetries(ctx, wake.RunID); err != nil {
		return err
	}
	ops, err := s.pendingRepo.ClaimBatch(
		ctx,
		types.TypeWorkflowNodeExecute,
		types.TaskScopeWorkflowRun,
		wake.RunID,
		1,
		time.Now().Add(-workflowNodeClaimStaleAfter),
	)
	if err != nil {
		return fmt.Errorf("claim workflow pending node: %w", err)
	}
	if len(ops) == 0 {
		return s.scheduleNextDueRetry(ctx, wake)
	}
	op := ops[0]
	if len(ops) > 1 {
		duplicateIDs := make([]int64, 0, len(ops)-1)
		for _, duplicate := range ops[1:] {
			duplicateIDs = append(duplicateIDs, duplicate.ID)
		}
		if err := s.pendingRepo.DeleteByIDs(ctx, duplicateIDs); err != nil {
			_ = s.pendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
			return fmt.Errorf("drop duplicate workflow pending nodes: %w", err)
		}
	}

	var pending types.WorkflowPendingNodePayload
	if err := json.Unmarshal(op.Payload, &pending); err != nil {
		_ = s.pendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
		return fmt.Errorf("decode workflow pending node payload: %w", err)
	}
	if pending.Initiator.UserID != "" {
		ctx = pending.Initiator.Apply(ctx)
	}
	if pending.NotBefore != nil && time.Now().Before(*pending.NotBefore) {
		_ = s.pendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
		return s.enqueueWake(ctx, wake, time.Until(*pending.NotBefore))
	}

	if err := s.processClaimedNode(ctx, wake, op, pending); err != nil {
		if errors.Is(err, repository.ErrWorkflowNodeAlreadyCompleted) {
			return nil
		}
		return err
	}
	return nil
}

func (s *WorkflowNodeTaskService) processClaimedNode(
	ctx context.Context,
	wake types.WorkflowNodeTaskPayload,
	op *types.TaskPendingOp,
	pending types.WorkflowPendingNodePayload,
) error {
	run, err := s.agent.workflowRepo.GetRun(ctx, wake.TenantID, wake.AgentID, wake.RunID)
	if err != nil {
		_ = s.pendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
		return err
	}
	if isTerminalWorkflowRun(run.Status) {
		return s.pendingRepo.DeleteByIDs(ctx, []int64{op.ID})
	}
	if run.CancelRequestedAt != nil {
		if err := s.pendingRepo.DeleteByIDs(ctx, []int64{op.ID}); err != nil {
			return err
		}
		_, _, err := s.agent.workflowRepo.RequestRunCancel(
			context.WithoutCancel(ctx), run.TenantID, run.AgentID, run.ID, time.Now(),
		)
		return err
	}
	branch, err := s.agent.workflowRepo.GetRunBranch(ctx, wake.TenantID, wake.RunID, pending.BranchID)
	if err != nil {
		_ = s.pendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
		return err
	}

	definition, customConfig, err := decodeWorkflowRunSnapshot(run)
	if err != nil {
		return s.completeConfigurationFailure(ctx, wake, run, branch, op, pending, err)
	}
	node, ok := workflowNodeByID(definition, pending.NodeID)
	if !ok {
		return s.completeConfigurationFailure(
			ctx, wake, run, branch, op, pending, fmt.Errorf("workflow node %s is missing from snapshot", pending.NodeID),
		)
	}
	variables, err := decodeWorkflowVariables(branch.Variables)
	if err != nil {
		return s.completeConfigurationFailure(ctx, wake, run, branch, op, pending, err)
	}

	executor, err := s.newPersistentExecutor(ctx, run, definition, customConfig)
	if err != nil {
		return s.completeConfigurationFailure(ctx, wake, run, branch, op, pending, err)
	}
	nodePath := joinWorkflowBranchPath(branch.BranchPath, node.ID)
	taskID, _ := asynq.GetTaskID(ctx)
	inputSummary := executor.nodeInputSummary(node, variables)
	inputPayload := marshalRedactedWorkflowPayload(variables)
	nodeRun := &types.WorkflowRunNode{
		RunID:          run.ID,
		TenantID:       run.TenantID,
		AgentID:        run.AgentID,
		NodeID:         node.ID,
		NodeName:       node.Name,
		NodeType:       node.Type,
		BranchID:       branch.ID,
		BranchPath:     nodePath,
		Attempt:        pending.Attempt,
		RetryOf:        pending.RetryOf,
		TaskID:         taskID,
		InputPayload:   inputPayload,
		InputSummary:   sanitizeWorkflowPayloadText(inputSummary),
		InputTruncated: false,
	}
	startedEvent := &types.WorkflowRunEvent{
		EventType: string(event.EventWorkflowNodeStarted),
		Status:    types.WorkflowNodeStatusRunning,
		Summary:   nodeRun.InputSummary,
	}
	if err := s.agent.workflowRepo.StartWorkflowNode(ctx, op.ID, nodeRun, startedEvent); err != nil {
		if errors.Is(err, repository.ErrWorkflowNodeAlreadyCompleted) {
			return err
		}
		if errors.Is(err, repository.ErrWorkflowRunCancelRequested) {
			_ = s.pendingRepo.DeleteByIDs(context.WithoutCancel(ctx), []int64{op.ID})
			_, _, cancelErr := s.agent.workflowRepo.RequestRunCancel(
				context.WithoutCancel(ctx), run.TenantID, run.AgentID, run.ID, time.Now(),
			)
			return cancelErr
		}
		_ = s.pendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
		return err
	}
	s.emitPersistedWorkflowEvent(ctx, run, startedEvent)

	nodeCtx, stopMonitor, canceledByRequest := s.monitorWorkflowNode(ctx, run)
	execution, execErr := executeWorkflowNodeSafely(executor, nodeCtx, node, variables)
	stopMonitor()
	if ctx.Err() != nil && !canceledByRequest.Load() {
		_ = s.pendingRepo.ReleaseByIDs(context.WithoutCancel(ctx), []int64{op.ID})
		return ctx.Err()
	}

	completion := s.buildNodeCompletion(
		run, branch, op, pending, node, nodeRun, executor, variables, execution, execErr, canceledByRequest.Load(),
	)
	completedRun, finalized, err := s.agent.workflowRepo.CompleteWorkflowNode(context.WithoutCancel(ctx), completion)
	if err != nil {
		if errors.Is(err, repository.ErrWorkflowNodeAlreadyCompleted) {
			return err
		}
		_ = s.pendingRepo.ReleaseByIDs(context.WithoutCancel(ctx), []int64{op.ID})
		return err
	}
	s.emitPersistedWorkflowEvent(ctx, run, completion.Event)
	if finalized && completion.RunEvent != nil {
		s.emitPersistedWorkflowEvent(ctx, completedRun, completion.RunEvent)
		s.emitWorkflowTerminalEvents(ctx, completedRun)
		return nil
	}
	return s.wakeNextPendingNodes(ctx, completedRun, completion.NextPendingOps, pending.Initiator)
}

func (s *WorkflowNodeTaskService) buildNodeCompletion(
	run *types.WorkflowRun,
	branch *types.WorkflowRunBranch,
	op *types.TaskPendingOp,
	pending types.WorkflowPendingNodePayload,
	node types.WorkflowNode,
	nodeRun *types.WorkflowRunNode,
	executor *workflowExecutor,
	variables map[string]interface{},
	execution workflowNodeExecution,
	execErr error,
	canceled bool,
) *types.WorkflowNodeCompletion {
	now := time.Now()
	completion := &types.WorkflowNodeCompletion{
		PendingOpID:           op.ID,
		Node:                  nodeRun,
		Branch:                branch,
		ExpectedBranchVersion: branch.Version,
		Event:                 &types.WorkflowRunEvent{},
	}
	if nodeRun.StartedAt != nil {
		nodeRun.DurationMs = now.Sub(*nodeRun.StartedAt).Milliseconds()
	}
	nodeRun.FinishedAt = &now

	if canceled {
		nodeRun.Status = types.WorkflowNodeStatusCanceled
		nodeRun.ErrorCode = types.WorkflowErrorCodeCanceled
		nodeRun.ErrorSummary = "运行已取消"
		branch.Status = types.WorkflowBranchStatusCanceled
		branch.CurrentNodeID = ""
		branch.ErrorCode = nodeRun.ErrorCode
		branch.ErrorSummary = nodeRun.ErrorSummary
		completion.Event.EventType = string(event.EventWorkflowNodeCanceled)
		completion.Event.Status = nodeRun.Status
		completion.Event.ErrorCode = nodeRun.ErrorCode
		completion.Event.Error = nodeRun.ErrorSummary
		completion.RunErrorCode = nodeRun.ErrorCode
		completion.RunErrorSummary = nodeRun.ErrorSummary
		return completion
	}

	failureText := workflowExecutionError(execution, execErr)
	if failureText != "" {
		code, transient := classifyWorkflowNodeFailure(execution, execErr)
		retrySafe := workflowNodeRetrySafe(node)
		retryable := transient && retrySafe && pending.Attempt < workflowMaxAutoAttempts
		if transient && !retrySafe {
			code = types.WorkflowErrorCodeUnsafeReplay
		}
		nodeRun.Status = types.WorkflowNodeStatusFailed
		nodeRun.Retryable = retryable
		nodeRun.ErrorCode = code
		nodeRun.ErrorSummary, nodeRun.ErrorTruncated = sanitizeWorkflowSummary(failureText)
		nodeRun.OutputPayload = marshalRedactedWorkflowPayload(execution.output)
		if execution.output != nil {
			nodeRun.OutputSummary, nodeRun.OutputTruncated = sanitizeWorkflowSummary(execution.output.Text)
		}
		if encoded, err := json.Marshal(execution.usage); err == nil {
			nodeRun.Usage = types.JSON(encoded)
			completion.RunUsage = nodeRun.Usage
		}
		completion.Event.EventType = string(event.EventWorkflowNodeFailed)
		completion.Event.Status = nodeRun.Status
		completion.Event.Summary = nodeRun.OutputSummary
		completion.Event.ErrorCode = code
		completion.Event.Error = nodeRun.ErrorSummary
		completion.Event.DurationMs = nodeRun.DurationMs

		if retryable {
			retryAt := time.Now().Add(workflowRetryDelay(pending.Attempt))
			branch.Status = types.WorkflowBranchStatusPending
			branch.CurrentNodeID = node.ID
			branch.ErrorCode = ""
			branch.ErrorSummary = ""
			completion.NextPendingOps = []*types.TaskPendingOp{
				newWorkflowPendingNode(run, branch.ID, node.ID, pending.Attempt+1, nodeRun.ID, pending.Initiator, &retryAt),
			}
			claimedAt := time.Now()
			completion.NextPendingOps[0].ClaimedAt = &claimedAt
			return completion
		}

		executor.publishFailedNodeOutput(node, execution, variables)
		handled := matchingWorkflowFailureEdges(executor, node, variables)
		if len(handled) == 0 {
			branch.Status = types.WorkflowBranchStatusFailed
			branch.CurrentNodeID = ""
			branch.ErrorCode = code
			branch.ErrorSummary = nodeRun.ErrorSummary
			completion.RunErrorCode = code
			completion.RunErrorSummary = nodeRun.ErrorSummary
		} else {
			branch.ErrorCode = ""
			branch.ErrorSummary = ""
			applyWorkflowNextEdges(run, branch, pending.Initiator, joinWorkflowBranchPath(branch.BranchPath, node.ID), variables, handled, completion)
		}
		branch.Variables = marshalWorkflowVariables(variables)
		return completion
	}

	output := execution.output
	if output == nil {
		output = &types.WorkflowNodeOutput{Status: "success", Data: map[string]interface{}{}}
	}
	if output.Data == nil {
		output.Data = map[string]interface{}{}
	}
	if output.Status == "" {
		output.Status = "success"
	}
	nodes, _ := variables["nodes"].(map[string]interface{})
	if nodes == nil {
		nodes = map[string]interface{}{}
		variables["nodes"] = nodes
	}
	nodes[node.ID] = output
	nodeRun.Status = types.WorkflowNodeStatusSucceeded
	nodeRun.OutputPayload = marshalRedactedWorkflowPayload(output)
	nodeRun.OutputSummary, nodeRun.OutputTruncated = sanitizeWorkflowSummary(output.Text)
	if encoded, err := json.Marshal(execution.usage); err == nil {
		nodeRun.Usage = types.JSON(encoded)
		completion.RunUsage = nodeRun.Usage
	}
	completion.Event.EventType = string(event.EventWorkflowNodeCompleted)
	completion.Event.Status = nodeRun.Status
	completion.Event.Summary = nodeRun.OutputSummary
	completion.Event.DurationMs = nodeRun.DurationMs
	branch.Variables = marshalWorkflowVariables(variables)
	branch.ErrorCode = ""
	branch.ErrorSummary = ""

	if node.Type == types.WorkflowNodeTypeEnd {
		branch.Status = types.WorkflowBranchStatusSucceeded
		branch.CurrentNodeID = ""
		completion.RunOutputSummary = output.Text
		return completion
	}
	matched, routeErr := executor.matchOutgoingEdges(node, variables)
	if routeErr != nil || len(matched) == 0 {
		branch.Status = types.WorkflowBranchStatusFailed
		branch.CurrentNodeID = ""
		completion.RunErrorCode = types.WorkflowErrorCodeNoMatchingBranch
		if routeErr != nil {
			completion.RunErrorSummary, _ = sanitizeWorkflowSummary(routeErr.Error)
		} else {
			completion.RunErrorSummary = fmt.Sprintf("%s 没有命中的路由", node.Name)
		}
		branch.ErrorCode = completion.RunErrorCode
		branch.ErrorSummary = completion.RunErrorSummary
		return completion
	}
	applyWorkflowNextEdges(
		run, branch, pending.Initiator, joinWorkflowBranchPath(branch.BranchPath, node.ID), variables, matched, completion,
	)
	return completion
}

func applyWorkflowNextEdges(
	run *types.WorkflowRun,
	branch *types.WorkflowRunBranch,
	initiator types.TaskInitiator,
	nodePath string,
	variables map[string]interface{},
	edges []types.WorkflowEdge,
	completion *types.WorkflowNodeCompletion,
) {
	if len(edges) == 1 {
		branch.BranchPath = nodePath
		branch.CurrentNodeID = edges[0].Target
		branch.Status = types.WorkflowBranchStatusPending
		branch.ErrorCode = ""
		branch.ErrorSummary = ""
		branch.Variables = marshalWorkflowVariables(variables)
		completion.NextPendingOps = []*types.TaskPendingOp{
			newWorkflowPendingNode(run, branch.ID, edges[0].Target, 1, 0, initiator, nil),
		}
		return
	}

	branch.BranchPath = nodePath
	branch.CurrentNodeID = ""
	branch.Status = types.WorkflowBranchStatusForked
	branch.ErrorCode = ""
	branch.ErrorSummary = ""
	branch.Variables = marshalWorkflowVariables(variables)
	for _, edge := range edges {
		childID := uuid.NewString()
		child := &types.WorkflowRunBranch{
			ID:            childID,
			RunID:         run.ID,
			TenantID:      run.TenantID,
			AgentID:       run.AgentID,
			ParentID:      branch.ID,
			BranchPath:    nodePath,
			CurrentNodeID: edge.Target,
			Variables:     marshalWorkflowVariables(workflowruntime.CloneVariables(variables)),
			Status:        types.WorkflowBranchStatusPending,
			Version:       1,
		}
		completion.ChildBranches = append(completion.ChildBranches, child)
		completion.NextPendingOps = append(
			completion.NextPendingOps,
			newWorkflowPendingNode(run, childID, edge.Target, 1, 0, initiator, nil),
		)
	}
}

func (s *WorkflowNodeTaskService) newPersistentExecutor(
	ctx context.Context,
	run *types.WorkflowRun,
	definition *types.WorkflowDefinition,
	config *types.CustomAgentConfig,
) (*workflowExecutor, error) {
	if config.ModelID == "" {
		return nil, fmt.Errorf("workflow model is not configured")
	}
	model, err := s.agent.modelService.GetChatModel(ctx, config.ModelID)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow model %s: %w", config.ModelID, err)
	}
	runtimeConfig := workflowRuntimeConfig(ctx, s.agent, config, run.TenantID)
	var resolvedReranker rerank.Reranker
	if config.RerankModelID != "" && s.agent.modelService != nil {
		if value, resolveErr := s.agent.modelService.GetRerankModel(ctx, config.RerankModelID); resolveErr == nil {
			resolvedReranker = value
		} else {
			logger.Warnf(ctx, "workflow run %s continues without rerank model %s: %v", run.ID, config.RerankModelID, resolveErr)
		}
	}
	nodes := make(map[string]types.WorkflowNode, len(definition.Nodes))
	outgoing := make(map[string][]types.WorkflowEdge, len(definition.Nodes))
	incoming := make(map[string][]types.WorkflowEdge, len(definition.Nodes))
	for _, node := range definition.Nodes {
		nodes[node.ID] = node
	}
	for _, edge := range definition.Edges {
		outgoing[edge.Source] = append(outgoing[edge.Source], edge)
		incoming[edge.Target] = append(incoming[edge.Target], edge)
	}
	for nodeID := range outgoing {
		outgoing[nodeID] = workflowruntime.SortedOutgoingEdges(outgoing[nodeID])
	}
	inputQuery := ""
	var input map[string]interface{}
	if json.Unmarshal(run.InputPayload, &input) == nil {
		inputQuery, _ = input["query"].(string)
	}
	sessionID := run.SessionID
	if sessionID == "" {
		sessionID = "workflow-" + run.ID
	}
	messageID := run.MessageID
	if messageID == "" {
		messageID = run.ID
	}
	return &workflowExecutor{
		ctx:                ctx,
		runtime:            s.agent,
		config:             runtimeConfig,
		model:              model,
		rerankModel:        resolvedReranker,
		definition:         definition,
		eventBus:           s.agent.eventBus,
		sessionID:          sessionID,
		assistantMessage:   messageID,
		requestID:          run.RequestID,
		inputQuery:         inputQuery,
		nodes:              nodes,
		outgoing:           outgoing,
		incoming:           incoming,
		finalAnswerEventID: generateEventID("workflow-answer"),
	}, nil
}

func workflowRuntimeConfig(
	ctx context.Context,
	agent *agentService,
	config *types.CustomAgentConfig,
	tenantID uint64,
) *types.AgentConfig {
	targets := make(types.SearchTargets, 0, len(config.KnowledgeBases))
	for _, kbID := range config.KnowledgeBases {
		targets = append(targets, &types.SearchTarget{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: kbID, TenantID: tenantID,
		})
	}
	runtimeConfig := &types.AgentConfig{
		MaxIterations:               config.MaxIterations,
		AllowedTools:                append([]string(nil), config.AllowedTools...),
		Temperature:                 config.Temperature,
		KnowledgeBases:              append([]string(nil), config.KnowledgeBases...),
		SearchTargets:               targets,
		SystemPrompt:                config.SystemPrompt,
		UseCustomSystemPrompt:       strings.TrimSpace(config.SystemPrompt) != "",
		WebSearchEnabled:            config.WebSearchEnabled,
		WebSearchMaxResults:         config.WebSearchMaxResults,
		WebSearchProviderID:         config.WebSearchProviderID,
		MemoryEnabled:               config.MemoryEnabled,
		MCPSelectionMode:            config.MCPSelectionMode,
		MCPServices:                 append([]string(nil), config.MCPServices...),
		MCPAuthWaitTimeout:          config.MCPAuthWaitTimeout,
		Thinking:                    config.Thinking,
		CitationEnabled:             config.CitationEnabled,
		RetrieveKBOnlyWhenMentioned: config.RetrieveKBOnlyWhenMentioned,
		RetainRetrievalHistory:      config.RetainRetrievalHistory,
		SkillsEnabled:               config.SkillsSelectionMode != "none" && len(config.SelectedSkills) > 0,
		AllowedSkills:               append([]string(nil), config.SelectedSkills...),
		SandboxConfigID:             config.SandboxConfigID,
		LLMCallTimeout:              config.LLMCallTimeout,
		MaxCompletionTokens:         config.MaxCompletionTokens,
		VLMModelID:                  config.VLMModelID,
	}
	if runtimeConfig.WebSearchMaxResults <= 0 {
		runtimeConfig.WebSearchMaxResults = 5
	}
	if agent != nil && agent.db != nil && strings.TrimSpace(config.SandboxConfigID) != "" {
		runtimeConfig.TenantSkills = effectiveTenantSkills(
			ctx,
			repository.NewTenantSandboxConfigRepository(agent.db),
			repository.NewTenantSkillRepository(agent.db),
			tenantID,
			config.SandboxConfigID,
		)
	}
	return runtimeConfig
}

func decodeWorkflowRunSnapshot(
	run *types.WorkflowRun,
) (*types.WorkflowDefinition, *types.CustomAgentConfig, error) {
	var definition types.WorkflowDefinition
	if err := json.Unmarshal(run.DefinitionSnapshot, &definition); err != nil {
		return nil, nil, fmt.Errorf("decode workflow definition snapshot: %w", err)
	}
	if err := workflowruntime.MigrateDefinition(&definition); err != nil {
		return nil, nil, err
	}
	var config types.CustomAgentConfig
	if err := json.Unmarshal(run.ConfigSnapshot, &config); err != nil {
		return nil, nil, fmt.Errorf("decode workflow config snapshot: %w", err)
	}
	config.AgentMode = types.AgentModeSmartReasoning
	config.AgentType = types.AgentTypeWorkflow
	config.Workflow = &definition
	if err := workflowruntime.ValidatePublishedConfig(&config); err != nil {
		return nil, nil, err
	}
	return &definition, &config, nil
}

func decodeWorkflowVariables(payload types.JSON) (map[string]interface{}, error) {
	variables := map[string]interface{}{}
	if err := json.Unmarshal(payload, &variables); err != nil {
		return nil, fmt.Errorf("decode workflow branch variables: %w", err)
	}
	if _, ok := variables["input"].(map[string]interface{}); !ok {
		return nil, fmt.Errorf("workflow branch input variables are missing")
	}
	if _, ok := variables["nodes"].(map[string]interface{}); !ok {
		variables["nodes"] = map[string]interface{}{}
	}
	return variables, nil
}

func workflowNodeByID(definition *types.WorkflowDefinition, nodeID string) (types.WorkflowNode, bool) {
	if definition == nil {
		return types.WorkflowNode{}, false
	}
	for _, node := range definition.Nodes {
		if node.ID == nodeID {
			return node, true
		}
	}
	return types.WorkflowNode{}, false
}

func executeWorkflowNodeSafely(
	executor *workflowExecutor,
	ctx context.Context,
	node types.WorkflowNode,
	variables map[string]interface{},
) (execution workflowNodeExecution, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("workflow node panicked: %v", recovered)
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"node_id": node.ID,
				"stack":   string(debug.Stack()),
			})
		}
	}()
	return executor.executeNode(ctx, node, variables, "workflow-"+uuid.NewString())
}

func workflowExecutionError(execution workflowNodeExecution, execErr error) string {
	if execErr != nil {
		return execErr.Error()
	}
	if execution.result != nil && !execution.result.Success {
		if strings.TrimSpace(execution.result.Error) != "" {
			return execution.result.Error
		}
		return "节点执行失败"
	}
	return ""
}

func classifyWorkflowNodeFailure(execution workflowNodeExecution, err error) (string, bool) {
	if errors.Is(err, context.DeadlineExceeded) {
		return types.WorkflowErrorCodeTimeout, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return types.WorkflowErrorCodeTimeout, true
		}
		return types.WorkflowErrorCodeNetwork, true
	}
	if execution.output != nil && execution.output.Data != nil {
		statusCode, _ := numericHTTPStatus(execution.output.Data["status_code"])
		switch {
		case statusCode == http.StatusTooManyRequests:
			return types.WorkflowErrorCodeRateLimited, true
		case statusCode >= 500:
			return types.WorkflowErrorCodeUpstream, true
		case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
			return types.WorkflowErrorCodePermission, false
		case statusCode >= 400:
			return types.WorkflowErrorCodeConfig, false
		}
	}
	message := strings.ToLower(workflowExecutionError(execution, err))
	switch {
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return types.WorkflowErrorCodeTimeout, true
	case strings.Contains(message, "rate limit"), strings.Contains(message, "too many requests"):
		return types.WorkflowErrorCodeRateLimited, true
	case strings.Contains(message, "connection reset"), strings.Contains(message, "connection refused"),
		strings.Contains(message, "temporary"), strings.Contains(message, "network"):
		return types.WorkflowErrorCodeNetwork, true
	case strings.Contains(message, "permission"), strings.Contains(message, "forbidden"), strings.Contains(message, "unauthorized"):
		return types.WorkflowErrorCodePermission, false
	default:
		return types.WorkflowErrorCodeConfig, false
	}
}

func numericHTTPStatus(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func workflowNodeRetrySafe(node types.WorkflowNode) bool {
	switch node.Type {
	case types.WorkflowNodeTypeStart,
		types.WorkflowNodeTypeEnd,
		types.WorkflowNodeTypeLLM,
		types.WorkflowNodeTypeLLMDecision,
		types.WorkflowNodeTypeRetrieval:
		return true
	case types.WorkflowNodeTypeHTTP:
		var config types.WorkflowHTTPNodeConfig
		if json.Unmarshal(node.Config, &config) != nil {
			return false
		}
		switch strings.ToUpper(strings.TrimSpace(config.Method)) {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return true
		default:
			return strings.TrimSpace(config.IdempotencyKeyTemplate) != ""
		}
	case types.WorkflowNodeTypeTool:
		var config types.WorkflowToolNodeConfig
		if json.Unmarshal(node.Config, &config) != nil {
			return false
		}
		if config.Kind == types.WorkflowToolKindBuiltin {
			return true
		}
		return config.RetrySafe
	default:
		return false
	}
}

func matchingWorkflowFailureEdges(
	executor *workflowExecutor,
	node types.WorkflowNode,
	variables map[string]interface{},
) []types.WorkflowEdge {
	edges := make([]types.WorkflowEdge, 0)
	for _, edge := range executor.outgoing[node.ID] {
		if edge.Condition == nil || edge.IsDefault {
			continue
		}
		matched, err := workflowruntime.EvaluateCondition(edge.Condition, variables)
		if err == nil && matched {
			edges = append(edges, edge)
		}
	}
	return workflowruntime.SortedOutgoingEdges(edges)
}

func newWorkflowPendingNode(
	run *types.WorkflowRun,
	branchID, nodeID string,
	attempt int,
	retryOf int64,
	initiator types.TaskInitiator,
	notBefore *time.Time,
) *types.TaskPendingOp {
	payload, _ := json.Marshal(types.WorkflowPendingNodePayload{
		BranchID: branchID, NodeID: nodeID, Attempt: attempt, RetryOf: retryOf,
		NotBefore: notBefore, Initiator: initiator,
	})
	return &types.TaskPendingOp{
		TenantID: run.TenantID,
		TaskType: types.TypeWorkflowNodeExecute,
		Scope:    types.TaskScopeWorkflowRun,
		ScopeID:  run.ID,
		Op:       "execute",
		DedupKey: fmt.Sprintf("%s:%s:%d", branchID, nodeID, attempt),
		Payload:  payload,
	}
}

func workflowRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(1<<min(attempt, 5)) * time.Second
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func (s *WorkflowNodeTaskService) monitorWorkflowNode(
	ctx context.Context,
	run *types.WorkflowRun,
) (context.Context, func(), *atomic.Bool) {
	nodeCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	canceledByRequest := &atomic.Bool{}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-nodeCtx.Done():
				return
			case now := <-ticker.C:
				var state struct {
					Status            string
					CancelRequestedAt *time.Time
				}
				err := s.agent.db.WithContext(context.WithoutCancel(ctx)).
					Model(&types.WorkflowRun{}).
					Select("status", "cancel_requested_at").
					Where("id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
					Take(&state).Error
				if err != nil {
					logger.Warnf(ctx, "workflow run %s cancellation poll failed: %v", run.ID, err)
					continue
				}
				_ = s.agent.workflowRepo.HeartbeatRun(context.WithoutCancel(ctx), run.TenantID, run.AgentID, run.ID, now)
				if state.CancelRequestedAt != nil || state.Status == types.WorkflowRunStatusCanceled {
					canceledByRequest.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	var stoppedOnce atomic.Bool
	stop := func() {
		if stoppedOnce.CompareAndSwap(false, true) {
			close(stopped)
			cancel()
		}
	}
	return nodeCtx, stop, canceledByRequest
}

func (s *WorkflowNodeTaskService) completeConfigurationFailure(
	ctx context.Context,
	wake types.WorkflowNodeTaskPayload,
	run *types.WorkflowRun,
	branch *types.WorkflowRunBranch,
	op *types.TaskPendingOp,
	pending types.WorkflowPendingNodePayload,
	failure error,
) error {
	now := time.Now()
	nodeRun := &types.WorkflowRunNode{
		RunID: run.ID, TenantID: run.TenantID, AgentID: run.AgentID,
		NodeID: pending.NodeID, NodeName: pending.NodeID, NodeType: "unknown",
		BranchID: branch.ID, BranchPath: joinWorkflowBranchPath(branch.BranchPath, pending.NodeID),
		Attempt: pending.Attempt, RetryOf: pending.RetryOf,
	}
	started := &types.WorkflowRunEvent{
		EventType: string(event.EventWorkflowNodeStarted), Status: types.WorkflowNodeStatusRunning,
	}
	if err := s.agent.workflowRepo.StartWorkflowNode(ctx, op.ID, nodeRun, started); err != nil {
		return err
	}
	s.emitPersistedWorkflowEvent(ctx, run, started)
	nodeRun.Status = types.WorkflowNodeStatusFailed
	nodeRun.ErrorCode = types.WorkflowErrorCodeConfig
	nodeRun.ErrorSummary, nodeRun.ErrorTruncated = sanitizeWorkflowSummary(failure.Error())
	nodeRun.FinishedAt = &now
	if nodeRun.StartedAt != nil {
		nodeRun.DurationMs = now.Sub(*nodeRun.StartedAt).Milliseconds()
	}
	branch.Status = types.WorkflowBranchStatusFailed
	branch.CurrentNodeID = ""
	branch.ErrorCode = nodeRun.ErrorCode
	branch.ErrorSummary = nodeRun.ErrorSummary
	completion := &types.WorkflowNodeCompletion{
		PendingOpID: op.ID, Node: nodeRun, Branch: branch, ExpectedBranchVersion: branch.Version,
		Event: &types.WorkflowRunEvent{
			EventType: string(event.EventWorkflowNodeFailed), Status: types.WorkflowNodeStatusFailed,
			ErrorCode: nodeRun.ErrorCode, Error: nodeRun.ErrorSummary,
		},
		RunErrorCode: nodeRun.ErrorCode, RunErrorSummary: nodeRun.ErrorSummary,
	}
	completed, finalized, err := s.agent.workflowRepo.CompleteWorkflowNode(context.WithoutCancel(ctx), completion)
	if err != nil {
		return err
	}
	s.emitPersistedWorkflowEvent(ctx, run, completion.Event)
	if finalized {
		s.emitPersistedWorkflowEvent(ctx, completed, completion.RunEvent)
		s.emitWorkflowTerminalEvents(ctx, completed)
	}
	return nil
}

func (s *WorkflowNodeTaskService) releaseDueWorkflowRetries(ctx context.Context, runID string) error {
	ops, err := s.pendingRepo.PeekBatch(ctx, types.TypeWorkflowNodeExecute, types.TaskScopeWorkflowRun, runID, 200)
	if err != nil {
		return err
	}
	now := time.Now()
	ids := make([]int64, 0)
	for _, op := range ops {
		if op == nil || op.ClaimedAt == nil {
			continue
		}
		var payload types.WorkflowPendingNodePayload
		if json.Unmarshal(op.Payload, &payload) == nil && payload.NotBefore != nil && !payload.NotBefore.After(now) {
			ids = append(ids, op.ID)
		}
	}
	return s.pendingRepo.ReleaseByIDs(ctx, ids)
}

func (s *WorkflowNodeTaskService) scheduleNextDueRetry(
	ctx context.Context,
	wake types.WorkflowNodeTaskPayload,
) error {
	ops, err := s.pendingRepo.PeekBatch(ctx, types.TypeWorkflowNodeExecute, types.TaskScopeWorkflowRun, wake.RunID, 200)
	if err != nil {
		return err
	}
	var next *time.Time
	for _, op := range ops {
		var payload types.WorkflowPendingNodePayload
		if op != nil && json.Unmarshal(op.Payload, &payload) == nil && payload.NotBefore != nil && payload.NotBefore.After(time.Now()) {
			if next == nil || payload.NotBefore.Before(*next) {
				candidate := *payload.NotBefore
				next = &candidate
			}
		}
	}
	if next == nil {
		return nil
	}
	return s.enqueueWake(ctx, wake, time.Until(*next))
}

func (s *WorkflowNodeTaskService) wakeNextPendingNodes(
	ctx context.Context,
	run *types.WorkflowRun,
	ops []*types.TaskPendingOp,
	initiator types.TaskInitiator,
) error {
	if run == nil || len(ops) == 0 {
		return nil
	}
	wake := types.WorkflowNodeTaskPayload{
		TenantID: run.TenantID, AgentID: run.AgentID, RunID: run.ID, Initiator: initiator,
	}
	for _, op := range ops {
		delay := time.Duration(0)
		if op != nil {
			var payload types.WorkflowPendingNodePayload
			if json.Unmarshal(op.Payload, &payload) == nil && payload.NotBefore != nil {
				delay = time.Until(*payload.NotBefore)
			}
		}
		if err := s.enqueueWake(ctx, wake, delay); err != nil {
			return err
		}
	}
	return nil
}

func (s *WorkflowNodeTaskService) enqueueWake(
	ctx context.Context,
	wake types.WorkflowNodeTaskPayload,
	delay time.Duration,
) error {
	payload, err := json.Marshal(wake)
	if err != nil {
		return err
	}
	options := []asynq.Option{
		asynq.Queue(types.QueueWorkflow),
		asynq.MaxRetry(3),
		asynq.Timeout(workflowNodeTaskTimeout),
	}
	if delay > 0 {
		options = append(options, asynq.ProcessIn(delay))
	}
	_, err = s.enqueuer.Enqueue(asynq.NewTask(types.TypeWorkflowNodeExecute, payload), options...)
	if err != nil {
		return fmt.Errorf("enqueue workflow run %s wake: %w", wake.RunID, err)
	}
	logger.Infof(ctx, "workflow wake enqueued run_id=%s agent_id=%s delay=%s", wake.RunID, wake.AgentID, max(delay, 0))
	return nil
}

func (s *WorkflowNodeTaskService) emitPersistedWorkflowEvent(
	ctx context.Context,
	run *types.WorkflowRun,
	row *types.WorkflowRunEvent,
) {
	if s.agent.eventBus == nil || run == nil || row == nil {
		return
	}
	data := types.WorkflowLifecycleEventData{
		EventID: row.EventID, Sequence: row.Sequence, RunID: row.RunID,
		WorkflowVersion: run.WorkflowVersion, NodeID: row.NodeID, BranchPath: row.BranchPath,
		Attempt: row.Attempt, Status: row.Status, OccurredAt: row.OccurredAt,
		DurationMs: row.DurationMs, Summary: row.Summary, SummaryTruncated: row.SummaryTruncated,
		ErrorCode: row.ErrorCode, Error: row.Error,
	}
	if err := s.agent.eventBus.Emit(context.WithoutCancel(ctx), event.Event{
		ID: row.EventID, Type: event.EventType(row.EventType), SessionID: run.SessionID,
		RequestID: run.RequestID, Data: data,
	}); err != nil {
		logger.Warnf(ctx, "emit workflow event %s for run %s: %v", row.EventType, run.ID, err)
	}
}

func (s *WorkflowNodeTaskService) emitWorkflowTerminalEvents(ctx context.Context, run *types.WorkflowRun) {
	if s.agent.eventBus == nil || run == nil || run.SessionID == "" {
		return
	}
	switch run.Status {
	case types.WorkflowRunStatusFailed:
		_ = s.agent.eventBus.Emit(context.WithoutCancel(ctx), event.Event{
			ID: generateEventID("workflow-error"), Type: event.EventError,
			SessionID: run.SessionID, RequestID: run.RequestID,
			Data: event.ErrorData{
				Error: run.ErrorSummary, Stage: "workflow_execution", SessionID: run.SessionID,
			},
		})
	case types.WorkflowRunStatusSucceeded, types.WorkflowRunStatusPartial:
		if strings.TrimSpace(run.OutputSummary) != "" {
			_ = s.agent.eventBus.Emit(context.WithoutCancel(ctx), event.Event{
				ID: generateEventID("workflow-answer"), Type: event.EventAgentFinalAnswer,
				SessionID: run.SessionID, RequestID: run.RequestID,
				Data: event.AgentFinalAnswerData{Content: run.OutputSummary, Done: true},
			})
		}
	}
	var usage interface{}
	if len(run.Usage) > 0 {
		var decoded types.TokenUsage
		if json.Unmarshal(run.Usage, &decoded) == nil {
			usage = &decoded
		}
	}
	_ = s.agent.eventBus.Emit(context.WithoutCancel(ctx), event.Event{
		ID: generateEventID("workflow-complete"), Type: event.EventAgentComplete,
		SessionID: run.SessionID, RequestID: run.RequestID,
		Data: event.AgentCompleteData{
			SessionID: run.SessionID, FinalAnswer: run.OutputSummary, Usage: usage,
			TotalDurationMs: run.DurationMs, MessageID: run.MessageID, RequestID: run.RequestID,
			Extra: map[string]interface{}{
				"workflow_run_id": run.ID, "workflow_run_status": run.Status,
			},
		},
	})
}

func marshalWorkflowVariables(value interface{}) types.JSON {
	payload, _ := json.Marshal(value)
	return types.JSON(payload)
}

func marshalRedactedWorkflowPayload(value interface{}) types.JSON {
	payload, _ := json.Marshal(redactWorkflowPayloadValue(value, ""))
	return types.JSON(payload)
}

func redactWorkflowPayloadValue(value interface{}, key string) interface{} {
	if isWorkflowSensitivePayloadKey(key) {
		return types.RedactedSecretPlaceholder
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for childKey, child := range typed {
			out[childKey] = redactWorkflowPayloadValue(child, childKey)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = redactWorkflowPayloadValue(child, key)
		}
		return out
	case *types.WorkflowNodeOutput:
		if typed == nil {
			return nil
		}
		return map[string]interface{}{
			"text": typed.Text, "status": typed.Status,
			"data": redactWorkflowPayloadValue(typed.Data, "data"),
		}
	case types.WorkflowNodeOutput:
		copy := typed
		return redactWorkflowPayloadValue(&copy, key)
	default:
		return value
	}
}

func isWorkflowSensitivePayloadKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	for _, candidate := range workflowSensitiveSummaryKeys {
		candidate = strings.ReplaceAll(strings.ToLower(candidate), "-", "_")
		if normalized == candidate || strings.HasSuffix(normalized, "_"+candidate) {
			return true
		}
	}
	return false
}

func isTerminalWorkflowRun(status string) bool {
	switch status {
	case types.WorkflowRunStatusSucceeded,
		types.WorkflowRunStatusPartial,
		types.WorkflowRunStatusFailed,
		types.WorkflowRunStatusCanceled:
		return true
	default:
		return false
	}
}

func sortedWorkflowAnswers(nodes []types.WorkflowRunNode) string {
	answers := make([]types.WorkflowRunNode, 0)
	for _, node := range nodes {
		if node.NodeType == types.WorkflowNodeTypeEnd && node.Status == types.WorkflowNodeStatusSucceeded && strings.TrimSpace(node.OutputSummary) != "" {
			answers = append(answers, node)
		}
	}
	sort.SliceStable(answers, func(i, j int) bool {
		if answers[i].BranchPath == answers[j].BranchPath {
			return answers[i].ID < answers[j].ID
		}
		return answers[i].BranchPath < answers[j].BranchPath
	})
	parts := make([]string, 0, len(answers))
	for _, node := range answers {
		parts = append(parts, node.OutputSummary)
	}
	return strings.Join(parts, "\n\n")
}
