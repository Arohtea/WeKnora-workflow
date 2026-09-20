package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// workflowRunStore 是执行器观测运行记录所需的窄接口。
//
// sessionService 只持有 interfaces.AgentService，运行记录的租户边界与版本绑定都在
// agentService 内部完成；这里用窄接口断言而不是扩大 AgentService 的公共契约，
// 使得旧的 agentService 实现（例如测试替身）仍能跑通工作流，只是不落库。
type workflowRunStore interface {
	CreateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error
	UpdateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error
	CreateWorkflowRunNode(ctx context.Context, node *types.WorkflowRunNode) error
	UpdateWorkflowRunNode(ctx context.Context, node *types.WorkflowRunNode) error
	CreateWorkflowRunEvent(ctx context.Context, runEvent *types.WorkflowRunEvent) error
}

// workflowRunObserver 负责一次运行的轨迹观测：生命周期事件与运行记录。
//
// 设计约束：
//   - 事件是轨迹的权威来源，运行记录只是它的持久化投影；两者共用同一份运行上下文，
//     因此节点事件与节点落库的 sequence 一定一致。
//   - 任何写入失败只记日志。工作流已经上线，观测链路故障不能让业务执行失败。
type workflowRunObserver struct {
	ctx      context.Context
	eventBus *event.EventBus
	store    workflowRunStore

	run             *types.WorkflowRun
	workflowVersion int64
	tenantID        uint64
	agentID         string
	sequence        int64
	mu              sync.Mutex
}

// newWorkflowRunObserver 准备一次运行观测，并尽力写入运行起始记录。
//
// @param ctx 当前请求上下文。
// @param eventBus 当前请求的事件总线。
// @param store 运行记录存储；为 nil 时只发事件不落库。
// @param publishedWorkflow 本次实际执行的不可变发布版本；debug run 可传 nil。
// @param definition 本次实际执行的定义。
// @param config 本次实际执行的运行时配置。
// @param tenantID 运行归属租户。
// @param agentID 工作流智能体 ID。
// @param triggerSource 触发来源，取值 types.WorkflowTrigger*。
// @param sessionID 会话 ID。
// @param messageID 助手消息 ID。
// @param inputSummary 已脱敏的输入摘要。
// @returns 运行观测器；store 为 nil 时返回只发事件的观测器。
func newWorkflowRunObserver(
	ctx context.Context,
	eventBus *event.EventBus,
	store workflowRunStore,
	publishedWorkflow *types.WorkflowVersionRecord,
	definition *types.WorkflowDefinition,
	config *types.AgentConfig,
	tenantID uint64,
	agentID, triggerSource, sessionID, messageID string,
	inputSummary string,
) *workflowRunObserver {
	requestID, _ := types.RequestIDFromContext(ctx)
	observer := &workflowRunObserver{
		ctx:      ctx,
		eventBus: eventBus,
		store:    store,
		tenantID: tenantID,
		agentID:  agentID,
	}

	definedVersion := int64(0)
	if publishedWorkflow != nil {
		definedVersion = publishedWorkflow.Version
	}
	definitionSnapshot, marshalErr := json.Marshal(definition)
	if marshalErr != nil {
		logger.Warnf(ctx, "Failed to snapshot workflow definition for agent %s: %v", agentID, marshalErr)
		definitionSnapshot = []byte("{}")
	}
	observer.workflowVersion = definedVersion

	configSnapshot, configErr := json.Marshal(config)
	if configErr != nil {
		logger.Warnf(ctx, "Failed to snapshot workflow config for agent %s: %v", agentID, configErr)
		configSnapshot = []byte("{}")
	}
	now := time.Now()
	runMode := types.WorkflowRunModeDebug
	draftRevision := int64(0)
	if publishedWorkflow != nil {
		runMode = types.WorkflowRunModeProduction
		draftRevision = publishedWorkflow.DraftRevision
	}
	requestedBy, _ := types.UserIDFromContext(ctx)
	inputPayload, _ := json.Marshal(map[string]interface{}{
		"query": sanitizeWorkflowPayloadText(inputSummary),
	})
	observer.run = &types.WorkflowRun{
		ID:                 generateEventID("workflow-run"),
		TenantID:           tenantID,
		AgentID:            agentID,
		WorkflowVersion:    definedVersion,
		DraftRevision:      draftRevision,
		DefinitionSnapshot: types.JSON(definitionSnapshot),
		ConfigSnapshot:     types.JSON(configSnapshot),
		InputPayload:       types.JSON(inputPayload),
		RunMode:            runMode,
		RequestedBy:        requestedBy,
		TriggerSource:      triggerSource,
		Status:             types.WorkflowRunStatusRunning,
		SessionID:          sessionID,
		MessageID:          messageID,
		RequestID:          requestID,
		LastHeartbeatAt:    &now,
		StartedAt:          now,
		CreatedAt:          now,
	}
	observer.run.InputSummary, observer.run.InputTruncated = sanitizeWorkflowSummary(inputSummary)

	if store != nil {
		if err := store.CreateWorkflowRun(context.WithoutCancel(ctx), observer.run); err != nil {
			logger.Warnf(ctx, "Failed to record workflow run %s for agent %s: %v", observer.run.ID, agentID, err)
		}
	}
	observer.emit(event.EventWorkflowRunStarted, "", "", types.WorkflowRunStatusRunning, "", "", "")
	return observer
}

