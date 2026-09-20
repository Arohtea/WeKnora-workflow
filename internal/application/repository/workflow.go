package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrWorkflowRevisionConflict 表示客户端基于过期草稿修订保存。
	ErrWorkflowRevisionConflict = errors.New("workflow draft revision conflict")
	// ErrWorkflowVersionNotFound 表示请求的发布版本不存在或不属于当前租户。
	ErrWorkflowVersionNotFound = errors.New("workflow version not found")
	// ErrWorkflowRunNotFound 表示请求的运行记录不存在或不属于当前租户。
	ErrWorkflowRunNotFound = errors.New("workflow run not found")
	// ErrWorkflowNodeAlreadyCompleted 表示重复投递命中了已完成的节点 attempt。
	ErrWorkflowNodeAlreadyCompleted = errors.New("workflow node attempt already completed")
	// ErrWorkflowRunCancelRequested 表示运行已收到取消请求，worker 不应再启动新节点。
	ErrWorkflowRunCancelRequested = errors.New("workflow run cancel requested")
)

// WorkflowRepository 是工作流版本和运行记录的持久化边界。
// 所有方法都要求调用方提供租户和智能体范围，避免跨租户查询。
type WorkflowRepository interface {
	SaveDraft(ctx context.Context, tenantID uint64, agentID string, expectedRevision int64, agent *types.CustomAgent) error
	Publish(ctx context.Context, tenantID uint64, agentID, publishedBy string, expectedRevision int64, definition *types.WorkflowDefinition, config types.CustomAgentConfig) (*types.WorkflowVersionRecord, error)
	GetCurrentVersion(ctx context.Context, tenantID uint64, agentID string) (*types.WorkflowVersionRecord, error)
	ListVersions(ctx context.Context, tenantID uint64, agentID string) ([]*types.WorkflowVersionRecord, error)
	GetVersion(ctx context.Context, tenantID uint64, agentID string, version int64) (*types.WorkflowVersionRecord, error)
	CreateRun(ctx context.Context, run *types.WorkflowRun) error
	CreateRunIdempotent(ctx context.Context, run *types.WorkflowRun) (*types.WorkflowRun, bool, error)
	CreateRunWithInitialNode(ctx context.Context, run *types.WorkflowRun, branch *types.WorkflowRunBranch, pendingOp *types.TaskPendingOp, startedEvent *types.WorkflowRunEvent) (*types.WorkflowRun, bool, error)
	UpdateRun(ctx context.Context, run *types.WorkflowRun) error
	RequestRunCancel(ctx context.Context, tenantID uint64, agentID, runID string, requestedAt time.Time) (*types.WorkflowRun, bool, error)
	HeartbeatRun(ctx context.Context, tenantID uint64, agentID, runID string, heartbeatAt time.Time) error
	CreateRunNode(ctx context.Context, node *types.WorkflowRunNode) error
	UpdateRunNode(ctx context.Context, node *types.WorkflowRunNode) error
	StartWorkflowNode(ctx context.Context, pendingOpID int64, node *types.WorkflowRunNode, startedEvent *types.WorkflowRunEvent) error
	CompleteWorkflowNode(ctx context.Context, completion *types.WorkflowNodeCompletion) (*types.WorkflowRun, bool, error)
	CreateRunBranch(ctx context.Context, branch *types.WorkflowRunBranch) error
	GetRunBranch(ctx context.Context, tenantID uint64, runID, branchID string) (*types.WorkflowRunBranch, error)
	UpdateRunBranch(ctx context.Context, branch *types.WorkflowRunBranch, expectedVersion int64) error
	CreateRunEvent(ctx context.Context, event *types.WorkflowRunEvent) error
	ListRunEventsAfter(ctx context.Context, tenantID uint64, agentID, runID string, afterSequence int64, limit int) ([]*types.WorkflowRunEvent, error)
	ListRuns(ctx context.Context, tenantID uint64, agentID string, query types.WorkflowRunQuery) ([]*types.WorkflowRun, bool, error)
	GetRun(ctx context.Context, tenantID uint64, agentID, runID string) (*types.WorkflowRun, error)
	DeleteRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteRunsOlderThanMode(ctx context.Context, cutoff time.Time, runMode string) (int64, error)
}

type workflowRepository struct {
	db *gorm.DB
}

// NewWorkflowRepository 创建基于 GORM 的工作流持久化仓储。
//
// @param db 应用共享的 GORM 数据库连接。
// @returns 工作流仓储实例。
func NewWorkflowRepository(db *gorm.DB) WorkflowRepository {
	return &workflowRepository{db: db}
}

// SaveDraft 以期望修订号原子保存工作流草稿。
func (r *workflowRepository) SaveDraft(
	ctx context.Context,
	tenantID uint64,
	agentID string,
	expectedRevision int64,
	agent *types.CustomAgent,
) error {
	if agent == nil {
		return fmt.Errorf("workflow draft agent is required")
	}
	result := r.db.WithContext(ctx).
		Model(&types.CustomAgent{}).
		Where("tenant_id = ? AND id = ? AND draft_revision = ?", tenantID, agentID, expectedRevision).
		Updates(map[string]interface{}{
			"name":           agent.Name,
			"description":    agent.Description,
			"avatar":         agent.Avatar,
			"config":         agent.Config,
			"draft_revision": gorm.Expr("draft_revision + 1"),
			"updated_at":     time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		agent.DraftRevision = expectedRevision + 1
		return nil
	}
	return ErrWorkflowRevisionConflict
}

// Publish 在一个事务中创建不可变版本并更新智能体当前发布版本。
func (r *workflowRepository) Publish(
	ctx context.Context,
	tenantID uint64,
	agentID, publishedBy string,
	expectedRevision int64,
	definition *types.WorkflowDefinition,
	config types.CustomAgentConfig,
) (*types.WorkflowVersionRecord, error) {
	if definition == nil {
		return nil, fmt.Errorf("workflow definition is required")
	}
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow definition: %w", err)
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow config: %w", err)
	}

	var published *types.WorkflowVersionRecord
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var agent types.CustomAgent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, agentID).First(&agent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowVersionNotFound
			}
			return err
		}
		if agent.DraftRevision != expectedRevision {
			return ErrWorkflowRevisionConflict
		}
		nextVersion := agent.PublishedVersion + 1
		if nextVersion <= 0 {
			nextVersion = 1
		}
		row := &types.WorkflowVersionRecord{
			TenantID:       tenantID,
			AgentID:        agentID,
			Version:        nextVersion,
			DraftRevision:  expectedRevision,
			Definition:     types.JSON(definitionJSON),
			ConfigSnapshot: types.JSON(configJSON),
			PublishedBy:    publishedBy,
			PublishedAt:    time.Now(),
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if err := tx.Model(&types.CustomAgent{}).
			Where("tenant_id = ? AND id = ? AND draft_revision = ?", tenantID, agentID, expectedRevision).
			Updates(map[string]interface{}{"published_version": nextVersion, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		published = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return published, nil
}

// GetCurrentVersion 返回智能体当前发布的不可变版本。
func (r *workflowRepository) GetCurrentVersion(ctx context.Context, tenantID uint64, agentID string) (*types.WorkflowVersionRecord, error) {
	var agent types.CustomAgent
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, agentID).First(&agent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowVersionNotFound
		}
		return nil, err
	}
	if agent.PublishedVersion <= 0 {
		return nil, ErrWorkflowVersionNotFound
	}
	return r.GetVersion(ctx, tenantID, agentID, agent.PublishedVersion)
}

