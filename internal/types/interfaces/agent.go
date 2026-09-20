package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

// AgentStreamEvent represents a streaming event from the agent
type AgentStreamEvent struct {
	Type      string                 `json:"type"`      // "thought", "tool_call", "tool_result", "final_answer", "error", "references"
	Content   string                 `json:"content"`   // Incremental content
	Data      map[string]interface{} `json:"data"`      // Additional structured data
	Done      bool                   `json:"done"`      // Whether this is the last event
	Iteration int                    `json:"iteration"` // Current iteration number
}

// AgentEngine defines the interface for agent execution engine
type AgentEngine interface {
	// Execute executes the agent with conversation history and returns a stream of events
	// imageURLs is optional - when provided, images are passed to the LLM as multimodal content
	Execute(
		ctx context.Context,
		sessionID, messageID, query string,
		llmContext []chat.Message,
		imageURLs ...[]string,
	) (*types.AgentState, error)

	// SetMemoryPrompt supplies the long-term memory envelope for this run.
	// It must be called before Execute; an empty string is a no-op.
	SetMemoryPrompt(prompt string)

	// SetSteerSink enables mid-run message injection for this run. Nil
	// (default) disables it; when set, the engine drains user-appended
	// messages at every round boundary and persists accepted ones through
	// the sink. Must be called before Execute.
	SetSteerSink(sink types.SteerSink)
}

// AgentService defines the interface for agent-related operations
type AgentService interface {
	// CreateAgentEngine creates an agent engine with the given configuration and EventBus.
	// Conversation history is loaded by the caller (see service.LoadAgentHistory) and
	// passed into AgentEngine.Execute; the engine itself is stateless across turns.
	CreateAgentEngine(
		ctx context.Context,
		config *types.AgentConfig,
		chatModel chat.Chat,
		rerankModel rerank.Reranker,
		eventBus *event.EventBus,
		sessionID, assistantMessageID string,
	) (AgentEngine, error)

	// ValidateConfig validates an agent configuration
	ValidateConfig(config *types.AgentConfig) error
	// ---- 工作流生产可用（发布 / 版本 / 校验 / 运行审计）----
	// 这些方法复用 *agentService 上的同名实现，接收 error 而非具体领域错误类型，
	// 避免 interfaces 反向依赖 repository 造成 import cycle；调用方（handler）
	// 自行用 errors.Is 判断 repository.ErrWorkflowRevisionConflict 等哨兵值。

	// PublishWorkflow 校验当前草稿并创建不可变发布版本。
	PublishWorkflow(ctx context.Context, agentID string, expectedRevision int64) (*types.WorkflowVersionRecord, error)

	// ListWorkflowVersions 返回发布历史（新 -> 旧）。
	ListWorkflowVersions(ctx context.Context, agentID string) ([]*types.WorkflowVersionRecord, error)

	// GetWorkflowVersion 返回指定不可变版本。
	GetWorkflowVersion(ctx context.Context, agentID string, version int64) (*types.WorkflowVersionRecord, error)

	// RestoreWorkflowVersion 将指定版本复制成新的草稿修订。
	RestoreWorkflowVersion(ctx context.Context, agentID string, version, expectedRevision int64) (*types.CustomAgent, error)

	// GetPublishedWorkflow 返回当前生效的发布快照，未发布时返回错误。
	GetPublishedWorkflow(ctx context.Context, agentID string) (*types.WorkflowVersionRecord, error)

	// ValidateWorkflowDefinition 返回结构化静态校验问题，不落库。
	ValidateWorkflowDefinition(ctx context.Context, config *types.CustomAgentConfig) ([]types.WorkflowValidationIssue, error)

	// ListWorkflowRuns 返回运行记录、是否还有下一页及错误。
	ListWorkflowRuns(ctx context.Context, agentID string, query types.WorkflowRunQuery) ([]*types.WorkflowRun, bool, error)

	// GetWorkflowRun 返回单次运行详情（含当次定义快照与节点记录）。
	GetWorkflowRun(ctx context.Context, agentID, runID string) (*types.WorkflowRun, error)

	// ListWorkflowRunEvents 返回指定 sequence 之后的持久化生命周期事件。
	ListWorkflowRunEvents(ctx context.Context, agentID, runID string, afterSequence int64, limit int) ([]*types.WorkflowRunEvent, error)

	// RequestWorkflowRunCancel 幂等请求取消运行。
	RequestWorkflowRunCancel(ctx context.Context, agentID, runID string) (*types.WorkflowRun, bool, error)
}