// nextSequence 生成单次运行内单调递增的事件序号。
//
// 节点分支并发执行，序号必须由观测器统一分配，否则前端无法按序重建时间线。
//
// @returns 大于 0 且本次运行内唯一递增的序号。
func (o *workflowRunObserver) nextSequence() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sequence++
	return o.sequence
}

// emit 发送一条工作流生命周期事件。
//
// @param eventType 事件类型，取值 event.EventWorkflow*。
// @param nodeID 节点 ID；运行级事件传空串。
// @param branchPath 节点所在执行路径；运行级事件传空串。
// @param status 节点或运行状态。
// @param summary 已由调用方脱敏的摘要，此处统一截断。
// @param errorCode 结构化错误码；无错误时传空串。
// @param errorText 错误描述；无错误时传空串。
func (o *workflowRunObserver) emit(
	eventType event.EventType,
	nodeID, branchPath, status, summary, errorCode, errorText string,
) {
	o.emitWithSequenceAttempt(
		o.nextSequence(), eventType, nodeID, branchPath, 0, status, summary, errorCode, errorText, 0,
	)
}

// emitWithSequence 发送一条使用既定序号的工作流生命周期事件。
//
// 节点 started 事件与节点运行记录必须共用 sequence；由调用方先分配序号，
// 再同时写入事件和记录，避免并发分支下出现一条节点占用两个序号。
//
// @param sequence 已由 nextSequence 分配的运行内序号。
// @param eventType 事件类型，取值 event.EventWorkflow*。
// @param nodeID 节点 ID；运行级事件传空串。
// @param branchPath 节点所在执行路径；运行级事件传空串。
// @param status 节点或运行状态。
// @param summary 已由调用方脱敏的摘要，此处统一截断。
// @param errorCode 结构化错误码；无错误时传空串。
// @param errorText 错误描述；无错误时传空串。
func (o *workflowRunObserver) emitWithSequence(
	sequence int64,
	eventType event.EventType,
	nodeID, branchPath, status, summary, errorCode, errorText string,
) {
	o.emitWithSequenceAttempt(
		sequence, eventType, nodeID, branchPath, 0, status, summary, errorCode, errorText, 0,
	)
}