// ListVersions 返回智能体的发布历史，按版本号从新到旧排列。
func (r *workflowRepository) ListVersions(ctx context.Context, tenantID uint64, agentID string) ([]*types.WorkflowVersionRecord, error) {
	var rows []*types.WorkflowVersionRecord
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND agent_id = ?", tenantID, agentID).
		Order("version DESC").Find(&rows).Error
	return rows, err
}

// GetVersion 返回指定的不可变发布版本。
func (r *workflowRepository) GetVersion(ctx context.Context, tenantID uint64, agentID string, version int64) (*types.WorkflowVersionRecord, error) {
	var row types.WorkflowVersionRecord
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND agent_id = ? AND version = ?", tenantID, agentID, version).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWorkflowVersionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateRun 写入一条运行记录。
func (r *workflowRepository) CreateRun(ctx context.Context, run *types.WorkflowRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

// CreateRunIdempotent 创建运行；幂等键已存在时返回原运行而不重复执行。
//
// @param ctx 当前请求上下文。
// @param run 待创建运行；空幂等键退化为普通创建。
// @returns 运行记录、是否新建以及错误。
func (r *workflowRepository) CreateRunIdempotent(
	ctx context.Context,
	run *types.WorkflowRun,
) (*types.WorkflowRun, bool, error) {
	if run == nil {
		return nil, false, fmt.Errorf("workflow run is required")
	}
	return createWorkflowRunIdempotent(r.db.WithContext(ctx), run)
}

func createWorkflowRunIdempotent(tx *gorm.DB, run *types.WorkflowRun) (*types.WorkflowRun, bool, error) {
	if run.IdempotencyKey == "" {
		if err := tx.Create(run).Error; err != nil {
			return nil, false, err
		}
		return run, true, nil
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(run)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return run, true, nil
	}
	var existing types.WorkflowRun
	err := tx.
		Where(
			"tenant_id = ? AND agent_id = ? AND run_mode = ? AND idempotency_key = ?",
			run.TenantID, run.AgentID, run.RunMode, run.IdempotencyKey,
		).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, fmt.Errorf("workflow run id conflict without matching idempotency key")
	}
	if err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

// CreateRunWithInitialNode 原子创建运行、根分支、首节点待办和 started 事件。
//
// @param ctx 当前请求上下文。
// @param run 绑定不可变快照的运行记录。
// @param branch 根执行分支。
// @param pendingOp 首节点 durable 待办。
// @param startedEvent 运行 started 事件。
// @returns 持久化运行、是否新建以及错误；幂等键命中时不重复创建分支和待办。
func (r *workflowRepository) CreateRunWithInitialNode(
	ctx context.Context,
	run *types.WorkflowRun,
	branch *types.WorkflowRunBranch,
	pendingOp *types.TaskPendingOp,
	startedEvent *types.WorkflowRunEvent,
) (*types.WorkflowRun, bool, error) {
	if run == nil || branch == nil || pendingOp == nil || startedEvent == nil {
		return nil, false, fmt.Errorf("workflow run, root branch, pending node and started event are required")
	}
	if run.ID == "" || run.TenantID == 0 || run.AgentID == "" {
		return nil, false, fmt.Errorf("workflow run identity is required")
	}
	if branch.ID == "" || branch.RunID != run.ID || branch.TenantID != run.TenantID || branch.AgentID != run.AgentID {
		return nil, false, fmt.Errorf("workflow root branch does not belong to run")
	}
	if err := preparePendingOp(pendingOp); err != nil {
		return nil, false, err
	}
	if err := validateWorkflowPendingOp(pendingOp, run, branch); err != nil {
		return nil, false, err
	}

	var persisted *types.WorkflowRun
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, wasCreated, err := createWorkflowRunIdempotent(tx, run)
		if err != nil {
			return err
		}
		persisted, created = row, wasCreated
		if !created {
			return nil
		}

		now := time.Now()
		if branch.Version <= 0 {
			branch.Version = 1
		}
		if branch.Status == "" {
			branch.Status = types.WorkflowBranchStatusPending
		}
		if branch.CreatedAt.IsZero() {
			branch.CreatedAt = now
		}
		branch.UpdatedAt = now
		if err := tx.Create(branch).Error; err != nil {
			return err
		}
		if err := tx.Create(pendingOp).Error; err != nil {
			return err
		}

		sequence, err := nextWorkflowSequence(tx, run.ID)
		if err != nil {
			return err
		}
		applyWorkflowEventDefaults(startedEvent, run, sequence)
		return tx.Create(startedEvent).Error
	})
	return persisted, created, err
}

