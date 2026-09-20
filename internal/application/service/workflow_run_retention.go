package service

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultWorkflowRunRetentionDays      = 90
	defaultWorkflowDebugRunRetentionDays = 14
)

// workflowRunPurgeInterval 是两次清理之间的间隔。
//
// 24 小时足够覆盖按天推进的保留边界：每次清理时截断点前进一天，正好删掉
// 一天滚出窗口的数据。缩短只会产生空扫描，延长则会多堆积一天陈旧记录。
const workflowRunPurgeInterval = 24 * time.Hour

// workflowRunPurgeStartupDelay 推迟首次清理，避免与启动期的迁移和其他初始化
// 工作争抢数据库连接。
const workflowRunPurgeStartupDelay = 10 * time.Minute

// WorkflowRunRetentionRunner 每天清理一次超出保留期的工作流运行记录。
//
// 复用 AuditLogRetentionRunner 的自包含 goroutine 模式：保留策略没有对钟点
// 的精确要求，只需要"大约每天、最终生效"，因此用 time.Ticker 而不是引入
// cron 或任务队列依赖。发布版本不在此清理范围内——它在智能体存续期间一直保留，
// 删除智能体时由外键级联负责回收。
type WorkflowRunRetentionRunner struct {
	svc            interfaces.WorkflowRunRetentionService
	interval       time.Duration
	productionDays int
	debugDays      int

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
	// started 在 startOnce.Do 内部先于 doneCh 绑定 goroutine 时置位，使 Stop
	// 能区分"从未 Start"和"正在运行"，而不必阻塞等待一个永不关闭的 doneCh。
	started atomic.Bool
}

// NewWorkflowRunRetentionRunner 构造运行记录清理器，使用生产默认参数。
//
// @param svc 提供按截止时间清理能力的服务。
// @returns 清理器实例；Start 被调用前不会有任何后台活动。
func NewWorkflowRunRetentionRunner(svc interfaces.WorkflowRunRetentionService) *WorkflowRunRetentionRunner {
	return &WorkflowRunRetentionRunner{
		svc:            svc,
		interval:       workflowRunPurgeInterval,
		productionDays: workflowRetentionDays("WEKNORA_WORKFLOW_RUN_RETENTION_DAYS", defaultWorkflowRunRetentionDays),
		debugDays:      workflowRetentionDays("WEKNORA_WORKFLOW_DEBUG_RUN_RETENTION_DAYS", defaultWorkflowDebugRunRetentionDays),
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
}

func workflowRetentionDays(envName string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

// Start 启动后台清理协程，重复调用是空操作。
//
// @param ctx 仅用于日志字段，不参与生命周期控制。
func (r *WorkflowRunRetentionRunner) Start(ctx context.Context) {
	if r == nil || r.svc == nil {
		return
	}
	r.startOnce.Do(func() {
		r.started.Store(true)
		logger.Infof(ctx, "[workflow-retention] starting daily sweep: production_days=%d debug_days=%d interval=%s",
			r.productionDays, r.debugDays, r.interval)
		go r.loop()
	})
}

// Stop 通知清理循环退出并等待其结束，可重复调用。
//
// 从未 Start 时立即返回：此时没有任何协程需要回收，等待 doneCh 会永久阻塞。
func (r *WorkflowRunRetentionRunner) Stop() {
	if r == nil {
		return
	}
	if !r.started.Load() {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	<-r.doneCh
}

// loop 按间隔驱动清理。
//
// 每轮都构造独立的超时上下文，因为 Start 传入的请求级 ctx 在容器初始化返回时
// 就会被取消。
func (r *WorkflowRunRetentionRunner) loop() {
	defer close(r.doneCh)

	startupTimer := time.NewTimer(workflowRunPurgeStartupDelay)
	defer startupTimer.Stop()
	select {
	case <-startupTimer.C:
	case <-r.stopCh:
		return
	}

	r.runOnce()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.runOnce()
		case <-r.stopCh:
			return
		}
	}
}

// runOnce 执行一次清理。
//
// 失败只记 WARN：记录继续多留一天不会影响功能，把清理失败升级为错误反而会
// 在每次数据库抖动时产生噪音告警。
func (r *WorkflowRunRetentionRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now()
	productionCutoff := now.Add(-time.Duration(r.productionDays) * 24 * time.Hour)
	productionDeleted, productionErr := r.svc.DeleteWorkflowRunsOlderThanMode(
		ctx, productionCutoff, types.WorkflowRunModeProduction,
	)
	debugCutoff := now.Add(-time.Duration(r.debugDays) * 24 * time.Hour)
	debugDeleted, debugErr := r.svc.DeleteWorkflowRunsOlderThanMode(
		ctx, debugCutoff, types.WorkflowRunModeDebug,
	)
	if productionErr != nil || debugErr != nil {
		logger.Warnf(ctx, "[workflow-retention] sweep failed: production_err=%v debug_err=%v",
			productionErr, debugErr)
		return
	}
	deleted := productionDeleted + debugDeleted
	if deleted > 0 {
		logger.Infof(ctx, "[workflow-retention] sweep complete: production_deleted=%d debug_deleted=%d",
			productionDeleted, debugDeleted)
		return
	}
	logger.Debugf(ctx, "[workflow-retention] sweep complete: deleted=0 production_days=%d debug_days=%d",
		r.productionDays, r.debugDays)
}
