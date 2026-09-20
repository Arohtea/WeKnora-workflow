package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// newRoutingTestExecutor 构造一个只带出边表的最小执行器，用于单独验证路由决策。
//
// matchOutgoingEdges 不依赖运行时、模型或事件总线，只读 outgoing 与节点分支模式；
// 把这些依赖留空才能让用例聚焦在"选哪条边"这一条业务规则上。
func newRoutingTestExecutor(outgoing map[string][]types.WorkflowEdge) *workflowExecutor {
	return &workflowExecutor{outgoing: outgoing}
}

// conditionOnQuery 构造一个只看 input.query 的条件。
func conditionOnQuery(operator string, value interface{}) *types.WorkflowCondition {
	return &types.WorkflowCondition{
		Mode: types.WorkflowConditionAll,
		Items: []types.WorkflowConditionItem{
			{Variable: "input.query", Operator: operator, Value: value},
		},
	}
}

// edgeTargets 把命中结果压成目标节点 ID 列表，便于断言顺序。
func edgeTargets(edges []types.WorkflowEdge) []string {
	targets := make([]string, 0, len(edges))
	for _, edge := range edges {
		targets = append(targets, edge.Target)
	}
	return targets
}

// TestMatchOutgoingEdgesFirstMatchReturnsEarliestHit 覆盖 first_match 的取舍语义。
//
// first_match 只应该执行排序后的第一条命中边：这是"用户调整分支顺序"能生效的前提，
// 也是与 all_match 的核心差别。
func TestMatchOutgoingEdgesFirstMatchReturnsEarliestHit(t *testing.T) {
	executor := newRoutingTestExecutor(map[string][]types.WorkflowEdge{
		"fork": {
			{ID: "edge-a", Source: "fork", Target: "a", Order: 0, Condition: conditionOnQuery("is_not_empty", nil)},
			{ID: "edge-b", Source: "fork", Target: "b", Order: 1, Condition: conditionOnQuery("is_not_empty", nil)},
		},
	})
	node := types.WorkflowNode{ID: "fork", Name: "分叉", BranchMode: types.WorkflowBranchModeFirstMatch}
	variables := map[string]interface{}{"input": map[string]interface{}{"query": "提问"}}

	matched, failure := executor.matchOutgoingEdges(node, variables)
	if failure != nil {
		t.Fatalf("正常路由不应产生失败记录，got: %+v", failure)
	}
	if got := edgeTargets(matched); len(got) != 1 || got[0] != "a" {
		t.Fatalf("first_match 应只返回首个命中边 a，got %v", got)
	}
}

// TestMatchOutgoingEdgesFirstMatchFallsBackToDefault 覆盖没有条件命中时的默认边兜底。
//
// 默认边是"条件都不成立时也要有去处"的兜底设计；缺了它，节点会以
// NO_MATCHING_BRANCH 失败，用户看到的是工作流中断而不是走了 else 分支。
func TestMatchOutgoingEdgesFirstMatchFallsBackToDefault(t *testing.T) {
	executor := newRoutingTestExecutor(map[string][]types.WorkflowEdge{
		"fork": {
			{ID: "edge-a", Source: "fork", Target: "a", Order: 0, Condition: conditionOnQuery("is_empty", nil)},
			{ID: "edge-default", Source: "fork", Target: "fallback", Order: 1, IsDefault: true},
		},
	})
	node := types.WorkflowNode{ID: "fork", Name: "分叉", BranchMode: types.WorkflowBranchModeFirstMatch}
	variables := map[string]interface{}{"input": map[string]interface{}{"query": "提问"}}

	matched, failure := executor.matchOutgoingEdges(node, variables)
	if failure != nil {
		t.Fatalf("默认边兜底不应产生失败记录，got: %+v", failure)
	}
	if got := edgeTargets(matched); len(got) != 1 || got[0] != "fallback" {
		t.Fatalf("条件全不命中时应走默认边 fallback，got %v", got)
	}
}

