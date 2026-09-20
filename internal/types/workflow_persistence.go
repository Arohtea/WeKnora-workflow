package types

import (
	"time"
	"unicode/utf8"
)

const (
	WorkflowRunModeProduction = "production"
	WorkflowRunModeDebug      = "debug"

	WorkflowNodeStatusPending   = "pending"
	WorkflowNodeStatusRunning   = "running"
	WorkflowNodeStatusSucceeded = "succeeded"
	WorkflowNodeStatusFailed    = "failed"
	WorkflowNodeStatusCanceled  = "canceled"
	WorkflowNodeStatusSkipped   = "skipped"

	WorkflowBranchStatusPending   = "pending"
	WorkflowBranchStatusRunning   = "running"
	WorkflowBranchStatusSucceeded = "succeeded"
	WorkflowBranchStatusFailed    = "failed"
	WorkflowBranchStatusCanceled  = "canceled"
	WorkflowBranchStatusForked    = "forked"

	WorkflowRunStatusRunning   = "running"
	WorkflowRunStatusSucceeded = "succeeded"
	WorkflowRunStatusPartial   = "partial"
	WorkflowRunStatusFailed    = "failed"
	WorkflowRunStatusCanceled  = "canceled"

	WorkflowTriggerChat  = "chat"
	WorkflowTriggerShare = "share"
	WorkflowTriggerIM    = "im"
	WorkflowTriggerEmbed = "embed"
	WorkflowTriggerDebug = "debug"

	// WorkflowErrorCodeNoMatchingBranch 表示一个节点的出边既没有条件命中，也没有
	// 可兜底的默认边。它不是节点执行失败，而是路由配置无法覆盖当前运行时数据，
	// 因此用独立错误码把它与节点自身失败区分开。
	WorkflowErrorCodeNoMatchingBranch  = "NO_MATCHING_BRANCH"
	WorkflowErrorCodeCanceled          = "CANCELED"
	WorkflowErrorCodeWorkerInterrupted = "WORKER_INTERRUPTED"
	WorkflowErrorCodeUnsafeReplay      = "UNSAFE_REPLAY_BLOCKED"
	WorkflowErrorCodeConfig            = "CONFIG_ERROR"
	WorkflowErrorCodePermission        = "PERMISSION_DENIED"
	WorkflowErrorCodeTimeout           = "TIMEOUT"
	WorkflowErrorCodeRateLimited       = "RATE_LIMITED"
	WorkflowErrorCodeUpstream          = "UPSTREAM_ERROR"
	WorkflowErrorCodeNetwork           = "NETWORK_ERROR"

	// WorkflowLifecycleSummaryLimit 是生命周期事件与运行记录摘要的字节上限。
	// 事件总线是进程内共享通道，节点输出可能包含整段文档或 HTTP 响应体，
	// 不设上限会让单次运行的事件占用内存线性膨胀。
	WorkflowLifecycleSummaryLimit = 8 * 1024
)

// WorkflowDebugInput 是编辑器试跑使用的固定输入空间。
type WorkflowDebugInput struct {
	Query           string `json:"query"`
	AttachmentsText string `json:"attachments_text,omitempty"`
}

// WorkflowImportPreview 是服务端导入预检结果。
type WorkflowImportPreview struct {
	Config           *CustomAgentConfig              `json:"config"`
	Definition       *WorkflowDefinition             `json:"definition"`
	Issues           []WorkflowValidationIssue       `json:"issues"`
	Warnings         []WorkflowValidationIssue       `json:"warnings"`
	MissingResources []string                        `json:"missing_resources"`
	SensitiveFields  []string                        `json:"sensitive_fields"`
	ResourceMappings []WorkflowImportResourceMapping `json:"resource_mappings"`
}

// WorkflowImportResourceMapping 描述导入定义中的资源引用是否能映射到当前租户。
type WorkflowImportResourceMapping struct {
	Kind       string `json:"kind"`
	Reference  string `json:"reference"`
	ResolvedID string `json:"resolved_id,omitempty"`
	Status     string `json:"status"`
}