// emitWithSequenceAttempt 持久化并广播一条带尝试次数的生命周期事件。
func (o *workflowRunObserver) emitWithSequenceAttempt(
	sequence int64,
	eventType event.EventType,
	nodeID, branchPath string,
	attempt int,
	status, summary, errorCode, errorText string,
	durationMs int64,
) {
	truncatedSummary, truncated := types.TruncateWorkflowSummary(summary)
	payload := types.WorkflowLifecycleEventData{
		EventID:          generateEventID(string(eventType)),
		Sequence:         sequence,
		RunID:            o.run.ID,
		WorkflowVersion:  o.workflowVersion,
		NodeID:           nodeID,
		BranchPath:       branchPath,
		Attempt:          attempt,
		Status:           status,
		OccurredAt:       time.Now(),
		DurationMs:       durationMs,
		Summary:          truncatedSummary,
		SummaryTruncated: truncated,
		ErrorCode:        errorCode,
		Error:            errorText,
	}
	if o.store != nil {
		extra, _ := json.Marshal(payload.Extra)
		row := &types.WorkflowRunEvent{
			RunID:            o.run.ID,
			TenantID:         o.tenantID,
			AgentID:          o.agentID,
			EventID:          payload.EventID,
			Sequence:         sequence,
			EventType:        string(eventType),
			NodeID:           nodeID,
			BranchPath:       branchPath,
			Attempt:          attempt,
			Status:           status,
			OccurredAt:       payload.OccurredAt,
			DurationMs:       durationMs,
			Summary:          truncatedSummary,
			SummaryTruncated: truncated,
			ErrorCode:        errorCode,
			Error:            errorText,
			Extra:            types.JSON(extra),
			CreatedAt:        payload.OccurredAt,
		}
		if err := o.store.CreateWorkflowRunEvent(context.WithoutCancel(o.ctx), row); err != nil {
			logger.Warnf(o.ctx, "Failed to persist workflow lifecycle event %s: %v", eventType, err)
		}
	}
	if o.eventBus == nil {
		return
	}
	// 事件在取消后仍需送达前端，否则用户看不到"已取消"的终态。
	if err := o.eventBus.Emit(context.WithoutCancel(o.ctx), event.Event{
		ID:        payload.EventID,
		Type:      eventType,
		SessionID: o.run.SessionID,
		RequestID: o.run.RequestID,
		Data:      payload,
	}); err != nil {
		logger.Warnf(o.ctx, "Failed to emit workflow lifecycle event %s: %v", eventType, err)
	}
}

// nodeStarted 记录节点进入运行态，并返回可供结束时更新的节点记录。
//
// @param node 节点定义。
// @param branchPath 节点所在执行路径。
// @param inputSummary 节点输入摘要，调用方需自行脱敏。
// @returns 已带 sequence 与起始时间的节点运行记录。
func (o *workflowRunObserver) nodeStarted(
	node types.WorkflowNode, branchPath, inputSummary string,
) *types.WorkflowRunNode {
	startedAt := time.Now()
	record := &types.WorkflowRunNode{
		RunID:      o.run.ID,
		TenantID:   o.tenantID,
		AgentID:    o.agentID,
		NodeID:     node.ID,
		NodeName:   node.Name,
		NodeType:   node.Type,
		BranchID:   uuid.NewSHA1(uuid.NameSpaceOID, []byte(o.run.ID+"\x00"+branchPath)).String(),
		BranchPath: branchPath,
		Attempt:    1,
		Status:     types.WorkflowNodeStatusRunning,
		StartedAt:  &startedAt,
		CreatedAt:  startedAt,
	}
	record.Sequence = o.nextSequence()
	record.InputSummary, record.InputTruncated = sanitizeWorkflowSummary(inputSummary)
	if encoded, err := json.Marshal(map[string]interface{}{
		"summary": sanitizeWorkflowPayloadText(inputSummary),
	}); err == nil {
		record.InputPayload = types.JSON(encoded)
	}
	o.emitWithSequenceAttempt(
		record.Sequence,
		event.EventWorkflowNodeStarted,
		node.ID,
		branchPath,
		record.Attempt,
		types.WorkflowNodeStatusRunning,
		inputSummary,
		"",
		"",
		0,
	)
	if o.store == nil {
		return record
	}
	if err := o.store.CreateWorkflowRunNode(context.WithoutCancel(o.ctx), record); err != nil {
		logger.Warnf(o.ctx, "Failed to record workflow node %s of run %s: %v", node.ID, o.run.ID, err)
	}
	return record
}