// TestMatchOutgoingEdgesReturnsEmptyWhenNothingMatches 覆盖完全无命中的情况。
//
// 这里刻意返回空切片而不是错误：调用方（executeNode）需要据此记录
// NO_MATCHING_BRANCH，把"路由配置覆盖不到运行时数据"和"节点自身执行失败"区分开。
func TestMatchOutgoingEdgesReturnsEmptyWhenNothingMatches(t *testing.T) {
	executor := newRoutingTestExecutor(map[string][]types.WorkflowEdge{
		"fork": {
			{ID: "edge-a", Source: "fork", Target: "a", Order: 0, Condition: conditionOnQuery("is_empty", nil)},
		},
	})
	node := types.WorkflowNode{ID: "fork", Name: "分叉", BranchMode: types.WorkflowBranchModeFirstMatch}
	variables := map[string]interface{}{"input": map[string]interface{}{"query": "提问"}}

	matched, failure := executor.matchOutgoingEdges(node, variables)
	if failure != nil {
		t.Fatalf("无命中不是求值错误，不应返回失败记录，got: %+v", failure)
	}
	if len(matched) != 0 {
		t.Fatalf("无命中时应返回空集合，got %v", edgeTargets(matched))
	}
}

// TestMatchOutgoingEdgesAllMatchReturnsEveryHit 覆盖 all_match 的并行语义。
//
// all_match 是 v1 多出边图的迁移结果，必须仍然把所有命中的条件边和默认边一起返回，
// 否则老图会在升级后被静默裁剪掉分支。
func TestMatchOutgoingEdgesAllMatchReturnsEveryHit(t *testing.T) {
	executor := newRoutingTestExecutor(map[string][]types.WorkflowEdge{
		"fork": {
			{ID: "edge-a", Source: "fork", Target: "a", Order: 0, Condition: conditionOnQuery("is_not_empty", nil)},
			{ID: "edge-b", Source: "fork", Target: "b", Order: 1, Condition: conditionOnQuery("contains", "问")},
			{ID: "edge-c", Source: "fork", Target: "c", Order: 2, Condition: conditionOnQuery("is_empty", nil)},
			{ID: "edge-default", Source: "fork", Target: "fallback", Order: 3, IsDefault: true},
		},
	})
	node := types.WorkflowNode{ID: "fork", Name: "分叉", BranchMode: types.WorkflowBranchModeAllMatch}
	variables := map[string]interface{}{"input": map[string]interface{}{"query": "提问"}}

	matched, failure := executor.matchOutgoingEdges(node, variables)
	if failure != nil {
		t.Fatalf("all_match 正常路由不应产生失败记录，got: %+v", failure)
	}
	got := edgeTargets(matched)
	want := []string{"a", "b", "fallback"}
	if len(got) != len(want) {
		t.Fatalf("all_match 应返回全部命中边加默认边 %v，got %v", want, got)
	}
	for index, target := range want {
		if got[index] != target {
			t.Fatalf("all_match 结果顺序应保持出边顺序 %v，got %v", want, got)
		}
	}
}

// TestMatchOutgoingEdgesReportsConditionEvaluationFailure 覆盖路由条件求值失败。
//
// 变量在当前分支不可用（例如引用了另一条分支的节点输出）时，如果静默当作"不命中"，
// 用户只会看到分支没走；必须带上 NO_MATCHING_BRANCH 错误码上报，才能和节点失败区分。
func TestMatchOutgoingEdgesReportsConditionEvaluationFailure(t *testing.T) {
	executor := newRoutingTestExecutor(map[string][]types.WorkflowEdge{
		"fork": {
			{
				ID: "edge-a", Source: "fork", Target: "a", Order: 0,
				Condition: conditionOnQuery("contains", nil),
			},
		},
	})
	node := types.WorkflowNode{ID: "fork", Name: "分叉", BranchMode: types.WorkflowBranchModeFirstMatch}
	// 缺少 input.query，条件求值会因变量缺失而失败。
	variables := map[string]interface{}{"input": map[string]interface{}{}}

	matched, failure := executor.matchOutgoingEdges(node, variables)
	if failure == nil {
		t.Fatal("变量缺失时必须返回失败记录")
	}
	if failure.ErrorCode != types.WorkflowErrorCodeNoMatchingBranch {
		t.Fatalf("失败记录应使用 %s 错误码，got %q", types.WorkflowErrorCodeNoMatchingBranch, failure.ErrorCode)
	}
	if failure.NodeID != "fork" {
		t.Fatalf("失败记录应定位到节点 fork，got %q", failure.NodeID)
	}
	if len(matched) != 0 {
		t.Fatalf("求值失败时不应返回任何边，got %v", edgeTargets(matched))
	}
}