// StartWorkflowNode 把一条已认领待办切换为运行中的节点 attempt。
//
// 同一 attempt 在 worker 崩溃后可重新进入 running；已落终态的 attempt 会清掉
// 遗留待办并返回 ErrWorkflowNodeAlreadyCompleted，调用方应按幂等成功处理。
func (r *workflowRepository) StartWorkflowNode(
	ctx context.Context,
	pendingOpID int64,
	node *types.WorkflowRunNode,
	startedEvent *types.WorkflowRunEvent,
) error {
	if pendingOpID <= 0 || node == nil {
		return fmt.Errorf("workflow pending op and node are required")
	}
	if node.RunID == "" || node.TenantID == 0 || node.AgentID == "" || node.BranchID == "" || node.NodeID == "" || node.Attempt <= 0 {
		return fmt.Errorf("workflow node identity is incomplete")
	}

	alreadyCompleted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pending types.TaskPendingOp
		pendingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND task_type = ? AND scope = ? AND scope_id = ?",
				pendingOpID, node.TenantID, types.TypeWorkflowNodeExecute, types.TaskScopeWorkflowRun, node.RunID).
			First(&pending).Error
		if errors.Is(pendingErr, gorm.ErrRecordNotFound) {
			completed, err := findWorkflowNodeAttempt(tx, node, false)
			if err == nil && isWorkflowNodeTerminal(completed.Status) {
				*node = *completed
				alreadyCompleted = true
				return nil
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return ErrWorkflowRunNotFound
		}
		if pendingErr != nil {
			return pendingErr
		}
		if err := validateWorkflowPendingNodePayload(&pending, node); err != nil {
			return err
		}

		var run types.WorkflowRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND agent_id = ?", node.RunID, node.TenantID, node.AgentID).
			First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowRunNotFound
			}
			return err
		}
		if isWorkflowRunTerminal(run.Status) {
			if err := tx.Delete(&pending).Error; err != nil {
				return err
			}
			alreadyCompleted = true
			return nil
		}
		if run.CancelRequestedAt != nil {
			return ErrWorkflowRunCancelRequested
		}

		var branch types.WorkflowRunBranch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND run_id = ? AND tenant_id = ? AND agent_id = ?",
				node.BranchID, node.RunID, node.TenantID, node.AgentID).
			First(&branch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowRunNotFound
			}
			return err
		}
		if branch.CurrentNodeID != "" && branch.CurrentNodeID != node.NodeID {
			return fmt.Errorf("workflow branch current node mismatch: got %s want %s", node.NodeID, branch.CurrentNodeID)
		}

		existing, err := findWorkflowNodeAttempt(tx, node, true)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		existingFound := err == nil
		if err == nil && isWorkflowNodeTerminal(existing.Status) {
			if err := tx.Delete(&pending).Error; err != nil {
				return err
			}
			*node = *existing
			alreadyCompleted = true
			return nil
		}

		sequence, err := nextWorkflowSequence(tx, node.RunID)
		if err != nil {
			return err
		}
		now := time.Now()
		node.Sequence = sequence
		node.Status = types.WorkflowNodeStatusRunning
		node.StartedAt = &now
		node.FinishedAt = nil
		node.DurationMs = 0
		if existingFound {
			node.ID = existing.ID
			node.CreatedAt = existing.CreatedAt
			if err := tx.Model(&types.WorkflowRunNode{}).
				Where("id = ? AND run_id = ? AND tenant_id = ?", existing.ID, node.RunID, node.TenantID).
				Updates(workflowNodeStartUpdates(node)).Error; err != nil {
				return err
			}
		} else {
			if node.CreatedAt.IsZero() {
				node.CreatedAt = now
			}
			if err := tx.Create(node).Error; err != nil {
				return err
			}
		}

		if err := tx.Model(&types.WorkflowRunBranch{}).
			Where("id = ? AND run_id = ? AND tenant_id = ?", branch.ID, branch.RunID, branch.TenantID).
			Updates(map[string]interface{}{
				"current_node_id": node.NodeID,
				"status":          types.WorkflowBranchStatusRunning,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if startedEvent != nil {
			applyWorkflowEventDefaults(startedEvent, &run, sequence)
			startedEvent.NodeID = node.NodeID
			startedEvent.BranchPath = node.BranchPath
			startedEvent.Attempt = node.Attempt
			if err := tx.Create(startedEvent).Error; err != nil {
				return err
			}
		}
		return tx.Model(&types.WorkflowRun{}).
			Where("id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
			Update("last_heartbeat_at", now).Error
	})
	if err != nil {
		return err
	}
	if alreadyCompleted {
		return ErrWorkflowNodeAlreadyCompleted
	}
	return nil
}