// WorkflowVersionRecord 是不可变的工作流发布版本快照。
type WorkflowVersionRecord struct {
	TenantID       uint64    `json:"tenant_id" gorm:"primaryKey"`
	AgentID        string    `json:"agent_id" gorm:"type:varchar(36);primaryKey"`
	Version        int64     `json:"version" gorm:"primaryKey"`
	DraftRevision  int64     `json:"draft_revision" gorm:"not null"`
	Definition     JSON      `json:"definition" gorm:"type:jsonb;not null"`
	ConfigSnapshot JSON      `json:"config_snapshot,omitempty" gorm:"type:jsonb;not null"`
	PublishedBy    string    `json:"published_by,omitempty" gorm:"type:varchar(36);not null;default:''"`
	PublishedAt    time.Time `json:"published_at" gorm:"not null"`
}

// TableName 返回发布版本表名。
func (WorkflowVersionRecord) TableName() string { return "workflow_versions" }

// WorkflowRun 保存一次绑定到不可变版本的工作流执行。
type WorkflowRun struct {
	ID                 string            `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID           uint64            `json:"tenant_id" gorm:"not null;index"`
	AgentID            string            `json:"agent_id" gorm:"type:varchar(36);not null;index"`
	WorkflowVersion    int64             `json:"workflow_version" gorm:"not null"`
	DraftRevision      int64             `json:"draft_revision" gorm:"not null;default:0"`
	DefinitionSnapshot JSON              `json:"definition_snapshot" gorm:"type:jsonb;not null"`
	ConfigSnapshot     JSON              `json:"-" gorm:"type:jsonb;not null"`
	InputPayload       JSON              `json:"-" gorm:"type:jsonb"`
	RunMode            string            `json:"run_mode" gorm:"type:varchar(16);not null;default:'production';index"`
	RequestedBy        string            `json:"requested_by,omitempty" gorm:"type:varchar(36);not null;default:'';index"`
	IdempotencyKey     string            `json:"idempotency_key,omitempty" gorm:"type:varchar(128);not null;default:''"`
	RetryOfRunID       string            `json:"retry_of_run_id,omitempty" gorm:"type:varchar(36);not null;default:'';index"`
	TriggerSource      string            `json:"trigger_source" gorm:"type:varchar(24);not null"`
	Status             string            `json:"status" gorm:"type:varchar(16);not null;index"`
	SessionID          string            `json:"session_id,omitempty" gorm:"type:varchar(36);index"`
	MessageID          string            `json:"message_id,omitempty" gorm:"type:varchar(36);index"`
	RequestID          string            `json:"request_id,omitempty" gorm:"type:varchar(64);index"`
	InputSummary       string            `json:"input_summary,omitempty" gorm:"type:text"`
	InputTruncated     bool              `json:"input_truncated" gorm:"not null;default:false"`
	OutputSummary      string            `json:"output_summary,omitempty" gorm:"type:text"`
	OutputTruncated    bool              `json:"output_truncated" gorm:"not null;default:false"`
	ErrorCode          string            `json:"error_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary       string            `json:"error_summary,omitempty" gorm:"type:text"`
	ErrorTruncated     bool              `json:"error_truncated" gorm:"not null;default:false"`
	Usage              JSON              `json:"usage,omitempty" gorm:"type:jsonb"`
	CancelRequestedAt  *time.Time        `json:"cancel_requested_at,omitempty" gorm:"index"`
	LastHeartbeatAt    *time.Time        `json:"last_heartbeat_at,omitempty" gorm:"index"`
	StartedAt          time.Time         `json:"started_at" gorm:"not null;index"`
	FinishedAt         *time.Time        `json:"finished_at,omitempty"`
	DurationMs         int64             `json:"duration_ms" gorm:"not null;default:0"`
	CreatedAt          time.Time         `json:"created_at" gorm:"not null"`
	Nodes              []WorkflowRunNode `json:"nodes,omitempty" gorm:"-"`
}

