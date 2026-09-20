// Package interfaces defines the interface contracts for custom agent management
package interfaces

import (
	"context"
	"time"
)

// WorkflowRunRetentionService 是运行记录保留策略所需的最小能力集合。
//
// 单独定义而不是复用完整的 AgentService，是为了让后台清理器只依赖"按截止时间
// 删除历史运行"这一个动作，避免保留策略随着智能体服务接口的增长而被动扩容。
type WorkflowRunRetentionService interface {
	// DeleteWorkflowRunsOlderThan 删除在截止时间之前开始的运行及其节点记录。
	//
	// @param ctx 后台清理上下文。
	// @param cutoff 保留时间边界，早于该时间的运行会被删除。
	// @returns 删除的运行数量。
	DeleteWorkflowRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)

	// DeleteWorkflowRunsOlderThanMode 按运行模式清理，允许 debug 使用更短保留期。
	DeleteWorkflowRunsOlderThanMode(ctx context.Context, cutoff time.Time, runMode string) (int64, error)
}