// CompleteWorkflowNode 原子提交单节点终态、分支检查点和后续 durable 待办。
//
// @returns 最新运行、是否已归并为终态以及错误。
func (r *workflowRepository) CompleteWorkflowNode(
	ctx context.Context,
	completion *types.WorkflowNodeCompletion,
) (*types.WorkflowRun, bool, error) {
	if completion == nil || completion.Node == nil || completion.Branch == nil || completion.PendingOpID <= 0 {
		return nil, false, fmt.Errorf("workflow node completion is incomplete")
	}
	node := completion.Node
	branch := completion.Branch
	if node.RunID == "" || node.TenantID == 0 || node.AgentID == "" || node.BranchID == "" || node.NodeID == "" {
		return nil, false, fmt.Errorf("workflow node identity is incomplete")
	}
	if branch.ID != node.BranchID || branch.RunID != node.RunID || branch.TenantID != node.TenantID || branch.AgentID != node.AgentID {
		return nil, false, fmt.Errorf("workflow branch does not belong to node")
	}
	if !isWorkflowNodeTerminal(node.Status) {
		return nil, false, fmt.Errorf("workflow node completion requires a terminal status")
	}

	var persistedRun *types.WorkflowRun
	finalized := false
	alreadyCompleted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run types.WorkflowRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND agent_id = ?", node.RunID, node.TenantID, node.AgentID).
			First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowRunNotFound
			}
			return err
		}
		persistedRun = &run

		storedNode, err := findWorkflowNodeAttempt(tx, node, true)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowRunNotFound
			}
			return err
		}

		var pending types.TaskPendingOp
		pendingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND task_type = ? AND scope = ? AND scope_id = ?",
				completion.PendingOpID, node.TenantID, types.TypeWorkflowNodeExecute, types.TaskScopeWorkflowRun, node.RunID).
			First(&pending).Error
		if pendingErr != nil && !errors.Is(pendingErr, gorm.ErrRecordNotFound) {
			return pendingErr
		}
		if isWorkflowNodeTerminal(storedNode.Status) {
			if pendingErr == nil {
				if err := tx.Delete(&pending).Error; err != nil {
					return err
				}
			}
			*node = *storedNode
			alreadyCompleted = true
			return nil
		}
		if errors.Is(pendingErr, gorm.ErrRecordNotFound) {
			return ErrWorkflowRunNotFound
		}
		if err := validateWorkflowPendingNodePayload(&pending, node); err != nil {
			return err
		}

		var storedBranch types.WorkflowRunBranch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND run_id = ? AND tenant_id = ? AND agent_id = ?",
				branch.ID, branch.RunID, branch.TenantID, branch.AgentID).
			First(&storedBranch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowRunNotFound
			}
			return err
		}

		now := time.Now()
		if node.StartedAt == nil {
			node.StartedAt = storedNode.StartedAt
		}
		if node.FinishedAt == nil {
			node.FinishedAt = &now
		}
		if node.StartedAt != nil && node.DurationMs <= 0 {
			node.DurationMs = node.FinishedAt.Sub(*node.StartedAt).Milliseconds()
		}
		node.ID = storedNode.ID
		node.Sequence = storedNode.Sequence
		node.CreatedAt = storedNode.CreatedAt
		if err := tx.Model(&types.WorkflowRunNode{}).
			Where("id = ? AND run_id = ? AND tenant_id = ?", node.ID, node.RunID, node.TenantID).
			Updates(workflowNodeCompletionUpdates(node)).Error; err != nil {
			return err
		}

		if run.CancelRequestedAt != nil {
			branch.Status = types.WorkflowBranchStatusCanceled
			branch.CurrentNodeID = ""
			branch.ErrorCode = types.WorkflowErrorCodeCanceled
			branch.ErrorSummary = "运行已取消"
			completion.ChildBranches = nil
			completion.NextPendingOps = nil
		}
		branch.LastNodeRunID = node.ID
		if err := updateWorkflowBranchTx(tx, branch, completion.ExpectedBranchVersion); err != nil {
			return err
		}

		if completion.Event != nil {
			sequence, err := nextWorkflowSequence(tx, run.ID)
			if err != nil {
				return err
			}
			applyWorkflowEventDefaults(completion.Event, &run, sequence)
			completion.Event.NodeID = node.NodeID
			completion.Event.BranchPath = node.BranchPath
			completion.Event.Attempt = node.Attempt
			if err := tx.Create(completion.Event).Error; err != nil {
				return err
			}
		}

		if run.CancelRequestedAt != nil {
			if err := tx.Model(&types.WorkflowRunBranch{}).
				Where("run_id = ? AND tenant_id = ? AND status IN ?", run.ID, run.TenantID,
					[]string{types.WorkflowBranchStatusPending, types.WorkflowBranchStatusRunning}).
				Updates(map[string]interface{}{
					"status":          types.WorkflowBranchStatusCanceled,
					"current_node_id": "",
					"error_code":      types.WorkflowErrorCodeCanceled,
					"error_summary":   "运行已取消",
					"updated_at":      now,
				}).Error; err != nil {
				return err
			}
			if err := tx.Where("scope = ? AND scope_id = ?", types.TaskScopeWorkflowRun, run.ID).
				Delete(&types.TaskPendingOp{}).Error; err != nil {
				return err
			}
		} else {
			for _, child := range completion.ChildBranches {
				if child == nil {
					continue
				}
				if child.RunID != run.ID || child.TenantID != run.TenantID || child.AgentID != run.AgentID || child.ParentID != branch.ID {
					return fmt.Errorf("workflow child branch does not belong to completion")
				}
				if child.Version <= 0 {
					child.Version = 1
				}
				if child.Status == "" {
					child.Status = types.WorkflowBranchStatusPending
				}
				if child.CreatedAt.IsZero() {
					child.CreatedAt = now
				}
				child.UpdatedAt = now
				if err := tx.Create(child).Error; err != nil {
					return err
				}
			}
			for _, next := range completion.NextPendingOps {
				if err := preparePendingOp(next); err != nil {
					return err
				}
				if next.TenantID != run.TenantID || next.TaskType != types.TypeWorkflowNodeExecute ||
					next.Scope != types.TaskScopeWorkflowRun || next.ScopeID != run.ID {
					return fmt.Errorf("workflow next pending op does not belong to run")
				}
				if err := tx.Create(next).Error; err != nil {
					return err
				}
			}
			if err := tx.Delete(&pending).Error; err != nil {
				return err
			}
		}

		finished, err := finalizeWorkflowRunIfIdle(tx, &run, completion, now)
		if err != nil {
			return err
		}
		finalized = finished
		persistedRun = &run
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if alreadyCompleted {
		return persistedRun, isWorkflowRunTerminal(persistedRun.Status), ErrWorkflowNodeAlreadyCompleted
	}
	return persistedRun, finalized, nil
}

func validateWorkflowPendingOp(
	op *types.TaskPendingOp,
	run *types.WorkflowRun,
	branch *types.WorkflowRunBranch,
) error {
	if op.TenantID != run.TenantID || op.TaskType != types.TypeWorkflowNodeExecute ||
		op.Scope != types.TaskScopeWorkflowRun || op.ScopeID != run.ID {
		return fmt.Errorf("workflow pending op does not belong to run")
	}
	var payload types.WorkflowPendingNodePayload
	if err := json.Unmarshal(op.Payload, &payload); err != nil {
		return fmt.Errorf("decode workflow pending node: %w", err)
	}
	if payload.BranchID != branch.ID || payload.NodeID == "" || payload.Attempt <= 0 {
		return fmt.Errorf("workflow pending node payload is incomplete")
	}
	if branch.CurrentNodeID != "" && branch.CurrentNodeID != payload.NodeID {
		return fmt.Errorf("workflow pending node does not match branch checkpoint")
	}
	return nil
}