// nodeFinished 记录节点终态。
//
// @param record nodeStarted 返回的节点记录。
// @param status types.WorkflowNodeStatus* 之一。
// @param outputSummary 节点输出摘要，调用方需自行脱敏。
// @param usage 该节点消耗的 token；无统计时传零值。
// @param errorCode 结构化错误码；无错误时传空串。
// @param errorText 错误描述；无错误时传空串。
func (o *workflowRunObserver) nodeFinished(
	record *types.WorkflowRunNode,
	status, outputSummary string,
	usage types.TokenUsage,
	errorCode, errorText string,
) {
	if record == nil {
		return
	}
	finishedAt := time.Now()
	record.Status = status
	record.FinishedAt = &finishedAt
	record.DurationMs = finishedAt.Sub(*record.StartedAt).Milliseconds()
	record.OutputSummary, record.OutputTruncated = sanitizeWorkflowSummary(outputSummary)
	if encoded, err := json.Marshal(map[string]interface{}{
		"text": sanitizeWorkflowPayloadText(outputSummary),
	}); err == nil {
		record.OutputPayload = types.JSON(encoded)
	}
	record.ErrorCode = errorCode
	record.ErrorSummary, record.ErrorTruncated = sanitizeWorkflowSummary(errorText)
	if encoded, err := json.Marshal(usage); err == nil {
		record.Usage = types.JSON(encoded)
	}

	eventType := event.EventWorkflowNodeCompleted
	switch status {
	case types.WorkflowNodeStatusFailed:
		eventType = event.EventWorkflowNodeFailed
	case types.WorkflowNodeStatusCanceled:
		eventType = event.EventWorkflowNodeCanceled
	}
	o.emitWithSequenceAttempt(
		o.nextSequence(), eventType, record.NodeID, record.BranchPath, record.Attempt,
		status, outputSummary, errorCode, errorText, record.DurationMs,
	)

	if o.store == nil || record.ID == 0 {
		return
	}
	if err := o.store.UpdateWorkflowRunNode(context.WithoutCancel(o.ctx), record); err != nil {
		logger.Warnf(o.ctx, "Failed to update workflow node %s of run %s: %v", record.NodeID, o.run.ID, err)
	}
}

// finish 写入运行终态并发送运行完成事件。
//
// @param status types.WorkflowRunStatus* 之一。
// @param answer 已聚合的最终答案；取消或失败时传空串。
// @param usage 整次运行的 token 合计。
// @param failures 未处理的节点失败，用于 error_summary 与事件载荷。
func (o *workflowRunObserver) finish(
	status, answer string, usage types.TokenUsage, failures []types.WorkflowNodeFailure,
) {
	if o.run == nil {
		return
	}
	finishedAt := time.Now()
	o.run.Status = status
	o.run.FinishedAt = &finishedAt
	o.run.DurationMs = finishedAt.Sub(o.run.StartedAt).Milliseconds()
	o.run.OutputSummary, o.run.OutputTruncated = sanitizeWorkflowSummary(answer)
	o.run.ErrorSummary, o.run.ErrorTruncated = sanitizeWorkflowSummary(workflowFailureMessage(failures))
	if len(failures) > 0 {
		o.run.ErrorCode = failures[0].ErrorCode
	}
	if encoded, err := json.Marshal(usage); err == nil {
		o.run.Usage = types.JSON(encoded)
	}
	o.emit(event.EventWorkflowRunCompleted, "", "", status, o.run.OutputSummary, o.run.ErrorCode, o.run.ErrorSummary)
	if o.store == nil {
		return
	}
	if err := o.store.UpdateWorkflowRun(context.WithoutCancel(o.ctx), o.run); err != nil {
		logger.Warnf(o.ctx, "Failed to update workflow run %s status to %s: %v", o.run.ID, status, err)
	}
}

// runID 返回本次运行的记录 ID，供事件与日志关联。
//
// @returns 运行记录 ID。
func (o *workflowRunObserver) runID() string {
	if o.run == nil {
		return ""
	}
	return o.run.ID
}