// TestMatchOutgoingEdgesWithoutOutgoing 确认没有出边的节点（终点）不产生失败记录。
func TestMatchOutgoingEdgesWithoutOutgoing(t *testing.T) {
	executor := newRoutingTestExecutor(map[string][]types.WorkflowEdge{})
	node := types.WorkflowNode{ID: "end", Name: "终点", Type: types.WorkflowNodeTypeEnd}

	matched, failure := executor.matchOutgoingEdges(node, map[string]interface{}{})
	if len(matched) != 0 || failure != nil {
		t.Fatalf("无出边节点应返回空集合且无失败记录，got matched=%v failure=%+v", matched, failure)
	}
}

// TestComposeWorkflowAnswerJoinsAnswersDeterministically 覆盖成功/部分成功时的答案聚合。
//
// 并行分支的完成顺序不稳定，答案必须按固定规则拼接，否则同一张图两次运行会给出
// 内容相同但排版不同的结果。
func TestComposeWorkflowAnswerJoinsAnswersDeterministically(t *testing.T) {
	result := workflowPathResult{
		successEnd: true,
		answers:    []string{"第一段答案", "  ", "第二段答案"},
	}

	got := composeWorkflowAnswer(result, types.WorkflowRunStatusSucceeded)
	want := "第一段答案\n\n第二段答案"
	if got != want {
		t.Fatalf("成功状态答案聚合结果应为 %q，got %q", want, got)
	}
	// 重复调用必须给出完全相同的文本。
	if again := composeWorkflowAnswer(result, types.WorkflowRunStatusSucceeded); again != got {
		t.Fatalf("聚合不确定：%q vs %q", got, again)
	}
}

// TestComposeWorkflowAnswerReportsPartialFailures 覆盖 partial 状态的答案装配。
//
// partial 只给答案会让用户以为工作流完整跑通，只给失败又丢掉了已经算出来的内容，
// 因此必须两者都下发。
func TestComposeWorkflowAnswerReportsPartialFailures(t *testing.T) {
	result := workflowPathResult{
		successEnd: true,
		answers:    []string{"可用的答案"},
		failures: []types.WorkflowNodeFailure{
			{NodeID: "http-1", NodeName: "调用接口", ErrorCode: "HTTP_ERROR", Error: "请求超时"},
		},
	}

	got := composeWorkflowAnswer(result, types.WorkflowRunStatusPartial)
	if !strings.Contains(got, "可用的答案") {
		t.Fatalf("partial 必须保留成功分支的答案，got %q", got)
	}
	if !strings.Contains(got, "调用接口") || !strings.Contains(got, "请求超时") {
		t.Fatalf("partial 必须附带结构化失败说明，got %q", got)
	}
}

// TestComposeWorkflowAnswerOnFailureAndCancel 覆盖失败与取消状态的答案。
//
// 取消后不能再补一段答案：用户已经点了停止，追加内容会覆盖新一轮对话。
func TestComposeWorkflowAnswerOnFailureAndCancel(t *testing.T) {
	failed := workflowPathResult{
		failures: []types.WorkflowNodeFailure{
			{NodeID: "http-1", NodeName: "调用接口", Error: "请求超时"},
		},
	}
	if got := composeWorkflowAnswer(failed, types.WorkflowRunStatusFailed); !strings.Contains(got, "请求超时") {
		t.Fatalf("失败状态应把失败原因告诉用户，got %q", got)
	}
	withoutFailure := workflowPathResult{}
	if got := composeWorkflowAnswer(withoutFailure, types.WorkflowRunStatusFailed); got == "" {
		t.Fatal("无失败详情时也应给出可展示的失败提示")
	}
	withAnswer := workflowPathResult{successEnd: true, answers: []string{"不该出现"}}
	if got := composeWorkflowAnswer(withAnswer, types.WorkflowRunStatusCanceled); got != "" {
		t.Fatalf("取消状态不应返回任何答案，got %q", got)
	}
	// 成功但没有任何答案文本时也要给一句收尾语，避免前端渲染出空气泡。
	if got := composeWorkflowAnswer(workflowPathResult{}, types.WorkflowRunStatusSucceeded); strings.TrimSpace(got) == "" {
		t.Fatal("成功状态且无答案文本时必须给出兜底文案")
	}
}

