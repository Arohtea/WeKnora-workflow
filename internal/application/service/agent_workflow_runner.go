package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	workflowruntime "github.com/Tencent/WeKnora/internal/workflow"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// StartWorkflowRun 原子创建工作流运行检查点并发出一个轻量唤醒任务。
//
// 数据库提交是执行状态的唯一事实来源；即使唤醒任务发送失败，启动补偿也会在
// 下次进程启动时根据 task_pending_ops 重建任务。相同幂等键重复调用不会创建第二次运行。
func (s *agentService) StartWorkflowRun(
	ctx context.Context,
	run *types.WorkflowRun,
	branch *types.WorkflowRunBranch,
	pending *types.TaskPendingOp,
	started *types.WorkflowRunEvent,
) (*types.WorkflowRun, bool, error) {
	if s == nil || s.workflowRepo == nil || s.taskEnqueuer == nil {
		return nil, false, fmt.Errorf("workflow runtime queue is unavailable")
	}
	persisted, created, err := s.workflowRepo.CreateRunWithInitialNode(ctx, run, branch, pending, started)
	if err != nil {
		return nil, false, err
	}
	if persisted == nil || isTerminalWorkflowRun(persisted.Status) {
		return persisted, created, nil
	}
	initiator := types.TaskInitiatorFromContext(ctx)
	if pending != nil && len(pending.Payload) > 0 {
		var pendingPayload types.WorkflowPendingNodePayload
		if err := json.Unmarshal(pending.Payload, &pendingPayload); err == nil && pendingPayload.Initiator.UserID != "" {
			initiator = pendingPayload.Initiator
		}
	}
	payload, err := json.Marshal(types.WorkflowNodeTaskPayload{
		TenantID:  persisted.TenantID,
		AgentID:   persisted.AgentID,
		RunID:     persisted.ID,
		Initiator: initiator,
	})
	if err != nil {
		return persisted, created, err
	}
	options := []asynq.Option{
		asynq.Queue(types.QueueWorkflow),
		asynq.MaxRetry(3),
		asynq.Timeout(workflowNodeTaskTimeout),
		asynq.TaskID("workflow-" + persisted.ID),
	}
	if _, err := s.taskEnqueuer.Enqueue(asynq.NewTask(types.TypeWorkflowNodeExecute, payload), options...); err != nil {
		if !errors.Is(err, asynq.ErrTaskIDConflict) && !errors.Is(err, asynq.ErrDuplicateTask) {
			logger.Warnf(ctx, "workflow run %s persisted but wake enqueue failed: %v", persisted.ID, err)
			return persisted, created, fmt.Errorf("enqueue workflow run %s wake: %w", persisted.ID, err)
		}
	}
	return persisted, created, nil
}

var _ interface {
	StartWorkflowRun(context.Context, *types.WorkflowRun, *types.WorkflowRunBranch, *types.TaskPendingOp, *types.WorkflowRunEvent) (*types.WorkflowRun, bool, error)
} = (*agentService)(nil)

// StartWorkflowDebugRun 基于当前草稿创建一次编辑器内试跑。
func (s *agentService) StartWorkflowDebugRun(
	ctx context.Context,
	agentID string,
	expectedRevision int64,
	input types.WorkflowDebugInput,
	idempotencyKey string,
) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	agent, err := s.loadWorkflowAgent(ctx, tenantID, agentID)
	if err != nil {
		return nil, err
	}
	if agent.DraftRevision != expectedRevision {
		return nil, fmt.Errorf("%w: expected %d got %d", repository.ErrWorkflowRevisionConflict, expectedRevision, agent.DraftRevision)
	}
	if err := workflowruntime.NormalizeDraftConfig(&agent.Config); err != nil {
		return nil, err
	}
	if err := workflowruntime.ValidatePublishedConfig(&agent.Config); err != nil {
		return nil, err
	}
	if issues := workflowruntime.ValidateIssues(agent.Config.Workflow); len(issues) > 0 {
		return nil, fmt.Errorf("workflow validation failed: %s", issues[0].Message)
	}
	if err := s.ValidateWorkflowResources(ctx, &agent.Config); err != nil {
		return nil, err
	}
	definitionJSON, err := json.Marshal(agent.Config.Workflow)
	if err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(agent.Config)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = "debug:" + uuid.NewString()
	}
	run, branch, pending, started := newWorkflowRunSeed(
		tenantID, agentID, types.WorkflowRunModeDebug, types.WorkflowTriggerDebug,
		expectedRevision, 0, definitionJSON, configJSON, input, idempotencyKey,
	)
	applyWorkflowRequestMetadata(ctx, run, pending)
	return s.startWorkflowRunFromSeed(ctx, run, branch, pending, started)
}

