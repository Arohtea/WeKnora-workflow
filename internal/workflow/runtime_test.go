package workflow

import (
	"testing"

	appTypes "github.com/Tencent/WeKnora/internal/types"
)

// allTrueCondition 返回一个对任何分支变量表都成立的条件，用于构造"多个候选分支"的图。
func allTrueCondition() *appTypes.WorkflowCondition {
	return &appTypes.WorkflowCondition{
		Mode: appTypes.WorkflowConditionAll,
		Items: []appTypes.WorkflowConditionItem{
			{Variable: "input.query", Operator: "is_not_empty"},
		},
	}
}

// edgeIDs 把边集合压成 ID 列表，仅用于失败信息。
func edgeIDs(edges []appTypes.WorkflowEdge) []string {
	ids := make([]string, 0, len(edges))
	for _, edge := range edges {
		ids = append(ids, edge.ID)
	}
	return ids
}

// inputQueryVariables 构造只含提问的最小变量表。
func inputQueryVariables(query string) map[string]interface{} {
	return map[string]interface{}{
		"input": map[string]interface{}{"query": query, "attachments_text": ""},
		"nodes": map[string]interface{}{},
	}
}

// TestMigrateDefinitionAssignsBranchModeByOutgoingCount 覆盖 v1 -> v2 的分支模式迁移。
//
// v1 没有分支模式，历史执行器会跑所有命中的条件边。迁移成 first_match 会改变这些
// 图的语义，因此必须按出边数量保守决策：多出边保持"全部命中"语义（all_match），
// 单出边取首个命中（first_match）。
func TestMigrateDefinitionAssignsBranchModeByOutgoingCount(t *testing.T) {
	definition := &appTypes.WorkflowDefinition{
		Version: 1,
		Nodes: []appTypes.WorkflowNode{
			{ID: "start", Type: appTypes.WorkflowNodeTypeStart, Name: "开始"},
			{ID: "fork", Type: appTypes.WorkflowNodeTypeLLM, Name: "分叉"},
			{ID: "a", Type: appTypes.WorkflowNodeTypeEnd, Name: "终点A"},
			{ID: "b", Type: appTypes.WorkflowNodeTypeEnd, Name: "终点B"},
			{ID: "tail", Type: appTypes.WorkflowNodeTypeEnd, Name: "单出边终点"},
		},
		Edges: []appTypes.WorkflowEdge{
			{ID: "start-fork", Source: "start", Target: "fork"},
			{ID: "fork-a", Source: "fork", Target: "a", Condition: allTrueCondition()},
			{ID: "fork-b", Source: "fork", Target: "b", Condition: allTrueCondition()},
			{ID: "tail-in", Source: "a", Target: "tail"},
		},
	}

	if err := MigrateDefinition(definition); err != nil {
		t.Fatalf("v1 定义应可迁移，got error: %v", err)
	}
	if definition.Version != appTypes.WorkflowVersion || definition.SchemaVersion != appTypes.WorkflowVersion {
		t.Fatalf("迁移后版本应提升到 %d，got version=%d schema_version=%d",
			appTypes.WorkflowVersion, definition.Version, definition.SchemaVersion)
	}

	modes := make(map[string]string, len(definition.Nodes))
	for _, node := range definition.Nodes {
		modes[node.ID] = node.BranchMode
	}
	if modes["fork"] != appTypes.WorkflowBranchModeAllMatch {
		t.Fatalf("多出边节点的分支模式应为 all_match，got %q", modes["fork"])
	}
	for _, nodeID := range []string{"start", "a", "tail"} {
		if modes[nodeID] != appTypes.WorkflowBranchModeFirstMatch {
			t.Fatalf("节点 %s 的分支模式应为 first_match，got %q", nodeID, modes[nodeID])
		}
	}
}

