package container

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type pendingWorkflowScope struct {
	TenantID uint64 `gorm:"column:tenant_id"`
	AgentID  string `gorm:"column:agent_id"`
	RunID    string `gorm:"column:run_id"`
}

// recoverPendingWorkflowTasks 为重启前已经写入数据库、但尚未成功唤醒的运行补发触发器。
//
// 工作流的真实待办存在 task_pending_ops；这里仅重建轻量 wake 任务。重复触发由
// ClaimBatch 和节点 attempt 唯一键幂等处理，因此 Redis、Lite 和多副本启动都可以安全执行。
func recoverPendingWorkflowTasks(db *gorm.DB, task interfaces.TaskEnqueuer) {
	if db == nil || task == nil {
		return
	}
	ctx := context.Background()
	var scopes []pendingWorkflowScope
	err := db.WithContext(ctx).
		Table("task_pending_ops AS ops").
		Select("ops.tenant_id, runs.agent_id, ops.scope_id AS run_id").
		Joins("JOIN workflow_runs AS runs ON runs.id = ops.scope_id AND runs.tenant_id = ops.tenant_id").
		Where("ops.task_type = ? AND ops.scope = ? AND runs.status = ?",
			types.TypeWorkflowNodeExecute, types.TaskScopeWorkflowRun, types.WorkflowRunStatusRunning).
		Group("ops.tenant_id, runs.agent_id, ops.scope_id").
		Find(&scopes).Error
	if err != nil {
		logger.Warnf(ctx, "[WorkflowRecovery] failed to list pending workflow runs: %v", err)
		return
	}

	recovered := 0
	for _, scope := range scopes {
		if scope.TenantID == 0 || scope.AgentID == "" || scope.RunID == "" {
			continue
		}
		payload, err := json.Marshal(types.WorkflowNodeTaskPayload{
			TenantID: scope.TenantID,
			AgentID:  scope.AgentID,
			RunID:    scope.RunID,
		})
		if err != nil {
			continue
		}
		options := []asynq.Option{
			asynq.Queue(types.QueueWorkflow),
			asynq.MaxRetry(3),
			asynq.Timeout(30 * time.Minute),
			asynq.TaskID("workflow-" + scope.RunID),
		}
		if _, err := task.Enqueue(asynq.NewTask(types.TypeWorkflowNodeExecute, payload, options...)); err != nil {
			logger.Warnf(ctx, "[WorkflowRecovery] enqueue run %s failed: %v", scope.RunID, err)
			continue
		}
		recovered++
	}
	if recovered > 0 {
		logger.Infof(ctx, "[WorkflowRecovery] recreated %d workflow wake task(s)", recovered)
	}
}
