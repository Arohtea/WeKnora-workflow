package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newWorkflowTestFixture 建立一套带工作流表的 SQLite 内存库。
//
// 用 uuid 命名 DSN 并限制单连接：工作流用例会在事务里做"读-改-写"，多个连接
// 共享同一内存库时事务之间可能互相看不到对方未提交的写入，导致断言随机失败。
func newWorkflowTestFixture(t *testing.T) (*gorm.DB, WorkflowRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.CustomAgent{},
		&types.WorkflowVersionRecord{},
		&types.WorkflowRun{},
		&types.WorkflowRunBranch{},
		&types.WorkflowRunNode{},
		&types.WorkflowRunEvent{},
		&types.TaskPendingOp{},
	))
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_runs_idempotency
		ON workflow_runs (tenant_id, agent_id, run_mode, idempotency_key)
		WHERE idempotency_key <> ''
	`).Error)
	return db, NewWorkflowRepository(db)
}

// seedWorkflowAgent 写入一个工作流智能体草稿，返回其租户与 ID。
func seedWorkflowAgent(t *testing.T, db *gorm.DB, tenantID uint64, agentID string) *types.CustomAgent {
	t.Helper()
	agent := &types.CustomAgent{
		ID:       agentID,
		TenantID: tenantID,
		Name:     "工作流智能体",
		Config: types.CustomAgentConfig{
			AgentType: types.AgentTypeWorkflow,
			Workflow:  types.DefaultWorkflowDefinition(),
		},
	}
	require.NoError(t, db.Create(agent).Error)
	return agent
}

// sampleDefinition 返回一个内容可辨识的定义快照，用于验证快照不被改写。
func sampleDefinition(marker string) *types.WorkflowDefinition {
	return &types.WorkflowDefinition{
		Version:       types.WorkflowVersion,
		SchemaVersion: types.WorkflowVersion,
		Nodes: []types.WorkflowNode{
			{ID: "start", Type: types.WorkflowNodeTypeStart, Name: marker + "-开始",
				BranchMode: types.WorkflowBranchModeFirstMatch},
		},
		Viewport: types.WorkflowViewport{Zoom: 1},
	}
}

// TestWorkflowSaveDraftRejectsStaleRevision 覆盖草稿的乐观并发控制。
//
// 编辑器可能长时间开着，用户 A 的保存不能悄悄覆盖用户 B 在同一期间的改动：
// 修订号不匹配时必须拒绝写入并保持库中数据不变。
func TestWorkflowSaveDraftRejectsStaleRevision(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")

	first := &types.CustomAgent{Name: "第一次保存"}
	require.NoError(t, repo.SaveDraft(ctx, 1, "agent-1", 0, first))
	assert.Equal(t, int64(1), first.DraftRevision, "成功保存后应在对象上回填新修订号")

	// 用已经过期的 revision=0 再存一次：必须冲突。
	stale := &types.CustomAgent{Name: "基于旧版本的覆盖"}
	err := repo.SaveDraft(ctx, 1, "agent-1", 0, stale)
	assert.ErrorIs(t, err, ErrWorkflowRevisionConflict)

	var persisted types.CustomAgent
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", 1, "agent-1").First(&persisted).Error)
	assert.Equal(t, "第一次保存", persisted.Name, "冲突时不能覆盖已有数据")
	assert.Equal(t, int64(1), persisted.DraftRevision, "冲突时修订号不能前进")

	// 用当前修订号保存应当成功并把修订号推进到 2。
	require.NoError(t, repo.SaveDraft(ctx, 1, "agent-1", 1, &types.CustomAgent{Name: "第二次保存"}))
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", 1, "agent-1").First(&persisted).Error)
	assert.Equal(t, "第二次保存", persisted.Name)
	assert.Equal(t, int64(2), persisted.DraftRevision)
}

// TestWorkflowSaveDraftRejectsNilAgent 覆盖空入参保护。
func TestWorkflowSaveDraftRejectsNilAgent(t *testing.T) {
	_, repo := newWorkflowTestFixture(t)
	err := repo.SaveDraft(context.Background(), 1, "agent-1", 0, nil)
	assert.Error(t, err)
}

// TestWorkflowPublishCreatesImmutableVersion 覆盖发布的版本生成与不可变性。
//
// 发布必须同时完成"落一个不可变版本"和"把智能体指向该版本"；已发布版本的内容
// 是这次运行的可追溯依据，后续任何草稿编辑都不能改写它。
func TestWorkflowPublishCreatesImmutableVersion(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")

	first, err := repo.Publish(ctx, 1, "agent-1", "user-1", 0, sampleDefinition("v1"), types.CustomAgentConfig{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.Version, "首次发布应为版本 1")
	assert.Equal(t, int64(0), first.DraftRevision)

	var agent types.CustomAgent
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", 1, "agent-1").First(&agent).Error)
	assert.Equal(t, int64(1), agent.PublishedVersion, "发布后 published_version 必须递增")

	// 再次发布应生成更高版本，而不是覆盖版本 1。
	second, err := repo.Publish(ctx, 1, "agent-1", "user-1", 0, sampleDefinition("v2"), types.CustomAgentConfig{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), second.Version)

	require.NoError(t, db.Where("tenant_id = ? AND id = ?", 1, "agent-1").First(&agent).Error)
	assert.Equal(t, int64(2), agent.PublishedVersion)

	// 版本 1 的内容必须还是第一次发布的那份。
	stored, err := repo.GetVersion(ctx, 1, "agent-1", 1)
	require.NoError(t, err)
	var definition types.WorkflowDefinition
	require.NoError(t, json.Unmarshal(stored.Definition, &definition))
	require.Len(t, definition.Nodes, 1)
	assert.Equal(t, "v1-开始", definition.Nodes[0].Name, "已发布版本不允许被后续操作改写")

	// 当前版本应指向最新发布。
	current, err := repo.GetCurrentVersion(ctx, 1, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), current.Version)

	versions, err := repo.ListVersions(ctx, 1, "agent-1")
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, int64(2), versions[0].Version, "版本历史应按版本号从新到旧排列")
	assert.Equal(t, int64(1), versions[1].Version)
}

// TestWorkflowPublishRejectsStaleRevision 覆盖发布时的修订号冲突。
//
// 发布本质上是"把当前草稿冻结成版本"，如果客户端基于过期草稿发布，冻结点就错了。
func TestWorkflowPublishRejectsStaleRevision(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")
	require.NoError(t, repo.SaveDraft(ctx, 1, "agent-1", 0, &types.CustomAgent{Name: "草稿"}))

	_, err := repo.Publish(ctx, 1, "agent-1", "user-1", 5, sampleDefinition("stale"), types.CustomAgentConfig{})
	assert.ErrorIs(t, err, ErrWorkflowRevisionConflict)

	count, err := repo.ListVersions(ctx, 1, "agent-1")
	require.NoError(t, err)
	assert.Empty(t, count, "冲突时不应留下半个版本")

	var agent types.CustomAgent
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", 1, "agent-1").First(&agent).Error)
	assert.Equal(t, int64(0), agent.PublishedVersion, "冲突时 published_version 不能前进")
}

// TestWorkflowGetCurrentVersionWithoutPublish 覆盖从未发布时的读取。
func TestWorkflowGetCurrentVersionWithoutPublish(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	seedWorkflowAgent(t, db, 1, "agent-1")

	_, err := repo.GetCurrentVersion(context.Background(), 1, "agent-1")
	assert.ErrorIs(t, err, ErrWorkflowVersionNotFound)
}

// TestWorkflowRunSnapshotRoundTrip 覆盖运行快照的写入与读回。
//
// 运行记录是排障的唯一依据：读回的定义快照必须与当次执行的那份完全一致，
// 否则"当时到底跑的是什么图"就没有答案。
func TestWorkflowRunSnapshotRoundTrip(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")

	definition := sampleDefinition("run")
	snapshot, err := json.Marshal(definition)
	require.NoError(t, err)
	run := &types.WorkflowRun{
		ID:                 "run-1",
		TenantID:           1,
		AgentID:            "agent-1",
		WorkflowVersion:    3,
		DefinitionSnapshot: types.JSON(snapshot),
		ConfigSnapshot:     types.JSON(`{"agent_mode":"smart-reasoning"}`),
		TriggerSource:      types.WorkflowTriggerChat,
		Status:             types.WorkflowRunStatusRunning,
		SessionID:          "session-1",
		MessageID:          "message-1",
		InputSummary:       "用户提问",
		StartedAt:          time.Now().Add(-time.Minute),
		CreatedAt:          time.Now().Add(-time.Minute),
	}
	require.NoError(t, repo.CreateRun(ctx, run))

	// 写入两个节点记录，sequence 故意乱序，读取必须按 sequence 排序。
	for index, nodeID := range []string{"second", "first"} {
		startedAt := time.Now()
		require.NoError(t, repo.CreateRunNode(ctx, &types.WorkflowRunNode{
			RunID:     run.ID,
			TenantID:  1,
			AgentID:   "agent-1",
			NodeID:    nodeID,
			NodeName:  nodeID,
			NodeType:  types.WorkflowNodeTypeLLM,
			Sequence:  int64(2 - index),
			Status:    types.WorkflowNodeStatusSucceeded,
			StartedAt: &startedAt,
			CreatedAt: startedAt,
		}))
	}

	stored, err := repo.GetRun(ctx, 1, "agent-1", run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), stored.WorkflowVersion)
	assert.JSONEq(t, string(snapshot), string(stored.DefinitionSnapshot), "定义快照必须原样读回")
	require.Len(t, stored.Nodes, 2)
	assert.Equal(t, "first", stored.Nodes[0].NodeID, "节点记录应按 sequence 升序返回")
	assert.Equal(t, "second", stored.Nodes[1].NodeID)

	// 终态更新必须能按 ID + 租户 + 智能体定位到同一行。
	finishedAt := time.Now()
	stored.Status = types.WorkflowRunStatusPartial
	stored.FinishedAt = &finishedAt
	stored.DurationMs = 1234
	require.NoError(t, repo.UpdateRun(ctx, stored))

	reread, err := repo.GetRun(ctx, 1, "agent-1", run.ID)
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusPartial, reread.Status)
	require.NotNil(t, reread.FinishedAt)
	assert.Equal(t, int64(1234), reread.DurationMs)
}

// TestWorkflowRunIsolationAcrossTenants 覆盖跨租户隔离。
//
// 运行与版本都可能包含用户数据与凭据摘要，跨租户读取一旦成立就是数据泄漏，
// 因此必须返回 not found 而不是空对象或错误行。
func TestWorkflowRunIsolationAcrossTenants(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")
	seedWorkflowAgent(t, db, 2, "agent-2")

	_, err := repo.Publish(ctx, 1, "agent-1", "user-1", 0, sampleDefinition("tenant-1"), types.CustomAgentConfig{})
	require.NoError(t, err)
	run := &types.WorkflowRun{
		ID: "run-tenant-1", TenantID: 1, AgentID: "agent-1", WorkflowVersion: 1,
		DefinitionSnapshot: types.JSON(`{"version":2}`), ConfigSnapshot: types.JSON(`{}`),
		TriggerSource: types.WorkflowTriggerChat, Status: types.WorkflowRunStatusSucceeded,
		StartedAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateRun(ctx, run))

	// 另一个租户用同样的 run ID / 版本号探测，必须拿不到数据。
	_, err = repo.GetRun(ctx, 2, "agent-1", "run-tenant-1")
	assert.ErrorIs(t, err, ErrWorkflowRunNotFound)
	_, err = repo.GetRun(ctx, 1, "agent-2", "run-tenant-1")
	assert.ErrorIs(t, err, ErrWorkflowRunNotFound)
	_, err = repo.GetVersion(ctx, 2, "agent-1", 1)
	assert.ErrorIs(t, err, ErrWorkflowVersionNotFound)
	_, err = repo.GetCurrentVersion(ctx, 2, "agent-1")
	assert.ErrorIs(t, err, ErrWorkflowVersionNotFound)

	versions, err := repo.ListVersions(ctx, 2, "agent-1")
	require.NoError(t, err)
	assert.Empty(t, versions, "版本列表不得泄漏其他租户的版本")
	runs, _, err := repo.ListRuns(ctx, 2, "agent-1", types.WorkflowRunQuery{})
	require.NoError(t, err)
	assert.Empty(t, runs, "运行列表不得泄漏其他租户的运行")
}

// TestWorkflowListRunsFiltersByStatusAndCursor 覆盖状态筛选与游标分页。
//
// 运行列表是排障入口：筛选错会把失败运行藏起来，游标错会让人翻页时反复看到
// 同一批记录或漏掉记录。
func TestWorkflowListRunsFiltersByStatusAndCursor(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	// 三条成功 + 一条失败，started_at 递增；同一时刻的两条用于验证 ID 兜底排序。
	seeds := []struct {
		id        string
		status    string
		startedAt time.Time
	}{
		{"run-1", types.WorkflowRunStatusSucceeded, base},
		{"run-2", types.WorkflowRunStatusFailed, base.Add(time.Minute)},
		{"run-3", types.WorkflowRunStatusSucceeded, base.Add(2 * time.Minute)},
		{"run-4", types.WorkflowRunStatusSucceeded, base.Add(2 * time.Minute)},
		{"run-5", types.WorkflowRunStatusSucceeded, base.Add(3 * time.Minute)},
	}
	for _, seed := range seeds {
		require.NoError(t, repo.CreateRun(ctx, &types.WorkflowRun{
			ID: seed.id, TenantID: 1, AgentID: "agent-1", WorkflowVersion: 1,
			DefinitionSnapshot: types.JSON(`{"version":2}`), ConfigSnapshot: types.JSON(`{}`),
			TriggerSource: types.WorkflowTriggerChat, Status: seed.status,
			StartedAt: seed.startedAt, CreatedAt: seed.startedAt,
		}))
	}

	// 状态筛选：只返回失败的那条。
	failed, hasMore, err := repo.ListRuns(ctx, 1, "agent-1", types.WorkflowRunQuery{
		Status: types.WorkflowRunStatusFailed,
	})
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, failed, 1)
	assert.Equal(t, "run-2", failed[0].ID)

	// 首页：limit=2，按 started_at DESC + id DESC，应取 run-5、run-4 并标记还有下一页。
	firstPage, hasMore, err := repo.ListRuns(ctx, 1, "agent-1", types.WorkflowRunQuery{Limit: 2})
	require.NoError(t, err)
	assert.True(t, hasMore, "还有未返回的记录时必须标记 hasMore")
	require.Len(t, firstPage, 2)
	assert.Equal(t, "run-5", firstPage[0].ID)
	assert.Equal(t, "run-4", firstPage[1].ID)

	// 用最后一条作为游标取下一页，必须严格续接、不重不漏。
	last := firstPage[len(firstPage)-1]
	secondPage, hasMore, err := repo.ListRuns(ctx, 1, "agent-1", types.WorkflowRunQuery{
		Limit:           2,
		BeforeStartedAt: &last.StartedAt,
		BeforeID:        last.ID,
	})
	require.NoError(t, err)
	assert.True(t, hasMore, "第二页之后仍有记录")
	require.Len(t, secondPage, 2)
	assert.Equal(t, "run-3", secondPage[0].ID)
	assert.Equal(t, "run-2", secondPage[1].ID)

	// 第三页只剩最后一条：hasMore 必须回到 false，否则前端会无限翻页。
	lastSecond := secondPage[len(secondPage)-1]
	thirdPage, hasMore, err := repo.ListRuns(ctx, 1, "agent-1", types.WorkflowRunQuery{
		Limit:           2,
		BeforeStartedAt: &lastSecond.StartedAt,
		BeforeID:        lastSecond.ID,
	})
	require.NoError(t, err)
	assert.False(t, hasMore, "取到末页时 hasMore 必须为 false")
	require.Len(t, thirdPage, 1)
	assert.Equal(t, "run-1", thirdPage[0].ID)
}

// TestWorkflowDeleteRunsOlderThanCascadesNodes 覆盖保留期清理。
//
// 清理只按 started_at 判断，且必须把节点记录一起删掉：留下孤儿 node 行会让
// 轨迹表持续膨胀，也让"运行列表里没有、轨迹接口却能查出节点"变成数据不一致。
func TestWorkflowDeleteRunsOlderThanCascadesNodes(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")

	cutoff := time.Now().Add(-24 * time.Hour)
	old := cutoff.Add(-time.Hour)
	fresh := cutoff.Add(time.Hour)
	for _, seed := range []struct {
		id        string
		startedAt time.Time
	}{{"run-old", old}, {"run-fresh", fresh}} {
		require.NoError(t, repo.CreateRun(ctx, &types.WorkflowRun{
			ID: seed.id, TenantID: 1, AgentID: "agent-1", WorkflowVersion: 1,
			DefinitionSnapshot: types.JSON(`{"version":2}`), ConfigSnapshot: types.JSON(`{}`),
			TriggerSource: types.WorkflowTriggerChat, Status: types.WorkflowRunStatusSucceeded,
			StartedAt: seed.startedAt, CreatedAt: seed.startedAt,
		}))
		startedAt := seed.startedAt
		require.NoError(t, repo.CreateRunNode(ctx, &types.WorkflowRunNode{
			RunID: seed.id, TenantID: 1, AgentID: "agent-1",
			NodeID: "node-" + seed.id, NodeName: "节点", NodeType: types.WorkflowNodeTypeLLM,
			Sequence: 1, Status: types.WorkflowNodeStatusSucceeded,
			StartedAt: &startedAt, CreatedAt: startedAt,
		}))
	}

	deleted, err := repo.DeleteRunsOlderThan(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "只应删除截止时间之前的运行")

	_, err = repo.GetRun(ctx, 1, "agent-1", "run-old")
	assert.ErrorIs(t, err, ErrWorkflowRunNotFound)
	_, err = repo.GetRun(ctx, 1, "agent-1", "run-fresh")
	assert.NoError(t, err, "截止时间之后的运行必须保留")

	var nodeCount int64
	require.NoError(t, db.Model(&types.WorkflowRunNode{}).Where("run_id = ?", "run-old").Count(&nodeCount).Error)
	assert.Equal(t, int64(0), nodeCount, "被删除运行的节点记录必须级联删除")

	require.NoError(t, db.Model(&types.WorkflowRunNode{}).Where("run_id = ?", "run-fresh").Count(&nodeCount).Error)
	assert.Equal(t, int64(1), nodeCount, "保留运行的节点记录不能被误删")

	// 没有命中任何记录时应返回 0 且不报错。
	deleted, err = repo.DeleteRunsOlderThan(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}

// TestWorkflowCreateRunWithInitialNodeIsIdempotent 覆盖运行初始化的事务边界。
//
// 同一幂等键重复提交时只能存在一条运行、一个根分支和一个首节点待办；否则编辑器
// 网络重试会把一次试跑变成两次真实执行。
func TestWorkflowCreateRunWithInitialNodeIsIdempotent(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")

	run, branch, pending, started := newWorkflowExecutionSeed(t, "run-1", "idem-1")
	persisted, created, err := repo.CreateRunWithInitialNode(ctx, run, branch, pending, started)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, "run-1", persisted.ID)
	assert.Equal(t, int64(1), started.Sequence)

	duplicateRun, duplicateBranch, duplicatePending, duplicateStarted := newWorkflowExecutionSeed(t, "run-2", "idem-1")
	persisted, created, err = repo.CreateRunWithInitialNode(
		ctx, duplicateRun, duplicateBranch, duplicatePending, duplicateStarted,
	)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "run-1", persisted.ID, "重复请求必须返回第一次创建的运行")

	for model, expected := range map[interface{}]int64{
		&types.WorkflowRun{}:       1,
		&types.WorkflowRunBranch{}: 1,
		&types.TaskPendingOp{}:     1,
		&types.WorkflowRunEvent{}:  1,
	} {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		assert.Equal(t, expected, count)
	}
}

// TestWorkflowNodeCheckpointSchedulesNextAndFinalizes 覆盖单节点状态机的完整推进。
//
// 首节点完成后，节点终态、分支变量和下一节点待办必须同时可见；末节点完成后不再
// 存在活动分支或待办，运行才允许归并为 succeeded。
func TestWorkflowNodeCheckpointSchedulesNextAndFinalizes(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")
	run, branch, pending, started := newWorkflowExecutionSeed(t, "run-1", "")
	_, created, err := repo.CreateRunWithInitialNode(ctx, run, branch, pending, started)
	require.NoError(t, err)
	require.True(t, created)

	startNode := &types.WorkflowRunNode{
		RunID: "run-1", TenantID: 1, AgentID: "agent-1",
		NodeID: "start", NodeName: "开始", NodeType: types.WorkflowNodeTypeStart,
		BranchID: branch.ID, BranchPath: "start", Attempt: 1, TaskID: "task-start",
	}
	require.NoError(t, repo.StartWorkflowNode(ctx, pending.ID, startNode, &types.WorkflowRunEvent{
		EventType: "workflow_node.started", Status: types.WorkflowNodeStatusRunning,
	}))
	assert.NotZero(t, startNode.ID)
	assert.Equal(t, int64(2), startNode.Sequence)

	nextPayload, err := json.Marshal(types.WorkflowPendingNodePayload{
		BranchID: branch.ID, NodeID: "end", Attempt: 1,
	})
	require.NoError(t, err)
	startNode.Status = types.WorkflowNodeStatusSucceeded
	startNode.OutputSummary = "已接收输入"
	branch.Status = types.WorkflowBranchStatusPending
	branch.CurrentNodeID = "end"
	branch.Variables = types.JSON(`{"input":{"query":"hello"},"nodes":{"start":{"status":"success"}}}`)
	updatedRun, finalized, err := repo.CompleteWorkflowNode(ctx, &types.WorkflowNodeCompletion{
		PendingOpID:           pending.ID,
		Node:                  startNode,
		Branch:                branch,
		ExpectedBranchVersion: 1,
		Event: &types.WorkflowRunEvent{
			EventType: "workflow_node.completed", Status: types.WorkflowNodeStatusSucceeded,
		},
		NextPendingOps: []*types.TaskPendingOp{{
			TenantID: 1, TaskType: types.TypeWorkflowNodeExecute,
			Scope: types.TaskScopeWorkflowRun, ScopeID: "run-1", Op: "execute",
			DedupKey: branch.ID + ":end:1", Payload: nextPayload,
		}},
	})
	require.NoError(t, err)
	assert.False(t, finalized)
	assert.Equal(t, types.WorkflowRunStatusRunning, updatedRun.Status)
	assert.Equal(t, int64(2), branch.Version)

	var queued []*types.TaskPendingOp
	require.NoError(t, db.Where("scope = ? AND scope_id = ?", types.TaskScopeWorkflowRun, "run-1").Find(&queued).Error)
	require.Len(t, queued, 1)
	assert.Equal(t, "end", pendingNodeID(t, queued[0]))

	endNode := &types.WorkflowRunNode{
		RunID: "run-1", TenantID: 1, AgentID: "agent-1",
		NodeID: "end", NodeName: "结束", NodeType: types.WorkflowNodeTypeEnd,
		BranchID: branch.ID, BranchPath: "start/end", Attempt: 1, TaskID: "task-end",
	}
	require.NoError(t, repo.StartWorkflowNode(ctx, queued[0].ID, endNode, &types.WorkflowRunEvent{
		EventType: "workflow_node.started", Status: types.WorkflowNodeStatusRunning,
	}))
	assert.Equal(t, int64(4), endNode.Sequence)

	checkpoint, err := repo.GetRunBranch(ctx, 1, "run-1", branch.ID)
	require.NoError(t, err)
	endNode.Status = types.WorkflowNodeStatusSucceeded
	endNode.OutputSummary = "最终答案"
	checkpoint.Status = types.WorkflowBranchStatusSucceeded
	checkpoint.CurrentNodeID = ""
	finishedRun, finalized, err := repo.CompleteWorkflowNode(ctx, &types.WorkflowNodeCompletion{
		PendingOpID:           queued[0].ID,
		Node:                  endNode,
		Branch:                checkpoint,
		ExpectedBranchVersion: checkpoint.Version,
		Event: &types.WorkflowRunEvent{
			EventType: "workflow_node.completed", Status: types.WorkflowNodeStatusSucceeded,
		},
		RunEvent:         &types.WorkflowRunEvent{EventType: "workflow_run.completed"},
		RunOutputSummary: "最终答案",
	})
	require.NoError(t, err)
	assert.True(t, finalized)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, finishedRun.Status)
	assert.Equal(t, "最终答案", finishedRun.OutputSummary)
	require.NotNil(t, finishedRun.FinishedAt)

	var pendingCount int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).
		Where("scope = ? AND scope_id = ?", types.TaskScopeWorkflowRun, "run-1").Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)

	events, err := repo.ListRunEventsAfter(ctx, 1, "agent-1", "run-1", 0, 20)
	require.NoError(t, err)
	require.Len(t, events, 6)
	for index, item := range events {
		assert.Equal(t, int64(index+1), item.Sequence, "生命周期事件 sequence 必须连续且唯一")
	}

	_, finalized, err = repo.CompleteWorkflowNode(ctx, &types.WorkflowNodeCompletion{
		PendingOpID:           queued[0].ID,
		Node:                  endNode,
		Branch:                checkpoint,
		ExpectedBranchVersion: checkpoint.Version,
	})
	assert.ErrorIs(t, err, ErrWorkflowNodeAlreadyCompleted)
	assert.True(t, finalized, "重复投递已完成节点时应返回现有运行终态")
}

// TestWorkflowNodeCompletionStaleBranchRollsBack 覆盖检查点乐观锁失败。
//
// 分支版本过期时，节点终态、完成事件和待办删除都必须回滚；否则另一个 worker 会
// 看到半提交状态并跳过本应继续执行的节点。
func TestWorkflowNodeCompletionStaleBranchRollsBack(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")
	run, branch, pending, started := newWorkflowExecutionSeed(t, "run-1", "")
	_, _, err := repo.CreateRunWithInitialNode(ctx, run, branch, pending, started)
	require.NoError(t, err)

	node := &types.WorkflowRunNode{
		RunID: "run-1", TenantID: 1, AgentID: "agent-1",
		NodeID: "start", NodeName: "开始", NodeType: types.WorkflowNodeTypeStart,
		BranchID: branch.ID, BranchPath: "start", Attempt: 1,
	}
	require.NoError(t, repo.StartWorkflowNode(ctx, pending.ID, node, &types.WorkflowRunEvent{
		EventType: "workflow_node.started", Status: types.WorkflowNodeStatusRunning,
	}))
	node.Status = types.WorkflowNodeStatusSucceeded
	branch.Status = types.WorkflowBranchStatusSucceeded
	branch.CurrentNodeID = ""
	_, _, err = repo.CompleteWorkflowNode(ctx, &types.WorkflowNodeCompletion{
		PendingOpID:           pending.ID,
		Node:                  node,
		Branch:                branch,
		ExpectedBranchVersion: 99,
		Event: &types.WorkflowRunEvent{
			EventType: "workflow_node.completed", Status: types.WorkflowNodeStatusSucceeded,
		},
	})
	assert.ErrorIs(t, err, ErrWorkflowRevisionConflict)

	var stored types.WorkflowRunNode
	require.NoError(t, db.First(&stored, node.ID).Error)
	assert.Equal(t, types.WorkflowNodeStatusRunning, stored.Status, "节点终态必须随事务回滚")
	var pendingCount int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", pending.ID).Count(&pendingCount).Error)
	assert.Equal(t, int64(1), pendingCount, "当前待办不能在检查点冲突时丢失")
	var eventCount int64
	require.NoError(t, db.Model(&types.WorkflowRunEvent{}).Where("run_id = ?", "run-1").Count(&eventCount).Error)
	assert.Equal(t, int64(2), eventCount, "失败事务不能留下完成事件")
}

// TestWorkflowFinalizationAggregatesPersistedBranches 覆盖并行分支终态归并。
//
// 最后完成的分支不一定是答案最完整的分支；终态必须从所有节点检查点读取答案、
// 失败摘要和 token 用量，不能只使用本次 completion 携带的字段。
func TestWorkflowFinalizationAggregatesPersistedBranches(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")
	now := time.Now()
	run := &types.WorkflowRun{
		ID: "run-aggregate", TenantID: 1, AgentID: "agent-1", WorkflowVersion: 1,
		DefinitionSnapshot: types.JSON(`{"version":2}`), ConfigSnapshot: types.JSON(`{}`),
		TriggerSource: types.WorkflowTriggerDebug, RunMode: types.WorkflowRunModeDebug,
		Status: types.WorkflowRunStatusRunning, StartedAt: now.Add(-time.Minute), CreatedAt: now,
	}
	require.NoError(t, repo.CreateRun(ctx, run))
	branches := []*types.WorkflowRunBranch{
		{ID: "branch-a", RunID: run.ID, TenantID: 1, AgentID: run.AgentID, BranchPath: "a", CurrentNodeID: "end-a", Variables: types.JSON(`{"input":{},"nodes":{}}`), Status: types.WorkflowBranchStatusPending, Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "branch-b", RunID: run.ID, TenantID: 1, AgentID: run.AgentID, BranchPath: "b", Variables: types.JSON(`{"input":{},"nodes":{}}`), Status: types.WorkflowBranchStatusSucceeded, Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "branch-c", RunID: run.ID, TenantID: 1, AgentID: run.AgentID, BranchPath: "c", Variables: types.JSON(`{"input":{},"nodes":{}}`), Status: types.WorkflowBranchStatusFailed, ErrorCode: types.WorkflowErrorCodeUpstream, ErrorSummary: "上游服务失败", Version: 1, CreatedAt: now, UpdatedAt: now},
	}
	for _, branch := range branches {
		require.NoError(t, repo.CreateRunBranch(ctx, branch))
	}
	usageB, err := json.Marshal(types.TokenUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3})
	require.NoError(t, err)
	start := now.Add(-time.Second)
	branchBNode := &types.WorkflowRunNode{
		RunID: run.ID, TenantID: 1, AgentID: run.AgentID, NodeID: "end-b", NodeName: "结束 B",
		NodeType: types.WorkflowNodeTypeEnd, BranchID: "branch-b", BranchPath: "b/end", Sequence: 1,
		Status: types.WorkflowNodeStatusSucceeded, OutputSummary: "答案 B", Usage: types.JSON(usageB),
		StartedAt: &start, CreatedAt: start,
	}
	require.NoError(t, repo.CreateRunNode(ctx, branchBNode))
	usageA, err := json.Marshal(types.TokenUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2})
	require.NoError(t, err)
	branchANode := &types.WorkflowRunNode{
		RunID: run.ID, TenantID: 1, AgentID: run.AgentID, NodeID: "end-a", NodeName: "结束 A",
		NodeType: types.WorkflowNodeTypeEnd, BranchID: "branch-a", BranchPath: "a/end", Sequence: 2,
		Status: types.WorkflowNodeStatusRunning, Usage: types.JSON(usageA), StartedAt: &start, CreatedAt: start,
	}
	require.NoError(t, repo.CreateRunNode(ctx, branchANode))
	payload, err := json.Marshal(types.WorkflowPendingNodePayload{BranchID: "branch-a", NodeID: "end-a", Attempt: 1})
	require.NoError(t, err)
	pending := &types.TaskPendingOp{TenantID: 1, TaskType: types.TypeWorkflowNodeExecute, Scope: types.TaskScopeWorkflowRun, ScopeID: run.ID, Op: "execute", DedupKey: "branch-a:end-a:1", Payload: payload}
	require.NoError(t, db.Create(pending).Error)

	branchA := *branches[0]
	branchA.Status = types.WorkflowBranchStatusSucceeded
	branchA.CurrentNodeID = ""
	branchA.Version = 1
	branchANode.Status = types.WorkflowNodeStatusSucceeded
	branchANode.OutputSummary = "答案 A"
	finished, finalized, err := repo.CompleteWorkflowNode(ctx, &types.WorkflowNodeCompletion{
		PendingOpID: pending.ID, Node: branchANode, Branch: &branchA, ExpectedBranchVersion: 1,
		Event:    &types.WorkflowRunEvent{EventType: "workflow_node.completed", Status: types.WorkflowNodeStatusSucceeded},
		RunEvent: &types.WorkflowRunEvent{EventType: "workflow_run.completed"},
	})
	require.NoError(t, err)
	require.True(t, finalized)
	assert.Equal(t, types.WorkflowRunStatusPartial, finished.Status)
	assert.Contains(t, finished.OutputSummary, "答案 A")
	assert.Contains(t, finished.OutputSummary, "答案 B")
	assert.Contains(t, finished.OutputSummary, "上游服务失败")
	assert.Equal(t, types.WorkflowErrorCodeUpstream, finished.ErrorCode)
	var totalUsage types.TokenUsage
	require.NoError(t, json.Unmarshal(finished.Usage, &totalUsage))
	assert.Equal(t, 5, totalUsage.TotalTokens)
}

// TestWorkflowCancelQueuedRunFinalizesImmediately 覆盖未启动节点的取消路径。
func TestWorkflowCancelQueuedRunFinalizesImmediately(t *testing.T) {
	db, repo := newWorkflowTestFixture(t)
	ctx := context.Background()
	seedWorkflowAgent(t, db, 1, "agent-1")
	run, branch, pending, started := newWorkflowExecutionSeed(t, "run-cancel", "")
	_, created, err := repo.CreateRunWithInitialNode(ctx, run, branch, pending, started)
	require.NoError(t, err)
	require.True(t, created)

	canceled, changed, err := repo.RequestRunCancel(ctx, 1, "agent-1", run.ID, time.Now())
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, types.WorkflowRunStatusCanceled, canceled.Status)
	assert.Equal(t, types.WorkflowErrorCodeCanceled, canceled.ErrorCode)
	var pendingCount int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("scope = ? AND scope_id = ?", types.TaskScopeWorkflowRun, run.ID).Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
	checkpoint, err := repo.GetRunBranch(ctx, 1, run.ID, branch.ID)
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowBranchStatusCanceled, checkpoint.Status)
	assert.Equal(t, types.WorkflowErrorCodeCanceled, checkpoint.ErrorCode)

	again, changed, err := repo.RequestRunCancel(ctx, 1, "agent-1", run.ID, time.Now())
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, types.WorkflowRunStatusCanceled, again.Status)
	var completedEvents int64
	require.NoError(t, db.Model(&types.WorkflowRunEvent{}).Where("run_id = ? AND event_type = ?", run.ID, "workflow_run.completed").Count(&completedEvents).Error)
	assert.Equal(t, int64(1), completedEvents)
}

func newWorkflowExecutionSeed(
	t *testing.T,
	runID, idempotencyKey string,
) (*types.WorkflowRun, *types.WorkflowRunBranch, *types.TaskPendingOp, *types.WorkflowRunEvent) {
	t.Helper()
	now := time.Now()
	branchID := "branch-" + runID
	payload, err := json.Marshal(types.WorkflowPendingNodePayload{
		BranchID: branchID, NodeID: "start", Attempt: 1,
	})
	require.NoError(t, err)
	return &types.WorkflowRun{
			ID: runID, TenantID: 1, AgentID: "agent-1", WorkflowVersion: 0, DraftRevision: 1,
			DefinitionSnapshot: types.JSON(`{"version":2}`), ConfigSnapshot: types.JSON(`{}`),
			InputPayload: types.JSON(`{"query":"hello"}`), RunMode: types.WorkflowRunModeDebug,
			IdempotencyKey: idempotencyKey, TriggerSource: types.WorkflowTriggerDebug,
			Status: types.WorkflowRunStatusRunning, StartedAt: now, CreatedAt: now,
		}, &types.WorkflowRunBranch{
			ID: branchID, RunID: runID, TenantID: 1, AgentID: "agent-1",
			BranchPath: "", CurrentNodeID: "start", Variables: types.JSON(`{"input":{"query":"hello"},"nodes":{}}`),
			Status: types.WorkflowBranchStatusPending, Version: 1, CreatedAt: now, UpdatedAt: now,
		}, &types.TaskPendingOp{
			TenantID: 1, TaskType: types.TypeWorkflowNodeExecute,
			Scope: types.TaskScopeWorkflowRun, ScopeID: runID, Op: "execute",
			DedupKey: branchID + ":start:1", Payload: payload,
		}, &types.WorkflowRunEvent{
			EventType: "workflow_run.started", Status: types.WorkflowRunStatusRunning,
			OccurredAt: now, CreatedAt: now,
		}
}

func pendingNodeID(t *testing.T, op *types.TaskPendingOp) string {
	t.Helper()
	var payload types.WorkflowPendingNodePayload
	require.NoError(t, json.Unmarshal(op.Payload, &payload))
	return payload.NodeID
}