// TestMigrateDefinitionAssignsStableOrderForV1 覆盖 v1 全零 order 的补齐。
//
// v1 序列化出来的 order 全是 0；如果保留这个值，first_match 的取舍将退化成按
// 边 ID 比较，而边 ID 是随机 UUID，用户看到的顺序每次都会变。因此必须按出边
// 在切片中的位置分配序号，且序号只在自己的源节点内递增。
func TestMigrateDefinitionAssignsStableOrderForV1(t *testing.T) {
	definition := &appTypes.WorkflowDefinition{
		Version: 1,
		Nodes: []appTypes.WorkflowNode{
			{ID: "s1", Name: "起点1"},
			{ID: "s2", Name: "起点2"},
			{ID: "t1", Name: "终点1"},
			{ID: "t2", Name: "终点2"},
			{ID: "t3", Name: "终点3"},
		},
		Edges: []appTypes.WorkflowEdge{
			{ID: "e-s1-t1", Source: "s1", Target: "t1"},
			{ID: "e-s2-t2", Source: "s2", Target: "t2"},
			{ID: "e-s1-t2", Source: "s1", Target: "t2"},
			{ID: "e-s2-t3", Source: "s2", Target: "t3"},
		},
	}

	if err := MigrateDefinition(definition); err != nil {
		t.Fatalf("v1 定义应可迁移，got error: %v", err)
	}
	orders := make(map[string]int, len(definition.Edges))
	for _, edge := range definition.Edges {
		orders[edge.ID] = edge.Order
	}
	want := map[string]int{
		"e-s1-t1": 0, "e-s2-t2": 0, "e-s1-t2": 1, "e-s2-t3": 1,
	}
	for edgeID, wantOrder := range want {
		if orders[edgeID] != wantOrder {
			t.Fatalf("边 %s 的 order 应为 %d（按同源切片顺序），got %d", edgeID, wantOrder, orders[edgeID])
		}
	}
}

// TestMigrateDefinitionAssignsOrderForNegativeV2Order 覆盖 v2 中未赋值的 order。
//
// 编辑器/导入链路在 order 缺失时可能落成负数（字段序列化前的哨兵值）。迁移必须
// 把负数按切片顺序补齐成 0..n-1，否则该边会在校验期被当作非法 order 拒绝，
// 或者让稳定排序退化为按随机边 ID 排序。
//
// WARN: 全零 order 在 v2 中不会被迁移补齐——MigrateDefinition 只处理 version==1
// 或 order<0 的边（validation.go:183）。见报告中的说明。
func TestMigrateDefinitionAssignsOrderForNegativeV2Order(t *testing.T) {
	definition := &appTypes.WorkflowDefinition{
		Version: appTypes.WorkflowVersion,
		Nodes: []appTypes.WorkflowNode{
			{ID: "fork", Name: "分叉", BranchMode: appTypes.WorkflowBranchModeFirstMatch},
			{ID: "a", Name: "终点A"},
			{ID: "b", Name: "终点B"},
		},
		Edges: []appTypes.WorkflowEdge{
			{ID: "edge-zzz", Source: "fork", Target: "b", Order: -1},
			{ID: "edge-aaa", Source: "fork", Target: "a", Order: -1},
		},
	}

	if err := MigrateDefinition(definition); err != nil {
		t.Fatalf("v2 定义应可迁移，got error: %v", err)
	}
	if definition.Edges[0].Order != 0 || definition.Edges[1].Order != 1 {
		t.Fatalf("负数 order 应按切片顺序补齐，got %d, %d",
			definition.Edges[0].Order, definition.Edges[1].Order)
	}
}

// TestMigrateDefinitionKeepsExplicitV2Order 确认显式 order 不被改写。
//
// 用户在画布上调整过分支顺序后，保存下来的 order 就是执行顺序的唯一依据；
// 迁移如果覆盖它，等于每次读取都重置用户配置。
func TestMigrateDefinitionKeepsExplicitV2Order(t *testing.T) {
	definition := &appTypes.WorkflowDefinition{
		Version:       appTypes.WorkflowVersion,
		SchemaVersion: appTypes.WorkflowVersion,
		Nodes: []appTypes.WorkflowNode{
			{ID: "fork", Name: "分叉", BranchMode: appTypes.WorkflowBranchModeAllMatch},
			{ID: "a", Name: "终点A"},
			{ID: "b", Name: "终点B"},
		},
		Edges: []appTypes.WorkflowEdge{
			{ID: "edge-first", Source: "fork", Target: "a", Order: 5},
			{ID: "edge-second", Source: "fork", Target: "b", Order: 9},
		},
	}

	if err := MigrateDefinition(definition); err != nil {
		t.Fatalf("v2 定义应可迁移，got error: %v", err)
	}
	if definition.Edges[0].Order != 5 || definition.Edges[1].Order != 9 {
		t.Fatalf("显式 order 不应被改写，got %d, %d", definition.Edges[0].Order, definition.Edges[1].Order)
	}
	if definition.Nodes[0].BranchMode != appTypes.WorkflowBranchModeAllMatch {
		t.Fatalf("已显式设置的分支模式不应被改写，got %q", definition.Nodes[0].BranchMode)
	}
}