// TableName 返回运行记录表名。
func (WorkflowRun) TableName() string { return "workflow_runs" }

// WorkflowRunNode 保存一次节点执行及其脱敏摘要。
type WorkflowRunNode struct {
	ID              int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	RunID           string     `json:"run_id" gorm:"type:varchar(36);not null;index"`
	TenantID        uint64     `json:"tenant_id" gorm:"not null;index"`
	AgentID         string     `json:"agent_id" gorm:"type:varchar(36);not null;index"`
	NodeID          string     `json:"node_id" gorm:"type:varchar(80);not null"`
	NodeName        string     `json:"node_name" gorm:"type:varchar(100);not null"`
	NodeType        string     `json:"node_type" gorm:"type:varchar(32);not null"`
	BranchID        string     `json:"branch_id,omitempty" gorm:"type:varchar(36);not null;default:'';index"`
	BranchPath      string     `json:"branch_path" gorm:"type:text;not null"`
	Sequence        int64      `json:"sequence" gorm:"not null"`
	Attempt         int        `json:"attempt" gorm:"not null;default:1"`
	RetryOf         int64      `json:"retry_of,omitempty" gorm:"not null;default:0;index"`
	TaskID          string     `json:"task_id,omitempty" gorm:"type:varchar(128);not null;default:'';index"`
	Retryable       bool       `json:"retryable" gorm:"not null;default:false"`
	Status          string     `json:"status" gorm:"type:varchar(16);not null;index"`
	InputPayload    JSON       `json:"-" gorm:"type:jsonb"`
	InputSummary    string     `json:"input_summary,omitempty" gorm:"type:text"`
	InputTruncated  bool       `json:"input_truncated" gorm:"not null;default:false"`
	OutputPayload   JSON       `json:"-" gorm:"type:jsonb"`
	OutputSummary   string     `json:"output_summary,omitempty" gorm:"type:text"`
	OutputTruncated bool       `json:"output_truncated" gorm:"not null;default:false"`
	ErrorCode       string     `json:"error_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary    string     `json:"error_summary,omitempty" gorm:"type:text"`
	ErrorTruncated  bool       `json:"error_truncated" gorm:"not null;default:false"`
	Usage           JSON       `json:"usage,omitempty" gorm:"type:jsonb"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationMs      int64      `json:"duration_ms" gorm:"not null;default:0"`
	CreatedAt       time.Time  `json:"created_at" gorm:"not null"`
}

// TableName 返回节点运行记录表名。
func (WorkflowRunNode) TableName() string { return "workflow_run_nodes" }

// WorkflowRunBranch 保存一条可恢复执行分支的当前位置与变量检查点。
type WorkflowRunBranch struct {
	ID            string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	RunID         string    `json:"run_id" gorm:"type:varchar(36);not null;index"`
	TenantID      uint64    `json:"tenant_id" gorm:"not null;index"`
	AgentID       string    `json:"agent_id" gorm:"type:varchar(36);not null;index"`
	ParentID      string    `json:"parent_id,omitempty" gorm:"type:varchar(36);not null;default:'';index"`
	BranchPath    string    `json:"branch_path" gorm:"type:text;not null"`
	CurrentNodeID string    `json:"current_node_id" gorm:"type:varchar(80);not null;default:''"`
	Variables     JSON      `json:"-" gorm:"type:jsonb;not null"`
	Status        string    `json:"status" gorm:"type:varchar(16);not null;index"`
	ErrorCode     string    `json:"error_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary  string    `json:"error_summary,omitempty" gorm:"type:text;not null;default:''"`
	Version       int64     `json:"version" gorm:"not null;default:1"`
	LastNodeRunID int64     `json:"last_node_run_id,omitempty" gorm:"not null;default:0"`
	CreatedAt     time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"not null"`
}