// workflowFailureMessage 把未处理失败渲染成一行可供 error_summary 使用的文本。
//
// @param failures 未处理的节点失败列表。
// @returns "" 表示没有失败；否则为按分支路径排序的失败摘要。
func workflowFailureMessage(failures []types.WorkflowNodeFailure) string {
	if len(failures) == 0 {
		return ""
	}
	lines := make([]string, 0, len(failures))
	for _, failure := range failures {
		name := failure.NodeName
		if name == "" {
			name = failure.NodeID
		}
		line := name + "：" + failure.Error
		if failure.ErrorCode != "" {
			line = fmt.Sprintf("[%s] %s", failure.ErrorCode, line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// workflowFailureDigest 让失败列表可比较，用于去重。
//
// @param failure 失败快照。
// @returns 唯一标识该失败的稳定字符串。
func workflowFailureDigest(failure types.WorkflowNodeFailure) string {
	return strings.Join([]string{
		failure.BranchPath, failure.NodeID, failure.ErrorCode, failure.Error,
	}, "\x00")
}

// dedupeWorkflowFailures 按分支路径 + 节点 + 错误去重并保持确定顺序。
//
// @param failures 可能重复的失败列表。
// @returns 去重排序后的失败列表。
func dedupeWorkflowFailures(failures []types.WorkflowNodeFailure) []types.WorkflowNodeFailure {
	seen := make(map[string]struct{}, len(failures))
	out := make([]types.WorkflowNodeFailure, 0, len(failures))
	for _, failure := range failures {
		if strings.TrimSpace(failure.Error) == "" {
			continue
		}
		digest := workflowFailureDigest(failure)
		if _, ok := seen[digest]; ok {
			continue
		}
		seen[digest] = struct{}{}
		out = append(out, failure)
	}
	// 并行分支的完成顺序不稳定，按分支路径排序才能让失败列表与事件一致。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].BranchPath == out[j].BranchPath {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].BranchPath < out[j].BranchPath
	})
	return out
}

// sanitizeWorkflowSummary 在摘要进入事件或数据库之前统一脱敏并截断。
//
// @param summary 原始摘要。
// @returns 脱敏且不超过生命周期上限的文本，以及是否发生了截断。
func sanitizeWorkflowSummary(summary string) (string, bool) {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return "", false
	}
	sanitized := sanitizeWorkflowSummarySecrets(trimmed)
	return types.TruncateWorkflowSummary(sanitized)
}

// sanitizeWorkflowPayloadText 对授权后可见的完整文本载荷做脱敏但不截断。
func sanitizeWorkflowPayloadText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	return sanitizeWorkflowSummarySecrets(trimmed)
}

// sanitizeWorkflowSummarySecrets 抹掉摘要里的凭据形态内容。
//
// 摘要会被写入运行记录并广播给前端；HTTP 节点的请求头与 MCP 配置里可能夹带
// Authorization、api_key 等字段，一旦落库就变成了可被翻页浏览的凭据泄漏。
//
// @param summary 原始摘要。
// @returns 已替换敏感字段值的摘要。
func sanitizeWorkflowSummarySecrets(summary string) string {
	masked := summary
	for _, key := range workflowSensitiveSummaryKeys {
		masked = maskWorkflowSummaryValue(masked, key)
	}
	return masked
}

// workflowSensitiveSummaryKeys 是需要在摘要中抹值的字段名（小写匹配）。
var workflowSensitiveSummaryKeys = []string{
	"authorization", "api_key", "apikey", "api-key", "token", "access_token",
	"secret", "password", "cookie", "set-cookie", "x-api-key", "bearer",
}

// maskWorkflowSummaryValue 把 "key": "value" 或 key: value 形态的值替换为掩码。
//
// @param summary 待处理文本。
// @param key 敏感字段名（小写）。
// @returns 替换后的文本。
func maskWorkflowSummaryValue(summary, key string) string {
	lowered := strings.ToLower(summary)
	masked := summary
	searchFrom := 0
	for {
		index := strings.Index(lowered[searchFrom:], key)
		if index < 0 {
			return masked
		}
		index += searchFrom
		valueStart, valueEnd, ok := workflowSummaryValueBounds(masked, index+len(key))
		if !ok {
			searchFrom = index + len(key)
			continue
		}
		replaced := masked[:valueStart] + "***" + masked[valueEnd:]
		lowered = strings.ToLower(replaced)
		masked = replaced
		// 掩码长度与原文不同，按替换后的位置继续扫描剩余内容。
		searchFrom = valueStart + len("***")
	}
}

// workflowSummaryValueBounds 定位字段名之后的值区间。
//
// @param text 待扫描文本。
// @param offset 字段名结尾在 text 中的下标。
// @returns 值区间的起止下标以及是否找到。
func workflowSummaryValueBounds(text string, offset int) (int, int, bool) {
	index := offset
	for index < len(text) && (text[index] == ' ' || text[index] == '"' || text[index] == ':') {
		index++
	}
	if index >= len(text) {
		return 0, 0, false
	}
	if text[index] == '"' || text[index] == '\'' {
		quote := text[index]
		index++
		end := strings.IndexByte(text[index:], quote)
		if end < 0 {
			return 0, 0, false
		}
		return index, index + end, true
	}
	end := index
	for end < len(text) && !strings.ContainsRune(" \n\r\t,;\"'}", rune(text[end])) {
		end++
	}
	if end == index {
		return 0, 0, false
	}
	return index, end, true
}