// RetryWorkflowRun 使用原运行快照创建一次全量重跑。
func (s *agentService) RetryWorkflowRun(ctx context.Context, agentID, runID string) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	original, err := s.workflowRepo.GetRun(ctx, tenantID, agentID, runID)
	if err != nil {
		return nil, err
	}
	var input types.WorkflowDebugInput
	if err := json.Unmarshal(original.InputPayload, &input); err != nil {
		return nil, fmt.Errorf("decode workflow input: %w", err)
	}
	newRun, branch, pending, started := newWorkflowRunSeed(
		tenantID, agentID, original.RunMode, original.TriggerSource,
		original.DraftRevision, original.WorkflowVersion, original.DefinitionSnapshot,
		original.ConfigSnapshot, input, "retry:"+original.ID+":"+uuid.NewString(),
	)
	newRun.RetryOfRunID = original.ID
	newRun.SessionID = original.SessionID
	newRun.MessageID = original.MessageID
	applyWorkflowRequestMetadata(ctx, newRun, pending)
	return s.startWorkflowRunFromSeed(ctx, newRun, branch, pending, started)
}

// RetryWorkflowNode 从失败节点的上游变量检查点创建一次节点继续运行。
func (s *agentService) RetryWorkflowNode(ctx context.Context, agentID, runID string, nodeRunID int64) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	original, err := s.workflowRepo.GetRun(ctx, tenantID, agentID, runID)
	if err != nil {
		return nil, err
	}
	var failed *types.WorkflowRunNode
	for i := range original.Nodes {
		if original.Nodes[i].ID == nodeRunID {
			failed = &original.Nodes[i]
			break
		}
	}
	if failed == nil || failed.Status != types.WorkflowNodeStatusFailed {
		return nil, fmt.Errorf("workflow node attempt is not retryable")
	}
	branch, err := s.workflowRepo.GetRunBranch(ctx, tenantID, runID, failed.BranchID)
	if err != nil {
		return nil, err
	}
	var input types.WorkflowDebugInput
	if err := json.Unmarshal(original.InputPayload, &input); err != nil {
		return nil, fmt.Errorf("decode workflow input: %w", err)
	}
	newRun, newBranch, pending, started := newWorkflowRunSeed(
		tenantID, agentID, original.RunMode, original.TriggerSource,
		original.DraftRevision, original.WorkflowVersion, original.DefinitionSnapshot,
		original.ConfigSnapshot, input, "node-retry:"+original.ID+":"+uuid.NewString(),
	)
	newRun.RetryOfRunID = original.ID
	newRun.SessionID = original.SessionID
	newRun.MessageID = original.MessageID
	applyWorkflowRequestMetadata(ctx, newRun, pending)
	newBranch.BranchPath = branch.BranchPath
	newBranch.Variables = append(types.JSON(nil), branch.Variables...)
	newBranch.CurrentNodeID = failed.NodeID
	pending = newWorkflowPendingNode(newRun, newBranch.ID, failed.NodeID, failed.Attempt+1, failed.ID, types.TaskInitiatorFromContext(ctx), nil)
	return s.startWorkflowRunFromSeed(ctx, newRun, newBranch, pending, started)
}