// TableName 返回分支检查点表名。
func (WorkflowRunBranch) TableName() string { return "workflow_run_branches" }

// WorkflowRunEvent 持久化可按 sequence 续接的运行生命周期事件。
type WorkflowRunEvent struct {
	ID               int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RunID            string    `json:"run_id" gorm:"type:varchar(36);not null;index"`
	TenantID         uint64    `json:"tenant_id" gorm:"not null;index"`
	AgentID          string    `json:"agent_id" gorm:"type:varchar(36);not null;index"`
	EventID          string    `json:"event_id" gorm:"type:varchar(96);not null"`
	Sequence         int64     `json:"sequence" gorm:"not null"`
	EventType        string    `json:"event_type" gorm:"type:varchar(48);not null"`
	NodeID           string    `json:"node_id,omitempty" gorm:"type:varchar(80);not null;default:''"`
	BranchPath       string    `json:"branch_path,omitempty" gorm:"type:text;not null"`
	Attempt          int       `json:"attempt,omitempty" gorm:"not null;default:0"`
	Status           string    `json:"status" gorm:"type:varchar(16);not null"`
	OccurredAt       time.Time `json:"occurred_at" gorm:"not null;index"`
	DurationMs       int64     `json:"duration_ms,omitempty" gorm:"not null;default:0"`
	Summary          string    `json:"summary,omitempty" gorm:"type:text"`
	SummaryTruncated bool      `json:"summary_truncated,omitempty" gorm:"not null;default:false"`
	ErrorCode        string    `json:"error_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	Error            string    `json:"error,omitempty" gorm:"type:text"`
	Extra            JSON      `json:"extra,omitempty" gorm:"type:jsonb"`
	CreatedAt        time.Time `json:"created_at" gorm:"not null"`
}

// TableName 返回运行事件表名。
func (WorkflowRunEvent) TableName() string { return "workflow_run_events" }

// WorkflowRunQuery 控制游标分页及可选筛选条件。
type WorkflowRunQuery struct {
	BeforeStartedAt *time.Time
	BeforeID        string
	Status          string
	RunMode         string
	StartedAfter    *time.Time
	StartedBefore   *time.Time
	Limit           int
}

// WorkflowNodeCompletion 描述一次单节点执行完成后的事务性检查点。
//
// 仓储层会在同一事务中写入节点终态、完成事件、分支状态、下一批 durable pending
// ops，并删除当前 pending op；因此 worker 在任意提交点崩溃都能从数据库继续。
type WorkflowNodeCompletion struct {
	PendingOpID           int64
	Node                  *WorkflowRunNode
	Branch                *WorkflowRunBranch
	ExpectedBranchVersion int64
	Event                 *WorkflowRunEvent
	RunEvent              *WorkflowRunEvent
	ChildBranches         []*WorkflowRunBranch
	NextPendingOps        []*TaskPendingOp
	RunOutputSummary      string
	RunErrorCode          string
	RunErrorSummary       string
	RunUsage              JSON
}

// WorkflowValidationIssue 标识一个可以定位到编辑器的结构化校验问题。
type WorkflowValidationIssue struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	NodeID    string `json:"node_id,omitempty"`
	EdgeID    string `json:"edge_id,omitempty"`
	FieldPath string `json:"field_path,omitempty"`
}

// WorkflowLifecycleEventData 是工作流专用生命周期事件的统一载荷。
type WorkflowLifecycleEventData struct {
	EventID          string                 `json:"event_id"`
	Sequence         int64                  `json:"sequence"`
	RunID            string                 `json:"run_id"`
	WorkflowVersion  int64                  `json:"workflow_version"`
	NodeID           string                 `json:"node_id,omitempty"`
	BranchPath       string                 `json:"branch_path,omitempty"`
	Attempt          int                    `json:"attempt,omitempty"`
	Status           string                 `json:"status"`
	OccurredAt       time.Time              `json:"occurred_at"`
	DurationMs       int64                  `json:"duration_ms,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
	SummaryTruncated bool                   `json:"summary_truncated,omitempty"`
	ErrorCode        string                 `json:"error_code,omitempty"`
	Error            string                 `json:"error,omitempty"`
	Extra            map[string]interface{} `json:"extra,omitempty"`
}