func validateWorkflowPendingNodePayload(op *types.TaskPendingOp, node *types.WorkflowRunNode) error {
	var payload types.WorkflowPendingNodePayload
	if err := json.Unmarshal(op.Payload, &payload); err != nil {
		return fmt.Errorf("decode workflow pending node: %w", err)
	}
	if payload.BranchID != node.BranchID || payload.NodeID != node.NodeID || payload.Attempt != node.Attempt {
		return fmt.Errorf("workflow pending node payload does not match node attempt")
	}
	return nil
}

func findWorkflowNodeAttempt(
	tx *gorm.DB,
	node *types.WorkflowRunNode,
	lock bool,
) (*types.WorkflowRunNode, error) {
	query := tx
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var stored types.WorkflowRunNode
	if node.ID > 0 {
		err := query.Where("id = ? AND run_id = ? AND tenant_id = ? AND agent_id = ?",
			node.ID, node.RunID, node.TenantID, node.AgentID).First(&stored).Error
		return &stored, err
	}
	err := query.Where(
		"run_id = ? AND tenant_id = ? AND agent_id = ? AND branch_id = ? AND node_id = ? AND attempt = ?",
		node.RunID, node.TenantID, node.AgentID, node.BranchID, node.NodeID, node.Attempt,
	).First(&stored).Error
	return &stored, err
}

func isWorkflowNodeTerminal(status string) bool {
	switch status {
	case types.WorkflowNodeStatusSucceeded,
		types.WorkflowNodeStatusFailed,
		types.WorkflowNodeStatusCanceled,
		types.WorkflowNodeStatusSkipped:
		return true
	default:
		return false
	}
}