// TestMigrateDefinitionRejectsUnknownVersionAndNil 覆盖迁移的失败路径。
func TestMigrateDefinitionRejectsUnknownVersionAndNil(t *testing.T) {
	if err := MigrateDefinition(nil); err == nil {
		t.Fatal("空定义必须报错")
	}
	for _, unsupported := range []int{3, 99} {
		definition := &appTypes.WorkflowDefinition{Version: unsupported}
		if err := MigrateDefinition(definition); err == nil {
			t.Fatalf("版本 %d 不在支持范围内，必须报错", unsupported)
		}
	}
	// Version 与 SchemaVersion 都为空时，历史定义被当作 v1 兼容处理而不是报错。
	legacy := &appTypes.WorkflowDefinition{
		Nodes: []appTypes.WorkflowNode{{ID: "only", Name: "唯一节点"}},
	}
	if err := MigrateDefinition(legacy); err != nil {
		t.Fatalf("无版本号的历史定义应按 v1 兼容迁移，got error: %v", err)
	}
	if legacy.Nodes[0].BranchMode != appTypes.WorkflowBranchModeFirstMatch {
		t.Fatalf("无出边的节点也应补齐分支模式，got %q", legacy.Nodes[0].BranchMode)
	}
}

// TestSortedOutgoingEdgesOrdersByOrderThenID 覆盖出边稳定排序。
//
// matchOutgoingEdges 直接消费这个顺序来决定 first_match 命中哪条边，排序不稳定
// 会让同一个图的执行路径在两次运行间不同。
func TestSortedOutgoingEdgesOrdersByOrderThenID(t *testing.T) {
	edges := []appTypes.WorkflowEdge{
		{ID: "edge-c", Order: 2},
		{ID: "edge-b", Order: 1},
		{ID: "edge-a", Order: 1},
		{ID: "edge-d", Order: 1},
		{ID: "edge-zero", Order: 0},
	}

	got := SortedOutgoingEdges(edges)
	wantIDs := []string{"edge-zero", "edge-a", "edge-b", "edge-d", "edge-c"}
	if len(got) != len(wantIDs) {
		t.Fatalf("排序后数量变化：%d -> %d", len(wantIDs), len(got))
	}
	for index, wantID := range wantIDs {
		if got[index].ID != wantID {
			t.Fatalf("第 %d 条边应为 %s，got %s（完整结果 %v）", index, wantID, got[index].ID, edgeIDs(got))
		}
	}
}

// TestSortedOutgoingEdgesDoesNotMutateInput 确认排序返回副本。
//
// executor 构建 outgoing 映射时直接对切片排序，如果原地改写，同一张图在不同
// 请求间会共享被改过的顺序。
func TestSortedOutgoingEdgesDoesNotMutateInput(t *testing.T) {
	edges := []appTypes.WorkflowEdge{
		{ID: "edge-b", Order: 1},
		{ID: "edge-a", Order: 0},
	}

	_ = SortedOutgoingEdges(edges)

	if edges[0].ID != "edge-b" || edges[1].ID != "edge-a" {
		t.Fatalf("输入切片被原地修改：%v", edgeIDs(edges))
	}
}