// WorkflowNodeFailure 是一个节点失败的脱敏快照。
//
// 它同时用于状态归并（判断 succeeded/partial/failed）和 partial 结果的结构化
// 上报，因此必须可 JSON 序列化，且只承载节点 ID/名称/错误原因，不携带任何请求头、
// 令牌或响应体原文。
type WorkflowNodeFailure struct {
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name,omitempty"`
	NodeType   string `json:"node_type,omitempty"`
	BranchPath string `json:"branch_path,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

// WorkflowRunReport 是一次运行的执行报告，供状态归并和最终事件消费。
type WorkflowRunReport struct {
	// SuccessEnd 表示至少有一个 end 节点产出了用户可见答案。
	SuccessEnd bool
	// Answers 是各成功分支的答案片段，按分支路径确定性排序。
	Answers []string
	// Unhandled 是没有任何失败处理分支命中的节点失败。
	Unhandled []WorkflowNodeFailure
	// Handled 是被失败处理分支消费掉的节点失败，仅用于审计与轨迹展示。
	Handled []WorkflowNodeFailure
	// Canceled 表示运行因上下文取消而终止。
	Canceled bool
}

// Status 归并出最终运行状态。
//
// 归并规则（与轨迹抽屉的状态标签一一对应）：
//   - 取消优先：取消后的运行不再具备"成功/失败"语义，只报告 canceled。
//   - 有成功终点且有未处理失败 -> partial：成功分支的答案仍然可用，调用方必须把
//     Unhandled 一并下发，否则用户会以为工作流完整跑通了。
//   - 有成功终点且无未处理失败 -> succeeded。
//   - 没有成功终点 -> failed。
//
// @returns types.WorkflowRunStatus* 之一。
func (report WorkflowRunReport) Status() string {
	return ResolveWorkflowRunStatus(report.SuccessEnd, report.Unhandled, report.Canceled)
}

// ResolveWorkflowRunStatus 是 WorkflowRunReport.Status 的纯函数形式。
//
// 单独暴露是为了让测试可以脱离执行器直接覆盖四种归并结果。
//
// @param successEnd 是否至少有一个 end 节点产出了答案。
// @param unhandled 未处理的节点失败列表。
// @param canceled 运行是否被取消。
// @returns types.WorkflowRunStatus* 之一。
func ResolveWorkflowRunStatus(successEnd bool, unhandled []WorkflowNodeFailure, canceled bool) string {
	switch {
	case canceled:
		return WorkflowRunStatusCanceled
	case successEnd && len(unhandled) > 0:
		return WorkflowRunStatusPartial
	case successEnd:
		return WorkflowRunStatusSucceeded
	default:
		return WorkflowRunStatusFailed
	}
}

// TruncateWorkflowSummary 把摘要裁剪到生命周期上限并报告是否发生截断。
//
// 节点输出可能是整段文档或 HTTP 响应体，而生命周期事件会被广播并落库；统一在
// 这里收口，避免每个调用点各自实现上限并出现不一致。
//
// @param summary 原始摘要文本。
// @returns 截断后的文本以及是否发生了截断。
func TruncateWorkflowSummary(summary string) (string, bool) {
	if len(summary) <= WorkflowLifecycleSummaryLimit {
		return summary, false
	}
	// 按字节裁剪可能切断多字节字符，回退到最近的 UTF-8 边界。
	trimmed := summary[:WorkflowLifecycleSummaryLimit]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed, true
}