func isWorkflowRunTerminal(status string) bool {
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

func nextWorkflowSequence(tx *gorm.DB, runID string) (int64, error) {
	var maxEvent int64
	if err := tx.Model(&types.WorkflowRunEvent{}).
		Where("run_id = ?", runID).
		Select("COALESCE(MAX(sequence), 0)").
		Scan(&maxEvent).Error; err != nil {
		return 0, err
	}
	var maxNode int64
	if err := tx.Model(&types.WorkflowRunNode{}).
		Where("run_id = ?", runID).
		Select("COALESCE(MAX(sequence), 0)").
		Scan(&maxNode).Error; err != nil {
		return 0, err
	}
	if maxNode > maxEvent {
		maxEvent = maxNode
	}
	return maxEvent + 1, nil
}

func applyWorkflowEventDefaults(event *types.WorkflowRunEvent, run *types.WorkflowRun, sequence int64) {
	now := time.Now()
	event.RunID = run.ID
	event.TenantID = run.TenantID
	event.AgentID = run.AgentID
	event.Sequence = sequence
	if event.EventID == "" {
		event.EventID = fmt.Sprintf("%s:%d", run.ID, sequence)
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = event.OccurredAt
	}
}

func workflowNodeStartUpdates(node *types.WorkflowRunNode) map[string]interface{} {
	return map[string]interface{}{
		"node_name":        node.NodeName,
		"node_type":        node.NodeType,
		"branch_path":      node.BranchPath,
		"sequence":         node.Sequence,
		"retry_of":         node.RetryOf,
		"task_id":          node.TaskID,
		"retryable":        node.Retryable,
		"status":           node.Status,
		"input_payload":    node.InputPayload,
		"input_summary":    node.InputSummary,
		"input_truncated":  node.InputTruncated,
		"output_payload":   types.JSON(nil),
		"output_summary":   "",
		"output_truncated": false,
		"error_code":       "",
		"error_summary":    "",
		"error_truncated":  false,
		"usage":            types.JSON(nil),
		"started_at":       node.StartedAt,
		"finished_at":      nil,
		"duration_ms":      0,
	}
}

func workflowNodeCompletionUpdates(node *types.WorkflowRunNode) map[string]interface{} {
	return map[string]interface{}{
		"task_id":          node.TaskID,
		"retryable":        node.Retryable,
		"status":           node.Status,
		"input_payload":    node.InputPayload,
		"input_summary":    node.InputSummary,
		"input_truncated":  node.InputTruncated,
		"output_payload":   node.OutputPayload,
		"output_summary":   node.OutputSummary,
		"output_truncated": node.OutputTruncated,
		"error_code":       node.ErrorCode,
		"error_summary":    node.ErrorSummary,
		"error_truncated":  node.ErrorTruncated,
		"usage":            node.Usage,
		"started_at":       node.StartedAt,
		"finished_at":      node.FinishedAt,
		"duration_ms":      node.DurationMs,
	}
}

func updateWorkflowBranchTx(tx *gorm.DB, branch *types.WorkflowRunBranch, expectedVersion int64) error {
	if expectedVersion <= 0 {
		return fmt.Errorf("workflow branch expected version is required")
	}
	result := tx.Model(&types.WorkflowRunBranch{}).
		Where("id = ? AND run_id = ? AND tenant_id = ? AND version = ?",
			branch.ID, branch.RunID, branch.TenantID, expectedVersion).
		Updates(map[string]interface{}{
			"branch_path":      branch.BranchPath,
			"current_node_id":  branch.CurrentNodeID,
			"variables":        branch.Variables,
			"status":           branch.Status,
			"error_code":       branch.ErrorCode,
			"error_summary":    branch.ErrorSummary,
			"last_node_run_id": branch.LastNodeRunID,
			"version":          gorm.Expr("version + 1"),
			"updated_at":       time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWorkflowRevisionConflict
	}
	branch.Version = expectedVersion + 1
	return nil
}

func finalizeWorkflowRunIfIdle(
	tx *gorm.DB,
	run *types.WorkflowRun,
	completion *types.WorkflowNodeCompletion,
	now time.Time,
) (bool, error) {
	var pendingCount int64
	if err := tx.Model(&types.TaskPendingOp{}).
		Where("scope = ? AND scope_id = ?", types.TaskScopeWorkflowRun, run.ID).
		Count(&pendingCount).Error; err != nil {
		return false, err
	}
	var activeBranches int64
	if err := tx.Model(&types.WorkflowRunBranch{}).
		Where("run_id = ? AND tenant_id = ? AND status IN ?", run.ID, run.TenantID,
			[]string{types.WorkflowBranchStatusPending, types.WorkflowBranchStatusRunning}).
		Count(&activeBranches).Error; err != nil {
		return false, err
	}
	if pendingCount > 0 || activeBranches > 0 {
		run.LastHeartbeatAt = &now
		if err := tx.Model(&types.WorkflowRun{}).
			Where("id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
			Update("last_heartbeat_at", now).Error; err != nil {
			return false, err
		}
		return false, nil
	}

	countStatus := func(status string) (int64, error) {
		var count int64
		err := tx.Model(&types.WorkflowRunBranch{}).
			Where("run_id = ? AND tenant_id = ? AND status = ?", run.ID, run.TenantID, status).
			Count(&count).Error
		return count, err
	}
	succeeded, err := countStatus(types.WorkflowBranchStatusSucceeded)
	if err != nil {
		return false, err
	}
	failed, err := countStatus(types.WorkflowBranchStatusFailed)
	if err != nil {
		return false, err
	}
	canceled, err := countStatus(types.WorkflowBranchStatusCanceled)
	if err != nil {
		return false, err
	}

	switch {
	case run.CancelRequestedAt != nil:
		run.Status = types.WorkflowRunStatusCanceled
	case succeeded > 0 && (failed > 0 || canceled > 0):
		run.Status = types.WorkflowRunStatusPartial
	case succeeded > 0:
		run.Status = types.WorkflowRunStatusSucceeded
	case canceled > 0 && failed == 0:
		run.Status = types.WorkflowRunStatusCanceled
	default:
		run.Status = types.WorkflowRunStatusFailed
	}
	run.FinishedAt = &now
	run.DurationMs = now.Sub(run.StartedAt).Milliseconds()
	run.OutputSummary, run.OutputTruncated, run.ErrorCode, run.ErrorSummary, run.ErrorTruncated, run.Usage, err = aggregateWorkflowRunResult(tx, run, completion)
	if err != nil {
		return false, err
	}
	if run.Status == types.WorkflowRunStatusSucceeded {
		run.ErrorCode = ""
		run.ErrorSummary = ""
		run.ErrorTruncated = false
	}
	if err := tx.Model(&types.WorkflowRun{}).
		Where("id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
		Updates(map[string]interface{}{
			"status":            run.Status,
			"output_summary":    run.OutputSummary,
			"output_truncated":  run.OutputTruncated,
			"error_code":        run.ErrorCode,
			"error_summary":     run.ErrorSummary,
			"error_truncated":   run.ErrorTruncated,
			"usage":             run.Usage,
			"last_heartbeat_at": now,
			"finished_at":       now,
			"duration_ms":       run.DurationMs,
		}).Error; err != nil {
		return false, err
	}
	run.LastHeartbeatAt = &now

	runEvent := completion.RunEvent
	if runEvent == nil {
		runEvent = &types.WorkflowRunEvent{EventType: "workflow_run.completed"}
		completion.RunEvent = runEvent
	}
	sequence, err := nextWorkflowSequence(tx, run.ID)
	if err != nil {
		return false, err
	}
	applyWorkflowEventDefaults(runEvent, run, sequence)
	runEvent.Status = run.Status
	runEvent.Summary = run.OutputSummary
	runEvent.SummaryTruncated = run.OutputTruncated
	runEvent.ErrorCode = run.ErrorCode
	runEvent.Error = run.ErrorSummary
	runEvent.DurationMs = run.DurationMs
	if runEvent.EventType == "" {
		runEvent.EventType = "workflow_run.completed"
	}
	if err := tx.Create(runEvent).Error; err != nil {
		return false, err
	}
	return true, nil
}

// aggregateWorkflowRunResult 从已持久化的节点和分支检查点归并运行结果。
//
// 工作流分支完成顺序不稳定，不能使用本次 completion 作为最终答案或用量来源；
// 必须在同一事务中读取全部成功终点、失败分支和节点 attempt，保证最终记录与检查点
// 一致。重试 attempt 的用量也会被计入运行成本，便于容量和计费分析。
func aggregateWorkflowRunResult(
	tx *gorm.DB,
	run *types.WorkflowRun,
	completion *types.WorkflowNodeCompletion,
) (string, bool, string, string, bool, types.JSON, error) {
	var nodes []types.WorkflowRunNode
	if err := tx.Where("run_id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
		Order("branch_path ASC, id ASC").Find(&nodes).Error; err != nil {
		return "", false, "", "", false, nil, err
	}
	var branches []types.WorkflowRunBranch
	if err := tx.Where("run_id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
		Order("branch_path ASC, id ASC").Find(&branches).Error; err != nil {
		return "", false, "", "", false, nil, err
	}

	answers := make([]string, 0)
	var usage types.TokenUsage
	usagePresent := false
	for _, node := range nodes {
		if node.NodeType == types.WorkflowNodeTypeEnd &&
			node.Status == types.WorkflowNodeStatusSucceeded &&
			strings.TrimSpace(node.OutputSummary) != "" {
			answers = append(answers, node.OutputSummary)
		}
		if len(node.Usage) > 0 && string(node.Usage) != "null" {
			var nodeUsage types.TokenUsage
			if err := json.Unmarshal(node.Usage, &nodeUsage); err != nil {
				return "", false, "", "", false, nil, fmt.Errorf("decode workflow node usage: %w", err)
			}
			usage.Accumulate(nodeUsage)
			usagePresent = true
		}
	}
	output := strings.Join(answers, "\n\n")

	failures := make([]string, 0)
	var firstErrorCode string
	for _, branch := range branches {
		if branch.Status != types.WorkflowBranchStatusFailed {
			continue
		}
		if branch.ErrorCode == "" && branch.ErrorSummary == "" {
			continue
		}
		if firstErrorCode == "" {
			firstErrorCode = branch.ErrorCode
		}
		summary := strings.TrimSpace(branch.ErrorSummary)
		if summary == "" {
			summary = branch.ErrorCode
		}
		if branch.BranchPath != "" {
			failures = append(failures, fmt.Sprintf("%s: %s", branch.BranchPath, summary))
		} else {
			failures = append(failures, summary)
		}
	}
	failureSummary := strings.Join(failures, "\n")
	if len(failures) > 0 {
		if output != "" {
			output += "\n\n"
		}
		output += "部分分支失败：\n" + failureSummary
	}
	if output == "" && completion != nil {
		output = completion.RunOutputSummary
	}
	output, outputTruncated := types.TruncateWorkflowSummary(output)

	if firstErrorCode == "" && completion != nil {
		firstErrorCode = completion.RunErrorCode
	}
	errorSummary := failureSummary
	if errorSummary == "" && completion != nil {
		errorSummary = completion.RunErrorSummary
	}
	if run.Status == types.WorkflowRunStatusCanceled && firstErrorCode == "" {
		firstErrorCode = types.WorkflowErrorCodeCanceled
		errorSummary = "运行已取消"
	}
	errorSummary, errorTruncated := types.TruncateWorkflowSummary(errorSummary)
	if !usagePresent && completion != nil && len(completion.RunUsage) > 0 {
		usage = types.TokenUsage{}
		if err := json.Unmarshal(completion.RunUsage, &usage); err != nil {
			return "", false, "", "", false, nil, fmt.Errorf("decode workflow completion usage: %w", err)
		}
		usagePresent = true
	}
	var usageJSON types.JSON
	if usagePresent {
		encoded, err := json.Marshal(usage)
		if err != nil {
			return "", false, "", "", false, nil, fmt.Errorf("encode workflow run usage: %w", err)
		}
		usageJSON = types.JSON(encoded)
	}
	return output, outputTruncated, firstErrorCode, errorSummary, errorTruncated, usageJSON, nil
}

// UpdateRun 更新指定运行记录的状态和摘要。
func (r *workflowRepository) UpdateRun(ctx context.Context, run *types.WorkflowRun) error {
	if run == nil {
		return fmt.Errorf("workflow run is required")
	}
	return r.db.WithContext(ctx).Model(&types.WorkflowRun{}).
		Where("id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
		Updates(run).Error
}

// RequestRunCancel 记录取消请求并尽快收敛排队分支。
//
// 没有活动节点时，所有待办都可以在本事务中删除并直接写入运行终态；存在活动
// 节点时只删除尚未认领的待办，正在执行的节点由 worker 的取消轮询完成收敛。
func (r *workflowRepository) RequestRunCancel(
	ctx context.Context,
	tenantID uint64,
	agentID, runID string,
	requestedAt time.Time,
) (*types.WorkflowRun, bool, error) {
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	var run types.WorkflowRun
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND agent_id = ?", runID, tenantID, agentID).
			First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowRunNotFound
			}
			return err
		}
		if isWorkflowRunTerminal(run.Status) {
			return nil
		}
		if run.CancelRequestedAt == nil {
			cancelAt := requestedAt
			run.CancelRequestedAt = &cancelAt
			if err := tx.Model(&types.WorkflowRun{}).
				Where("id = ? AND tenant_id = ? AND agent_id = ?", run.ID, run.TenantID, run.AgentID).
				Update("cancel_requested_at", cancelAt).Error; err != nil {
				return err
			}
			changed = true
		}

		var activeNodes int64
		if err := tx.Model(&types.WorkflowRunNode{}).
			Where("run_id = ? AND tenant_id = ? AND agent_id = ? AND status = ?",
				run.ID, run.TenantID, run.AgentID, types.WorkflowNodeStatusRunning).
			Count(&activeNodes).Error; err != nil {
			return err
		}
		branchStatuses := []string{types.WorkflowBranchStatusPending}
		if activeNodes == 0 {
			branchStatuses = append(branchStatuses, types.WorkflowBranchStatusRunning)
		}
		if err := tx.Model(&types.WorkflowRunBranch{}).
			Where("run_id = ? AND tenant_id = ? AND agent_id = ? AND status IN ?",
				run.ID, run.TenantID, run.AgentID, branchStatuses).
			Updates(map[string]interface{}{
				"status":          types.WorkflowBranchStatusCanceled,
				"current_node_id": "",
				"error_code":      types.WorkflowErrorCodeCanceled,
				"error_summary":   "运行已取消",
				"updated_at":      requestedAt,
			}).Error; err != nil {
			return err
		}
		pendingQuery := tx.Where("scope = ? AND scope_id = ?", types.TaskScopeWorkflowRun, run.ID)
		if activeNodes > 0 {
			pendingQuery = pendingQuery.Where("claimed_at IS NULL")
		}
		if err := pendingQuery.Delete(&types.TaskPendingOp{}).Error; err != nil {
			return err
		}
		if activeNodes == 0 {
			completion := &types.WorkflowNodeCompletion{
				RunEvent: &types.WorkflowRunEvent{EventType: "workflow_run.completed"},
			}
			_, err := finalizeWorkflowRunIfIdle(tx, &run, completion, requestedAt)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &run, changed, nil
}

// HeartbeatRun 更新活动运行心跳；运行已结束或不存在时不复活状态。
func (r *workflowRepository) HeartbeatRun(
	ctx context.Context,
	tenantID uint64,
	agentID, runID string,
	heartbeatAt time.Time,
) error {
	if heartbeatAt.IsZero() {
		heartbeatAt = time.Now()
	}
	return r.db.WithContext(ctx).Model(&types.WorkflowRun{}).
		Where(
			"id = ? AND tenant_id = ? AND agent_id = ? AND status = ?",
			runID, tenantID, agentID, types.WorkflowRunStatusRunning,
		).
		Update("last_heartbeat_at", heartbeatAt).Error
}

// CreateRunNode 写入节点运行记录。
func (r *workflowRepository) CreateRunNode(ctx context.Context, node *types.WorkflowRunNode) error {
	return r.db.WithContext(ctx).Create(node).Error
}

// UpdateRunNode 更新节点运行记录。
func (r *workflowRepository) UpdateRunNode(ctx context.Context, node *types.WorkflowRunNode) error {
	if node == nil {
		return fmt.Errorf("workflow run node is required")
	}
	return r.db.WithContext(ctx).Model(&types.WorkflowRunNode{}).
		Where("id = ? AND run_id = ? AND tenant_id = ?", node.ID, node.RunID, node.TenantID).
		Updates(node).Error
}

// CreateRunBranch 写入可恢复分支检查点。
func (r *workflowRepository) CreateRunBranch(ctx context.Context, branch *types.WorkflowRunBranch) error {
	if branch == nil {
		return fmt.Errorf("workflow run branch is required")
	}
	return r.db.WithContext(ctx).Create(branch).Error
}

// GetRunBranch 返回租户范围内的一条分支检查点。
func (r *workflowRepository) GetRunBranch(
	ctx context.Context,
	tenantID uint64,
	runID, branchID string,
) (*types.WorkflowRunBranch, error) {
	var branch types.WorkflowRunBranch
	err := r.db.WithContext(ctx).
		Where("id = ? AND run_id = ? AND tenant_id = ?", branchID, runID, tenantID).
		First(&branch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWorkflowRunNotFound
	}
	if err != nil {
		return nil, err
	}
	return &branch, nil
}

// UpdateRunBranch 以版本号乐观锁推进分支检查点。
func (r *workflowRepository) UpdateRunBranch(
	ctx context.Context,
	branch *types.WorkflowRunBranch,
	expectedVersion int64,
) error {
	if branch == nil {
		return fmt.Errorf("workflow run branch is required")
	}
	result := r.db.WithContext(ctx).Model(&types.WorkflowRunBranch{}).
		Where(
			"id = ? AND run_id = ? AND tenant_id = ? AND version = ?",
			branch.ID, branch.RunID, branch.TenantID, expectedVersion,
		).
		Updates(map[string]interface{}{
			"branch_path":      branch.BranchPath,
			"current_node_id":  branch.CurrentNodeID,
			"variables":        branch.Variables,
			"status":           branch.Status,
			"error_code":       branch.ErrorCode,
			"error_summary":    branch.ErrorSummary,
			"last_node_run_id": branch.LastNodeRunID,
			"version":          gorm.Expr("version + 1"),
			"updated_at":       time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWorkflowRevisionConflict
	}
	branch.Version = expectedVersion + 1
	return nil
}

// CreateRunEvent 持久化一条可续接的生命周期事件。
func (r *workflowRepository) CreateRunEvent(ctx context.Context, runEvent *types.WorkflowRunEvent) error {
	if runEvent == nil {
		return fmt.Errorf("workflow run event is required")
	}
	return r.db.WithContext(ctx).Create(runEvent).Error
}

// ListRunEventsAfter 按 sequence 正序返回增量事件。
func (r *workflowRepository) ListRunEventsAfter(
	ctx context.Context,
	tenantID uint64,
	agentID, runID string,
	afterSequence int64,
	limit int,
) ([]*types.WorkflowRunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	var exists int64
	if err := r.db.WithContext(ctx).Model(&types.WorkflowRun{}).
		Where("id = ? AND tenant_id = ? AND agent_id = ?", runID, tenantID, agentID).
		Count(&exists).Error; err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrWorkflowRunNotFound
	}
	var events []*types.WorkflowRunEvent
	err := r.db.WithContext(ctx).
		Where("run_id = ? AND tenant_id = ? AND agent_id = ? AND sequence > ?", runID, tenantID, agentID, afterSequence).
		Order("sequence ASC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

// ListRuns 按 started_at、id 倒序返回运行记录，并报告是否还有下一页。
func (r *workflowRepository) ListRuns(ctx context.Context, tenantID uint64, agentID string, query types.WorkflowRunQuery) ([]*types.WorkflowRun, bool, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	tx := r.db.WithContext(ctx).Where("tenant_id = ? AND agent_id = ?", tenantID, agentID)
	if query.Status != "" {
		tx = tx.Where("status = ?", query.Status)
	}
	if query.RunMode != "" {
		tx = tx.Where("run_mode = ?", query.RunMode)
	}
	if query.StartedAfter != nil {
		tx = tx.Where("started_at >= ?", *query.StartedAfter)
	}
	if query.StartedBefore != nil {
		tx = tx.Where("started_at < ?", *query.StartedBefore)
	}
	if query.BeforeStartedAt != nil {
		tx = tx.Where("(started_at < ? OR (started_at = ? AND id < ?))", *query.BeforeStartedAt, *query.BeforeStartedAt, query.BeforeID)
	}
	var rows []*types.WorkflowRun
	if err := tx.Order("started_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

// GetRun 返回运行元数据及其按序排列的节点记录。
func (r *workflowRepository) GetRun(ctx context.Context, tenantID uint64, agentID, runID string) (*types.WorkflowRun, error) {
	var run types.WorkflowRun
	if err := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND agent_id = ?", runID, tenantID, agentID).
		First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowRunNotFound
		}
		return nil, err
	}
	if err := r.db.WithContext(ctx).Where("run_id = ? AND tenant_id = ?", runID, tenantID).
		Order("sequence ASC, id ASC").Find(&run.Nodes).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

// DeleteRunsOlderThan 清理超过保留期的运行和节点记录。
func (r *workflowRepository) DeleteRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	return r.deleteRunsOlderThan(ctx, cutoff, "")
}

// DeleteRunsOlderThanMode 按运行模式分批清理过期记录。
func (r *workflowRepository) DeleteRunsOlderThanMode(
	ctx context.Context,
	cutoff time.Time,
	runMode string,
) (int64, error) {
	return r.deleteRunsOlderThan(ctx, cutoff, runMode)
}

func (r *workflowRepository) deleteRunsOlderThan(
	ctx context.Context,
	cutoff time.Time,
	runMode string,
) (int64, error) {
	const batchSize = 500
	var deleted int64
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		var batchDeleted int64
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			query := tx.Model(&types.WorkflowRun{}).
				Where("started_at < ?", cutoff).
				Order("started_at ASC, id ASC").
				Limit(batchSize)
			if runMode != "" {
				query = query.Where("run_mode = ?", runMode)
			}
			var ids []string
			if err := query.Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) == 0 {
				return nil
			}
			for _, model := range []interface{}{
				&types.WorkflowRunEvent{},
				&types.WorkflowRunNode{},
				&types.WorkflowRunBranch{},
			} {
				if err := tx.Where("run_id IN ?", ids).Delete(model).Error; err != nil {
					return err
				}
			}
			result := tx.Where("id IN ?", ids).Delete(&types.WorkflowRun{})
			if result.Error != nil {
				return result.Error
			}
			batchDeleted = result.RowsAffected
			return nil
		})
		if err != nil {
			return deleted, err
		}
		deleted += batchDeleted
		if batchDeleted < batchSize {
			return deleted, nil
		}
	}
}