// TestDedupeWorkflowFailuresKeepsDeterministicOrder 覆盖失败列表去重与排序。
//
// 同一节点在并行分支上可能被重复上报（同一失败既来自观察器又来自分支合并），
// 不去重会让错误摘要重复；不排序则会让列表顺序随 goroutine 调度变化。
func TestDedupeWorkflowFailuresKeepsDeterministicOrder(t *testing.T) {
	failures := []types.WorkflowNodeFailure{
		{NodeID: "b-node", BranchPath: "start/b", Error: "boom"},
		{NodeID: "a-node", BranchPath: "start/a", Error: "boom"},
		{NodeID: "b-node", BranchPath: "start/b", Error: "boom"},
		{NodeID: "a-node", BranchPath: "start/a", Error: "另一个错误"},
		{NodeID: "empty-error", BranchPath: "start/a", Error: "   "},
	}

	got := dedupeWorkflowFailures(failures)
	if len(got) != 3 {
		t.Fatalf("应去重并丢弃空错误描述后剩 3 条，got %d: %+v", len(got), got)
	}
	order := make([]string, 0, len(got))
	for _, failure := range got {
		order = append(order, failure.BranchPath+"/"+failure.NodeID)
	}
	want := []string{"start/a/a-node", "start/a/a-node", "start/b/b-node"}
	if order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Fatalf("结果应按分支路径与节点 ID 稳定排序，got %v", order)
	}

	// 同一分支下不同失败的相对顺序由稳定排序保留（即入参顺序），但跨分支的排序
	// 必须一致：无论入参怎么写，start/a 的失败都排在 start/b 之前。
	reversed := []types.WorkflowNodeFailure{failures[3], failures[2], failures[1], failures[0]}
	reversedResult := dedupeWorkflowFailures(reversed)
	if len(reversedResult) != len(got) {
		t.Fatalf("倒序输入应得到相同条数，got %d vs %d", len(reversedResult), len(got))
	}
	for index, failure := range reversedResult {
		if index < 2 && failure.BranchPath != "start/a" {
			t.Fatalf("倒序输入下第 %d 条仍应属于 start/a 分支，got %+v", index, failure)
		}
		if index == 2 && failure.BranchPath != "start/b" {
			t.Fatalf("倒序输入下最后一条应属于 start/b 分支，got %+v", failure)
		}
	}
}

// TestWorkflowFailureMessageFormatsFailures 覆盖失败摘要文本。
func TestWorkflowFailureMessageFormatsFailures(t *testing.T) {
	if got := workflowFailureMessage(nil); got != "" {
		t.Fatalf("无失败时应返回空串，got %q", got)
	}
	got := workflowFailureMessage([]types.WorkflowNodeFailure{
		{NodeID: "http-1", NodeName: "调用接口", ErrorCode: "HTTP_ERROR", Error: "请求超时"},
		{NodeID: "llm-1", Error: "模型返回为空"},
	})
	if !strings.Contains(got, "[HTTP_ERROR] 调用接口：请求超时") {
		t.Fatalf("带错误码的失败应渲染成 [CODE] 名称：原因，got %q", got)
	}
	// 节点没有名称时退回用 ID 定位，避免输出一行没有主体的文本。
	if !strings.Contains(got, "llm-1：模型返回为空") {
		t.Fatalf("无节点名时应使用节点 ID，got %q", got)
	}
	if lines := strings.Split(got, "\n"); len(lines) != 2 {
		t.Fatalf("每条失败占一行，got %d 行：%q", len(lines), got)
	}
}

// TestSanitizeWorkflowSummaryMasksSecrets 覆盖摘要脱敏。
//
// 摘要会落库并可被翻页浏览，HTTP 请求头/MCP 配置里的凭据一旦写入就等同于泄漏。
func TestSanitizeWorkflowSummaryMasksSecrets(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		hidden    []string
		retained  []string
		truncated bool
	}{
		{
			name:     "JSON 请求头里的 Authorization",
			input:    `{"url":"https://example.com","headers":{"Authorization":"Bearer secret-token-123"}}`,
			hidden:   []string{"secret-token-123"},
			retained: []string{"https://example.com"},
		},
		{
			name:     "下划线风格的 api_key",
			input:    "api_key=live-abcdef 调用完成",
			hidden:   []string{"live-abcdef"},
			retained: []string{"调用完成"},
		},
		{
			name:      "超长摘要同时被脱敏与截断",
			input:     `{"token":"t0ken-value"}` + strings.Repeat("x", types.WorkflowLifecycleSummaryLimit),
			hidden:    []string{"t0ken-value"},
			truncated: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, truncated := sanitizeWorkflowSummary(testCase.input)
			for _, secret := range testCase.hidden {
				if strings.Contains(got, secret) {
					t.Fatalf("凭据未脱敏：%q", got)
				}
			}
			for _, text := range testCase.retained {
				if !strings.Contains(got, text) {
					t.Fatalf("非敏感内容不应被改写，期望包含 %q，got %q", text, got)
				}
			}
			if truncated != testCase.truncated {
				t.Fatalf("截断标记应为 %v，got %v", testCase.truncated, truncated)
			}
		})
	}

	if got, truncated := sanitizeWorkflowSummary("   "); got != "" || truncated {
		t.Fatalf("空白摘要应归一化为空串，got %q truncated=%v", got, truncated)
	}
}