// TestEvaluateConditionAllAndAnyModes 覆盖条件求值的两种模式。
func TestEvaluateConditionAllAndAnyModes(t *testing.T) {
	variables := map[string]interface{}{
		"input": map[string]interface{}{"query": "今天天气怎么样", "attachments_text": ""},
		"nodes": map[string]interface{}{
			"find": appTypes.WorkflowNodeOutput{Text: "检索到三条结果", Status: "success"},
		},
	}

	allMatched := &appTypes.WorkflowCondition{
		Mode: appTypes.WorkflowConditionAll,
		Items: []appTypes.WorkflowConditionItem{
			{Variable: "input.query", Operator: "contains", Value: "天气"},
			{Variable: "nodes.find.status", Operator: "eq", Value: "success"},
		},
	}
	ok, err := EvaluateCondition(allMatched, variables)
	if err != nil {
		t.Fatalf("all 条件求值不应报错，got: %v", err)
	}
	if !ok {
		t.Fatal("全部条件成立时 all 模式应返回 true")
	}

	allWithOneFalse := &appTypes.WorkflowCondition{
		Mode: appTypes.WorkflowConditionAll,
		Items: []appTypes.WorkflowConditionItem{
			{Variable: "input.query", Operator: "contains", Value: "天气"},
			{Variable: "nodes.find.status", Operator: "eq", Value: "failed"},
		},
	}
	if ok, err = EvaluateCondition(allWithOneFalse, variables); err != nil || ok {
		t.Fatalf("all 模式有一个条件不成立应返回 false，got ok=%v err=%v", ok, err)
	}

	anyMatched := &appTypes.WorkflowCondition{
		Mode: appTypes.WorkflowConditionAny,
		Items: []appTypes.WorkflowConditionItem{
			{Variable: "input.query", Operator: "is_empty"},
			{Variable: "nodes.find.text", Operator: "contains", Value: "三条"},
		},
	}
	if ok, err = EvaluateCondition(anyMatched, variables); err != nil || !ok {
		t.Fatalf("any 模式有任一条件成立应返回 true，got ok=%v err=%v", ok, err)
	}
}

// TestEvaluateConditionReportsMissingVariable 确认变量缺失返回错误而不是静默取假。
//
// 静默返回 false 会让用户看到"分支没走"，却完全不知道是变量引用写错了。
func TestEvaluateConditionReportsMissingVariable(t *testing.T) {
	variables := inputQueryVariables("提问")

	missing := &appTypes.WorkflowCondition{
		Mode: appTypes.WorkflowConditionAll,
		Items: []appTypes.WorkflowConditionItem{
			{Variable: "nodes.absent.text", Operator: "is_not_empty"},
		},
	}
	ok, err := EvaluateCondition(missing, variables)
	if err == nil {
		t.Fatal("变量在当前分支不可用时必须返回错误")
	}
	if ok {
		t.Fatal("求值失败时不应返回 true")
	}

	// 未被任何节点写入的分支（走另一条分支的节点）属于正常情况，同样必须报错，
	// 而不是当作空值继续往下跑。
	unknownNode := &appTypes.WorkflowCondition{
		Mode: appTypes.WorkflowConditionAll,
		Items: []appTypes.WorkflowConditionItem{
			{Variable: "nodes.other-branch.status", Operator: "eq", Value: "success"},
		},
	}
	if _, err := EvaluateCondition(unknownNode, variables); err == nil {
		t.Fatal("引用其他分支节点的变量时必须返回错误")
	}

	// 空条件组视为恒真，这是"无条件边"的语义，不应报错。
	if ok, err := EvaluateCondition(nil, variables); err != nil || !ok {
		t.Fatalf("空条件应恒真，got ok=%v err=%v", ok, err)
	}
}

// TestNormalizeDraftConfigAllowsIncompleteTypedFields 确认合法 JSON 的中间草稿可保存。
//
// 节点表单编辑过程中，knowledge_base_ids 可能暂时还是字符串而不是数组。该值
// 发布时必须被严格校验，但草稿保存不能因为强类型资源收集失败而丢掉用户输入。
func TestNormalizeDraftConfigAllowsIncompleteTypedFields(t *testing.T) {
	config := &appTypes.CustomAgentConfig{
		AgentType: appTypes.AgentTypeWorkflow,
		Workflow: &appTypes.WorkflowDefinition{
			Version: 1,
			Nodes: []appTypes.WorkflowNode{
				{
					ID:     "retrieval",
					Name:   "检索",
					Type:   appTypes.WorkflowNodeTypeRetrieval,
					Config: []byte(`{"knowledge_base_ids":"正在编辑"}`),
				},
			},
		},
	}

	if err := NormalizeDraftConfig(config); err != nil {
		t.Fatalf("合法 JSON 的未完成节点配置应允许保存，got error: %v", err)
	}
	if len(config.KnowledgeBases) != 0 {
		t.Fatalf("无法确认类型的资源引用应被忽略，got %v", config.KnowledgeBases)
	}
	if err := ValidatePublishedConfig(config); err == nil {
		t.Fatal("同一未完成配置在发布校验中必须失败")
	}
}