// applyWorkflowRequestMetadata 把请求操作者和 request_id 固化到运行与首个待办，
// 这样 debug/retry 在异步 worker 中仍能写出可追溯的审计和结构化日志。
func applyWorkflowRequestMetadata(ctx context.Context, run *types.WorkflowRun, pending *types.TaskPendingOp) {
	if run == nil {
		return
	}
	if userID, ok := types.UserIDFromContext(ctx); ok && !types.IsSyntheticUserID(userID) {
		run.RequestedBy = userID
	}
	if requestID, ok := types.RequestIDFromContext(ctx); ok {
		run.RequestID = requestID
	}
	if pending == nil || len(pending.Payload) == 0 {
		return
	}
	var payload types.WorkflowPendingNodePayload
	if err := json.Unmarshal(pending.Payload, &payload); err != nil {
		return
	}
	payload.Initiator = types.TaskInitiatorFromContext(ctx)
	encoded, err := json.Marshal(payload)
	if err == nil {
		pending.Payload = encoded
	}
}

func (s *agentService) startWorkflowRunFromSeed(
	ctx context.Context,
	run *types.WorkflowRun,
	branch *types.WorkflowRunBranch,
	pending *types.TaskPendingOp,
	started *types.WorkflowRunEvent,
) (*types.WorkflowRun, error) {
	starter, ok := interface{}(s).(interface {
		StartWorkflowRun(context.Context, *types.WorkflowRun, *types.WorkflowRunBranch, *types.TaskPendingOp, *types.WorkflowRunEvent) (*types.WorkflowRun, bool, error)
	})
	if !ok {
		return nil, fmt.Errorf("workflow durable runner is unavailable")
	}
	persisted, _, err := starter.StartWorkflowRun(ctx, run, branch, pending, started)
	return persisted, err
}

func (s *agentService) loadWorkflowAgent(ctx context.Context, tenantID uint64, agentID string) (*types.CustomAgent, error) {
	agent, err := repository.NewCustomAgentRepository(s.db).GetAgentByID(ctx, agentID, tenantID)
	if err != nil {
		return nil, err
	}
	if agent.Config.AgentType != types.AgentTypeWorkflow {
		return nil, fmt.Errorf("agent is not a workflow agent")
	}
	return agent, nil
}

func newWorkflowRunSeed(
	tenantID uint64,
	agentID, runMode, triggerSource string,
	draftRevision, workflowVersion int64,
	definitionJSON, configJSON types.JSON,
	input types.WorkflowDebugInput,
	idempotencyKey string,
) (*types.WorkflowRun, *types.WorkflowRunBranch, *types.TaskPendingOp, *types.WorkflowRunEvent) {
	now := time.Now()
	runID := generateEventID("workflow-run")
	inputMap := map[string]interface{}{"query": input.Query, "attachments_text": input.AttachmentsText}
	run := &types.WorkflowRun{
		ID: runID, TenantID: tenantID, AgentID: agentID, WorkflowVersion: workflowVersion,
		DraftRevision: draftRevision, DefinitionSnapshot: definitionJSON, ConfigSnapshot: configJSON,
		InputPayload: marshalRedactedWorkflowPayload(inputMap), RunMode: runMode,
		RequestedBy: "", IdempotencyKey: idempotencyKey, TriggerSource: triggerSource,
		Status: types.WorkflowRunStatusRunning, RequestID: "", StartedAt: now, CreatedAt: now,
	}
	run.InputSummary, run.InputTruncated = sanitizeWorkflowSummary(input.Query)
	branch := &types.WorkflowRunBranch{
		ID: uuid.NewString(), RunID: runID, TenantID: tenantID, AgentID: agentID,
		CurrentNodeID: workflowStartNodeID(definitionJSON),
		Variables:     marshalWorkflowVariables(map[string]interface{}{"input": inputMap, "nodes": map[string]interface{}{}}),
		Status:        types.WorkflowBranchStatusPending, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	pending := newWorkflowPendingNode(run, branch.ID, branch.CurrentNodeID, 1, 0, types.TaskInitiator{}, nil)
	started := &types.WorkflowRunEvent{EventType: "workflow_run.started", Status: types.WorkflowRunStatusRunning}
	return run, branch, pending, started
}

func workflowStartNodeID(definitionJSON types.JSON) string {
	var definition types.WorkflowDefinition
	if json.Unmarshal(definitionJSON, &definition) != nil {
		return ""
	}
	for _, node := range definition.Nodes {
		if node.Type == types.WorkflowNodeTypeStart {
			return node.ID
		}
	}
	return ""
}