// TestJoinWorkflowBranchPath 覆盖分支路径拼接。
//
// 路径是并行分支之间唯一的区分标识：节点记录、失败去重和前端轨迹都靠它区分
// "同一个节点在不同分支上的两次执行"。
func TestJoinWorkflowBranchPath(t *testing.T) {
	if got := joinWorkflowBranchPath("", "start"); got != "start" {
		t.Fatalf("根节点路径应为节点 ID，got %q", got)
	}
	if got := joinWorkflowBranchPath("start", "fork"); got != "start/fork" {
		t.Fatalf("子节点路径应拼接父路径，got %q", got)
	}
	if got := joinWorkflowBranchPath("start/fork", "end"); got != "start/fork/end" {
		t.Fatalf("多级路径应逐级拼接，got %q", got)
	}
}

// TestWorkflowRunObserverRecordsVersionAndNodes 覆盖发布版本绑定与节点记录。
//
// 运行记录必须钉在不可变的发布版本上，否则"这次运行跑的是哪一版图"就无法回答；
// 同时节点记录要写进 store，轨迹抽屉才有数据。
func TestWorkflowRunObserverRecordsVersionAndNodes(t *testing.T) {
	store := &fakeWorkflowRunStore{
		published: &types.WorkflowVersionRecord{TenantID: 1, AgentID: "agent-1", Version: 7},
	}
	definition := &types.WorkflowDefinition{
		Version: types.WorkflowVersion,
		Nodes:   []types.WorkflowNode{{ID: "start", Type: types.WorkflowNodeTypeStart, Name: "开始"}},
	}
	observer := newWorkflowRunObserver(
		context.Background(),
		event.NewEventBus(),
		store,
		store.published,
		definition,
		&types.AgentConfig{},
		1,
		"agent-1",
		types.WorkflowTriggerChat,
		"session-1",
		"message-1",
		"用户提问",
	)

	if store.createdRun == nil {
		t.Fatal("运行起始记录必须写入 store")
	}
	if store.createdRun.WorkflowVersion != 7 {
		t.Fatalf("运行记录应绑定当前发布版本 7，got %d", store.createdRun.WorkflowVersion)
	}
	if store.createdRun.Status != types.WorkflowRunStatusRunning {
		t.Fatalf("起始状态应为 running，got %q", store.createdRun.Status)
	}
	if store.createdRun.TenantID != 1 || store.createdRun.AgentID != "agent-1" {
		t.Fatalf("运行记录租户/智能体归属错误：%+v", store.createdRun)
	}

	node := definition.Nodes[0]
	record := observer.nodeStarted(node, "start", "节点输入摘要")
	// 运行起始事件已经占用了 sequence=1，节点序号必须继续单调递增，
	// 否则前端按序号重建时间线时会把节点事件排到运行开始之前。
	if record == nil || record.Sequence != 2 {
		t.Fatalf("节点记录应带递增的 sequence（运行起始事件已占用 1），got %+v", record)
	}
	if len(store.createdNodes) != 1 || store.createdNodes[0].NodeID != "start" {
		t.Fatalf("节点记录必须写入 store，got %+v", store.createdNodes)
	}

	observer.nodeFinished(record, types.WorkflowNodeStatusSucceeded, "节点输出", types.TokenUsage{}, "", "")
	if len(store.updatedNodes) != 1 || store.updatedNodes[0].Status != types.WorkflowNodeStatusSucceeded {
		t.Fatalf("节点终态必须写回 store，got %+v", store.updatedNodes)
	}
	if store.updatedNodes[0].FinishedAt == nil {
		t.Fatal("节点终态记录必须带结束时间")
	}

	failures := []types.WorkflowNodeFailure{
		{NodeID: "http-1", NodeName: "调用接口", ErrorCode: "HTTP_ERROR", Error: "请求超时"},
	}
	observer.finish(types.WorkflowRunStatusFailed, "", types.TokenUsage{}, failures)
	if store.updatedRuns != 1 {
		t.Fatalf("运行终态必须写回 store，got %d 次", store.updatedRuns)
	}
	if store.updatedRun == nil || store.updatedRun.Status != types.WorkflowRunStatusFailed {
		t.Fatalf("运行终态应为 failed，got %+v", store.updatedRun)
	}
	if store.updatedRun.ErrorCode != "HTTP_ERROR" {
		t.Fatalf("运行错误码应取自首条失败，got %q", store.updatedRun.ErrorCode)
	}
	if store.updatedRun.FinishedAt == nil {
		t.Fatal("运行终态必须带结束时间")
	}
}

// TestWorkflowRunObserverWithoutStoreStillEmitsStarted 覆盖无存储时的事件行为。
//
// Debug Run 可以只使用事件总线而不落库；即使 store 为空，编辑器仍必须收到 started
// 事件，否则前端会一直停在“尚未开始”。
func TestWorkflowRunObserverWithoutStoreStillEmitsStarted(t *testing.T) {
	bus := event.NewEventBus()
	var started int
	bus.On(event.EventWorkflowRunStarted, func(context.Context, event.Event) error {
		started++
		return nil
	})
	observer := newWorkflowRunObserver(
		context.Background(),
		bus,
		nil,
		&types.WorkflowVersionRecord{Version: 3},
		&types.WorkflowDefinition{Version: types.WorkflowVersion},
		&types.AgentConfig{},
		1,
		"agent-1",
		types.WorkflowTriggerChat,
		"session-1",
		"message-1",
		"用户提问",
	)

	if observer == nil || observer.run == nil {
		t.Fatal("无存储时仍应创建运行观测上下文")
	}
	if observer.run.WorkflowVersion != 3 {
		t.Fatalf("显式发布版本应写入运行记录，got %d", observer.run.WorkflowVersion)
	}
	if started != 1 {
		t.Fatalf("无存储时仍应发送 started 事件，got %d", started)
	}
}

// fakeWorkflowRunStore 是 workflowRunStore 的内存替身，仅记录调用顺序与内容。
//
// 观测链路的关键行为（版本绑定、节点记录、终态回写）都能在这里断言，不必依赖数据库；
// 落库语义本身由 repository 层的 SQLite 用例覆盖。
type fakeWorkflowRunStore struct {
	published *types.WorkflowVersionRecord

	createdRun  *types.WorkflowRun
	updatedRun  *types.WorkflowRun
	updatedRuns int

	createdNodes  []*types.WorkflowRunNode
	updatedNodes  []*types.WorkflowRunNode
	createdEvents []*types.WorkflowRunEvent
}

func (f *fakeWorkflowRunStore) GetPublishedWorkflow(
	context.Context, string,
) (*types.WorkflowVersionRecord, error) {
	if f.published == nil {
		return nil, repository.ErrWorkflowVersionNotFound
	}
	return f.published, nil
}

func (f *fakeWorkflowRunStore) CreateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	f.createdRun = run
	return nil
}

func (f *fakeWorkflowRunStore) UpdateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	f.updatedRun = run
	f.updatedRuns++
	return nil
}

func (f *fakeWorkflowRunStore) CreateWorkflowRunNode(_ context.Context, node *types.WorkflowRunNode) error {
	// 模拟自增主键回填，终态更新依赖这个 ID。
	node.ID = int64(len(f.createdNodes) + 1)
	f.createdNodes = append(f.createdNodes, node)
	return nil
}

func (f *fakeWorkflowRunStore) UpdateWorkflowRunNode(_ context.Context, node *types.WorkflowRunNode) error {
	f.updatedNodes = append(f.updatedNodes, node)
	return nil
}

func (f *fakeWorkflowRunStore) CreateWorkflowRunEvent(_ context.Context, runEvent *types.WorkflowRunEvent) error {
	f.createdEvents = append(f.createdEvents, runEvent)
	return nil
}
