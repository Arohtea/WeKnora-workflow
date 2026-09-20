package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"cel.dev/cel-go/cel"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	appTypes "github.com/Tencent/WeKnora/internal/types"
	"github.com/dominikbraun/graph"
)

var (
	workflowNodeErrorRE = regexp.MustCompile(`workflow (?:node|节点) ([A-Za-z0-9_-]+)`)
	workflowEdgeErrorRE = regexp.MustCompile(`workflow edge ([A-Za-z0-9_-]+)`)
)

func validationIssueCode(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unreachable") || strings.Contains(message, "不可达"):
		return "UNREACHABLE_NODE"
	case strings.Contains(message, "acyclic") || strings.Contains(message, "cycle"):
		return "CYCLE"
	case strings.Contains(message, "condition") || strings.Contains(message, "路由"):
		return "INVALID_CONDITION"
	case strings.Contains(message, "template") || strings.Contains(message, "引用"):
		return "INVALID_REFERENCE"
	case strings.Contains(message, "branch mode") || strings.Contains(message, "分支模式"):
		return "INVALID_BRANCH_MODE"
	default:
		return "INVALID_WORKFLOW"
	}
}

const (
	// MaxNodes 限制单个工作流的节点数量。
	MaxNodes = 50
	// MaxEdges 限制单个工作流的连线数量。
	MaxEdges = 100
	// MaxParallelNodes 是运行时允许同时执行的节点上限。
	MaxParallelNodes = 8
)

var (
	workflowIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	inputVariableRE   = regexp.MustCompile(`^input\.(query|attachments_text)$`)
	nodeVariableRE    = regexp.MustCompile(`^nodes\.([A-Za-z0-9_-]{1,80})\.(text|status|data(?:\.[A-Za-z0-9_-]+)*)$`)
)

// BuiltinToolAllowlist 是工作流 v1 可直接调用的只读内置工具集合。
var BuiltinToolAllowlist = map[string]struct{}{
	tools.ToolKnowledgeSearch:     {},
	tools.ToolGrepChunks:          {},
	tools.ToolListKnowledgeChunks: {},
	tools.ToolQueryKnowledgeGraph: {},
	tools.ToolGetDocumentInfo:     {},
	tools.ToolWikiSearch:          {},
	tools.ToolWikiReadPage:        {},
	tools.ToolWikiReadSourceDoc:   {},
	tools.ToolWikiReadIssue:       {},
	tools.ToolWebSearch:           {},
	tools.ToolWebFetch:            {},
	tools.ToolSearchConversations: {},
	tools.ToolSearchMemory:        {},
	tools.ToolDataSchema:          {},
}

var conditionOperators = map[string]struct{}{
	"eq":           {},
	"neq":          {},
	"gt":           {},
	"gte":          {},
	"lt":           {},
	"lte":          {},
	"contains":     {},
	"not_contains": {},
	"starts_with":  {},
	"ends_with":    {},
	"in":           {},
	"not_in":       {},
	"is_empty":     {},
	"is_not_empty": {},
}

var blockedHTTPHeaders = map[string]struct{}{
	"Authorization":       {},
	"Cookie":              {},
	"Proxy-Authorization": {},
	"X-Api-Key":           {},
	"Api-Key":             {},
	"X-Auth-Token":        {},
	"X-Access-Token":      {},
	"Access-Token":        {},
}

// NormalizeDraftConfig 把工作流草稿迁移到当前结构，并校验节点配置仍可被解析。
//
// 草稿允许暂时缺少连线、必填配置或可用资源，因此这里不检查 DAG、可达性、
// 变量作用域和资源可用性。唯一会阻止保存的是执行器无法再读回的结构性问题，
// 例如不支持的 schema 版本或损坏的节点 JSON。
//
// @param config 待归一化的智能体配置。
// @returns 草稿结构无法迁移或解析时返回错误。
func NormalizeDraftConfig(config *appTypes.CustomAgentConfig) error {
	if config == nil || config.AgentType != appTypes.AgentTypeWorkflow {
		return nil
	}
	config.AgentMode = appTypes.AgentModeSmartReasoning
	if config.Workflow == nil {
		config.Workflow = appTypes.DefaultWorkflowDefinition()
	}
	if err := MigrateDefinition(config.Workflow); err != nil {
		return err
	}
	if config.Workflow.Viewport.Zoom == 0 {
		config.Workflow.Viewport.Zoom = 1
	}
	for i := range config.Workflow.Nodes {
		if len(config.Workflow.Nodes[i].Config) == 0 {
			config.Workflow.Nodes[i].Config = json.RawMessage(`{}`)
		}
		if !json.Valid(config.Workflow.Nodes[i].Config) {
			return fmt.Errorf("workflow node %s has invalid config JSON", config.Workflow.Nodes[i].ID)
		}
	}
	// 草稿编辑过程中，节点表单可能只填了一半。此时 RawMessage 仍是合法
	// JSON，但强类型解码会因为临时的字段类型不匹配而失败。顶层资源字段只是
	// 运行时派生缓存，保存阶段按可识别字段尽力收集即可；发布/试跑会继续通过
	// CollectResources 和 Validate 做严格检查。
	refs := collectDraftResources(config.Workflow)

	config.AllowedTools = refs.BuiltinTools
	config.KnowledgeBases = refs.KnowledgeBaseIDs
	config.MCPServices = refs.MCPServiceIDs
	config.SelectedSkills = refs.SkillNames
	config.WebSearchEnabled = containsString(refs.BuiltinTools, tools.ToolWebSearch) ||
		containsString(refs.BuiltinTools, tools.ToolWebFetch)
	config.KBSelectionMode = selectionMode(refs.KnowledgeBaseIDs)
	config.MCPSelectionMode = selectionMode(refs.MCPServiceIDs)
	config.SkillsSelectionMode = selectionMode(refs.SkillNames)
	return nil
}

// collectDraftResources 从合法 JSON 草稿中尽力提取资源引用。
//
// 该函数不得把节点字段类型错误升级为保存失败：编辑器需要能保存尚未完成的
// 中间状态。无法识别的字段会被忽略，原始节点 Config 不会被修改。
func collectDraftResources(definition *appTypes.WorkflowDefinition) *ResourceReferences {
	refs := &ResourceReferences{}
	if definition == nil {
		return refs
	}
	for _, node := range definition.Nodes {
		var raw map[string]interface{}
		if len(node.Config) == 0 || json.Unmarshal(node.Config, &raw) != nil {
			continue
		}
		switch node.Type {
		case appTypes.WorkflowNodeTypeRetrieval:
			refs.KnowledgeBaseIDs = appendUnique(refs.KnowledgeBaseIDs, draftStringSlice(raw["knowledge_base_ids"])...)
		case appTypes.WorkflowNodeTypeTool:
			kind, _ := raw["kind"].(string)
			switch kind {
			case appTypes.WorkflowToolKindBuiltin:
				if value, ok := raw["tool_name"].(string); ok {
					refs.BuiltinTools = appendUnique(refs.BuiltinTools, value)
				}
			case appTypes.WorkflowToolKindMCP:
				if value, ok := raw["service_id"].(string); ok {
					refs.MCPServiceIDs = appendUnique(refs.MCPServiceIDs, value)
				}
			case appTypes.WorkflowToolKindSkill:
				if value, ok := raw["skill_name"].(string); ok {
					refs.SkillNames = appendUnique(refs.SkillNames, value)
				}
			}
		}
	}
	return refs
}

func draftStringSlice(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			values = append(values, value)
		}
	}
	return values
}

// ValidatePublishedConfig 对准备试跑或发布的工作流执行严格校验。
//
// @param config 待发布或试跑的智能体配置。
// @returns DAG、变量、节点配置或运行必需配置不合法时返回错误。
func ValidatePublishedConfig(config *appTypes.CustomAgentConfig) error {
	if err := NormalizeDraftConfig(config); err != nil {
		return err
	}
	if config == nil || config.AgentType != appTypes.AgentTypeWorkflow {
		return nil
	}
	if err := Validate(config.Workflow); err != nil {
		return err
	}
	if len(config.SelectedSkills) > 0 && strings.TrimSpace(config.SandboxConfigID) == "" {
		return fmt.Errorf("workflow skill nodes require sandbox_config_id")
	}
	return nil
}

// NormalizeConfig 保留旧调用方的严格语义。
//
// 新代码必须根据场景显式选择 NormalizeDraftConfig 或
// ValidatePublishedConfig，避免再次把草稿保存与发布校验混为一谈。
//
// @param config 待严格归一化的智能体配置。
// @returns 配置不满足发布要求时返回错误。
func NormalizeConfig(config *appTypes.CustomAgentConfig) error {
	return ValidatePublishedConfig(config)
}

// MigrateDefinition 将旧版工作流定义转换为当前版本，并补齐所有影响执行确定性的默认值。
//
// v1 没有分支模式和稳定出边顺序，历史执行器会运行所有命中的条件边。因此，
// 只要一个节点存在多条出边，就必须迁移成 all_match；单出边节点使用 first_match。
// 迁移只做兼容转换，不替代发布阶段的严格校验。
//
// @param definition 待迁移的工作流定义，会被原地更新。
// @returns 版本号不支持或定义为空时返回错误。
func MigrateDefinition(definition *appTypes.WorkflowDefinition) error {
	if definition == nil {
		return fmt.Errorf("workflow definition is required")
	}
	version := definition.SchemaVersion
	if version == 0 {
		version = definition.Version
	}
	if version == 0 {
		version = 1
	}
	if version != 1 && version != appTypes.WorkflowVersion {
		return fmt.Errorf("unsupported workflow version %d", version)
	}

	outgoingCount := make(map[string]int)
	for _, edge := range definition.Edges {
		outgoingCount[edge.Source]++
	}
	orderBySource := make(map[string]int)
	for index := range definition.Edges {
		edge := &definition.Edges[index]
		// v1 没有可靠的 order。即便旧数据被序列化成了全零，也按原
		// 出边切片顺序分配稳定序号；新定义的显式 order 则保持不变。
		if version == 1 || edge.Order < 0 {
			edge.Order = orderBySource[edge.Source]
		}
		orderBySource[edge.Source]++
	}
	for index := range definition.Nodes {
		node := &definition.Nodes[index]
		if version == 1 || strings.TrimSpace(node.BranchMode) == "" {
			if outgoingCount[node.ID] > 1 {
				node.BranchMode = appTypes.WorkflowBranchModeAllMatch
			} else {
				node.BranchMode = appTypes.WorkflowBranchModeFirstMatch
			}
		}
		if strings.TrimSpace(node.BranchMode) == "" {
			node.BranchMode = appTypes.WorkflowBranchModeFirstMatch
		}
	}
	definition.Version = appTypes.WorkflowVersion
	definition.SchemaVersion = appTypes.WorkflowVersion
	return nil
}

func selectionMode(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return "selected"
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// ResourceReferences 汇总工作流节点引用的顶层资源。
type ResourceReferences struct {
	BuiltinTools     []string
	KnowledgeBaseIDs []string
	MCPServiceIDs    []string
	SkillNames       []string
}

// CollectResources 按画布节点顺序收集并去重工作流资源引用。
//
// @param definition 已通过或待通过静态校验的工作流定义。
// @returns 顶层资源引用集合；节点配置无法解析时返回错误。
func CollectResources(definition *appTypes.WorkflowDefinition) (*ResourceReferences, error) {
	refs := &ResourceReferences{}
	if definition == nil {
		return refs, nil
	}
	for _, node := range definition.Nodes {
		switch node.Type {
		case appTypes.WorkflowNodeTypeRetrieval:
			var cfg appTypes.WorkflowRetrievalNodeConfig
			if err := decodeNodeConfig(node, &cfg); err != nil {
				return nil, err
			}
			refs.KnowledgeBaseIDs = appendUnique(refs.KnowledgeBaseIDs, cfg.KnowledgeBaseIDs...)
		case appTypes.WorkflowNodeTypeTool:
			var cfg appTypes.WorkflowToolNodeConfig
			if err := decodeNodeConfig(node, &cfg); err != nil {
				return nil, err
			}
			switch cfg.Kind {
			case appTypes.WorkflowToolKindBuiltin:
				refs.BuiltinTools = appendUnique(refs.BuiltinTools, cfg.ToolName)
			case appTypes.WorkflowToolKindMCP:
				refs.MCPServiceIDs = appendUnique(refs.MCPServiceIDs, cfg.ServiceID)
			case appTypes.WorkflowToolKindSkill:
				refs.SkillNames = appendUnique(refs.SkillNames, cfg.SkillName)
			}
		}
	}
	return refs, nil
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, item := range dst {
		seen[item] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

// Validate 校验工作流结构、节点配置、DAG 约束和条件变量作用域。
//
// @param definition 工作流定义。
// @returns 第一处不合法配置的错误。
func Validate(definition *appTypes.WorkflowDefinition) error {
	if definition == nil {
		return fmt.Errorf("workflow definition is required")
	}
	if err := MigrateDefinition(definition); err != nil {
		return err
	}
	if definition.Version != appTypes.WorkflowVersion {
		return fmt.Errorf("unsupported workflow version %d", definition.Version)
	}
	if len(definition.Nodes) == 0 {
		return fmt.Errorf("workflow requires at least one node")
	}
	if len(definition.Nodes) > MaxNodes {
		return fmt.Errorf("workflow supports at most %d nodes", MaxNodes)
	}
	if len(definition.Edges) > MaxEdges {
		return fmt.Errorf("workflow supports at most %d edges", MaxEdges)
	}

	nodeByID := make(map[string]appTypes.WorkflowNode, len(definition.Nodes))
	startIDs := make([]string, 0, 1)
	endCount := 0
	for _, node := range definition.Nodes {
		if !workflowIDPattern.MatchString(node.ID) {
			return fmt.Errorf("invalid workflow node id %q", node.ID)
		}
		if _, exists := nodeByID[node.ID]; exists {
			return fmt.Errorf("duplicate workflow node id %q", node.ID)
		}
		if strings.TrimSpace(node.Name) == "" {
			return fmt.Errorf("workflow node %s requires a name", node.ID)
		}
		if node.BranchMode != appTypes.WorkflowBranchModeFirstMatch &&
			node.BranchMode != appTypes.WorkflowBranchModeAllMatch {
			return fmt.Errorf("workflow node %s uses unsupported branch mode %q", node.ID, node.BranchMode)
		}
		if len([]rune(node.Name)) > 100 {
			return fmt.Errorf("workflow node %s name exceeds 100 characters", node.ID)
		}
		if !finite(node.Position.X) || !finite(node.Position.Y) {
			return fmt.Errorf("workflow node %s has an invalid position", node.ID)
		}
		if err := validateNodeConfig(node); err != nil {
			return err
		}
		nodeByID[node.ID] = node
		switch node.Type {
		case appTypes.WorkflowNodeTypeStart:
			startIDs = append(startIDs, node.ID)
		case appTypes.WorkflowNodeTypeEnd:
			endCount++
		}
	}
	if len(startIDs) != 1 {
		return fmt.Errorf("workflow requires exactly one start node")
	}
	if endCount == 0 {
		return fmt.Errorf("workflow requires at least one end node")
	}

	if !finite(definition.Viewport.X) || !finite(definition.Viewport.Y) ||
		!finite(definition.Viewport.Zoom) || definition.Viewport.Zoom <= 0 {
		return fmt.Errorf("workflow viewport is invalid")
	}

	outgoing := make(map[string][]appTypes.WorkflowEdge, len(definition.Nodes))
	incoming := make(map[string][]appTypes.WorkflowEdge, len(definition.Nodes))
	edgeIDs := make(map[string]struct{}, len(definition.Edges))
	g := graph.New(graph.StringHash, graph.Directed())
	for _, node := range definition.Nodes {
		if err := g.AddVertex(node.ID); err != nil {
			return fmt.Errorf("add workflow node %s: %w", node.ID, err)
		}
	}
	for _, edge := range definition.Edges {
		if !workflowIDPattern.MatchString(edge.ID) {
			return fmt.Errorf("invalid workflow edge id %q", edge.ID)
		}
		if _, exists := edgeIDs[edge.ID]; exists {
			return fmt.Errorf("duplicate workflow edge id %q", edge.ID)
		}
		edgeIDs[edge.ID] = struct{}{}
		if _, ok := nodeByID[edge.Source]; !ok {
			return fmt.Errorf("workflow edge %s references missing source %s", edge.ID, edge.Source)
		}
		if _, ok := nodeByID[edge.Target]; !ok {
			return fmt.Errorf("workflow edge %s references missing target %s", edge.ID, edge.Target)
		}
		if edge.Source == edge.Target {
			return fmt.Errorf("workflow edge %s cannot connect a node to itself", edge.ID)
		}
		if edge.Order < 0 {
			return fmt.Errorf("workflow edge %s order cannot be negative", edge.ID)
		}
		if edge.IsDefault && edge.Condition != nil && len(edge.Condition.Items) > 0 {
			return fmt.Errorf("default workflow edge %s cannot have a condition", edge.ID)
		}
		if edge.Condition != nil {
			if err := validateCondition(edge.Condition); err != nil {
				return fmt.Errorf("workflow edge %s: %w", edge.ID, err)
			}
		}
		outgoing[edge.Source] = append(outgoing[edge.Source], edge)
		incoming[edge.Target] = append(incoming[edge.Target], edge)
		if err := g.AddEdge(edge.Source, edge.Target); err != nil {
			return fmt.Errorf("add workflow edge %s: %w", edge.ID, err)
		}
	}
	if _, err := graph.TopologicalSort(g); err != nil {
		return fmt.Errorf("workflow must be acyclic: %w", err)
	}

	startID := startIDs[0]
	if len(incoming[startID]) != 0 {
		return fmt.Errorf("workflow start node cannot have incoming edges")
	}
	for _, node := range definition.Nodes {
		if len(incoming[node.ID]) > 1 {
			return fmt.Errorf("workflow node %s has multiple incoming edges; branch merging is not supported", node.ID)
		}
		if node.Type == appTypes.WorkflowNodeTypeEnd {
			if len(outgoing[node.ID]) > 0 {
				return fmt.Errorf("workflow end node %s cannot have outgoing edges", node.ID)
			}
			continue
		}
		if len(outgoing[node.ID]) == 0 {
			return fmt.Errorf("workflow node %s is a dead end", node.ID)
		}
		defaults := 0
		for _, edge := range outgoing[node.ID] {
			if edge.IsDefault {
				defaults++
			}
		}
		if defaults > 1 {
			return fmt.Errorf("workflow node %s has more than one default edge", node.ID)
		}
		if len(outgoing[node.ID]) > 1 {
			for _, edge := range outgoing[node.ID] {
				if !edge.IsDefault && (edge.Condition == nil || len(edge.Condition.Items) == 0) {
					return fmt.Errorf("workflow edge %s requires a condition because its source branches", edge.ID)
				}
			}
		}
	}

	reachable := reachableNodes(startID, outgoing)
	if len(reachable) != len(definition.Nodes) {
		missing := make([]string, 0)
		for id := range nodeByID {
			if !reachable[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		return fmt.Errorf("workflow contains unreachable nodes: %s", strings.Join(missing, ", "))
	}

	for _, edge := range definition.Edges {
		if edge.Condition == nil {
			continue
		}
		allowed := ancestorSet(edge.Source, incoming)
		for _, item := range edge.Condition.Items {
			if inputVariableRE.MatchString(item.Variable) {
				continue
			}
			match := nodeVariableRE.FindStringSubmatch(item.Variable)
			if len(match) == 0 {
				return fmt.Errorf("workflow edge %s uses invalid variable %q", edge.ID, item.Variable)
			}
			if !allowed[match[1]] {
				return fmt.Errorf("workflow edge %s can only reference outputs on its current upstream path: %s", edge.ID, item.Variable)
			}
		}
	}

	// 模板里的 {{nodes.<id>.*}} 此前完全不校验，写错节点 ID 也能保存成功，直到
	// 运行到该节点才以 "unavailable on this branch" 失败——用户既看不出是哪个
	// 节点写错，也不知道该改成什么。这里在保存期就拦截。
	for _, node := range definition.Nodes {
		if err := validateNodeTemplates(node, nodeByID, incoming); err != nil {
			return err
		}
	}
	return nil
}

// ValidateIssues 返回编辑器可直接消费的结构化校验问题。
//
// 现有 Validate 保留单错误字符串契约，避免影响旧调用方；新接口使用本方法，
// 并尽量从兼容错误文本中提取节点、连线和字段路径。
//
// @param definition 待校验的工作流定义。
// @returns 所有当前可识别的校验问题；定义合法时返回空切片。
func ValidateIssues(definition *appTypes.WorkflowDefinition) []appTypes.WorkflowValidationIssue {
	issues := make([]appTypes.WorkflowValidationIssue, 0)
	if definition == nil {
		return append(issues, appTypes.WorkflowValidationIssue{
			Code:    "WORKFLOW_REQUIRED",
			Message: "工作流定义不能为空",
		})
	}
	if err := Validate(definition); err != nil {
		issue := appTypes.WorkflowValidationIssue{
			Code:    validationIssueCode(err),
			Message: err.Error(),
		}
		if match := workflowNodeErrorRE.FindStringSubmatch(err.Error()); len(match) > 1 {
			issue.NodeID = match[1]
		}
		if match := workflowEdgeErrorRE.FindStringSubmatch(err.Error()); len(match) > 1 {
			issue.EdgeID = match[1]
		}
		issues = append(issues, issue)
	}
	return issues
}

// templateField 描述一个节点内可渲染的模板字段，用于报错时告诉用户改哪里。
type templateField struct {
	label  string
	values []string
}

// nodeTemplateFields 收集节点配置里所有会被渲染的字符串。
//
// 覆盖范围必须与执行期的实际渲染点一致，否则校验会漏（保存通过、运行时才炸）。
func nodeTemplateFields(node appTypes.WorkflowNode) ([]templateField, error) {
	switch node.Type {
	case appTypes.WorkflowNodeTypeRetrieval:
		var cfg appTypes.WorkflowRetrievalNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return nil, err
		}
		return []templateField{{label: "检索关键词", values: []string{cfg.QueryTemplate}}}, nil
	case appTypes.WorkflowNodeTypeLLM:
		var cfg appTypes.WorkflowLLMNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return nil, err
		}
		fields := make([]templateField, 0, 2)
		if strings.TrimSpace(cfg.SystemPrompt) != "" {
			fields = append(fields, templateField{label: "系统提示词", values: []string{cfg.SystemPrompt}})
		}
		fields = append(fields, templateField{label: "提示词", values: []string{cfg.Prompt}})
		return fields, nil
	case appTypes.WorkflowNodeTypeLLMDecision:
		var cfg appTypes.WorkflowLLMDecisionNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return nil, err
		}
		return []templateField{{label: "判断说明", values: []string{cfg.Prompt}}}, nil
	case appTypes.WorkflowNodeTypeHTTP:
		var cfg appTypes.WorkflowHTTPNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return nil, err
		}
		fields := []templateField{
			{label: "请求地址", values: []string{cfg.URL}},
			{label: "请求内容", values: []string{cfg.BodyTemplate}},
		}
		headerValues := make([]string, 0, len(cfg.Headers))
		for _, value := range cfg.Headers {
			headerValues = append(headerValues, value)
		}
		return append(fields, templateField{label: "请求头", values: headerValues}), nil
	case appTypes.WorkflowNodeTypeTool:
		var cfg appTypes.WorkflowToolNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return nil, err
		}
		fields := make([]templateField, 0, 2)
		if argValues := collectTemplateStrings(cfg.Arguments); len(argValues) > 0 {
			fields = append(fields, templateField{label: "工具参数", values: argValues})
		}
		fields = append(fields, templateField{label: "任务说明", values: []string{cfg.TaskTemplate}})
		return fields, nil
	case appTypes.WorkflowNodeTypeEnd:
		var cfg appTypes.WorkflowEndNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return nil, err
		}
		return []templateField{{label: "输出内容", values: []string{cfg.TextTemplate}}}, nil
	default:
		// start 节点没有可渲染字段。
		return nil, nil
	}
}

// collectTemplateStrings 递归取出任意嵌套结构里的字符串，用于校验工具参数。
func collectTemplateStrings(value interface{}) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case map[string]interface{}:
		collected := make([]string, 0, len(typed))
		for _, item := range typed {
			collected = append(collected, collectTemplateStrings(item)...)
		}
		return collected
	case []interface{}:
		collected := make([]string, 0, len(typed))
		for _, item := range typed {
			collected = append(collected, collectTemplateStrings(item)...)
		}
		return collected
	default:
		return nil
	}
}

// validateNodeTemplates 校验节点模板引用的变量在保存期就存在且可达。
//
// 变量必须属于该节点的严格上游（不含自身）。节点不存在、引用自身或不在上游时直接报错，
// 避免用户把错误留到运行时才发现。
func validateNodeTemplates(
	node appTypes.WorkflowNode,
	nodeByID map[string]appTypes.WorkflowNode,
	incoming map[string][]appTypes.WorkflowEdge,
) error {
	fields, err := nodeTemplateFields(node)
	if err != nil {
		return err
	}
	allowed := ancestorSet(node.ID, incoming)
	for _, field := range fields {
		for _, value := range field.values {
			for _, match := range workflowTemplateRE.FindAllStringSubmatch(value, -1) {
				if len(match) < 2 {
					continue
				}
				variable := match[1]
				if inputVariableRE.MatchString(variable) {
					continue
				}
				nodeMatch := nodeVariableRE.FindStringSubmatch(variable)
				if len(nodeMatch) == 0 {
					return fmt.Errorf(
						"workflow node %s 的%s引用了不支持的变量 %s；可用变量为 input.query、input.attachments_text 或 nodes.<节点ID>.text|status|data.*",
						nodeDisplayName(node), field.label, variable)
				}
				referencedID := nodeMatch[1]
				if _, exists := nodeByID[referencedID]; !exists {
					return fmt.Errorf(
						"workflow node %s 的%s引用了不存在的节点 %q（变量 %s）",
						nodeDisplayName(node), field.label, referencedID, variable)
				}
				if referencedID == node.ID {
					return fmt.Errorf(
						"workflow node %s 的%s不能引用节点自身 %q；节点自身的输出在当前步骤尚未产生",
						nodeDisplayName(node), field.label, referencedID)
				}
				if !allowed[referencedID] {
					return fmt.Errorf(
						"workflow node %s 的%s引用了非上游节点 %q；只有该节点之前执行过的步骤才能被引用",
						nodeDisplayName(node), field.label, referencedID)
				}
			}
		}
	}
	return nil
}

// nodeDisplayName 生成"名称（ID）"形式的节点标识，便于用户定位到画布节点。
func nodeDisplayName(node appTypes.WorkflowNode) string {
	if strings.TrimSpace(node.Name) == "" {
		return fmt.Sprintf("%q", node.ID)
	}
	return fmt.Sprintf("%q（ID: %s）", node.Name, node.ID)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateNodeConfig(node appTypes.WorkflowNode) error {
	switch node.Type {
	case appTypes.WorkflowNodeTypeStart:
		var cfg appTypes.WorkflowStartNodeConfig
		return decodeNodeConfig(node, &cfg)
	case appTypes.WorkflowNodeTypeRetrieval:
		var cfg appTypes.WorkflowRetrievalNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return err
		}
		if len(cfg.KnowledgeBaseIDs) == 0 {
			return fmt.Errorf("workflow retrieval node %s requires at least one knowledge base", node.ID)
		}
		for _, kbID := range cfg.KnowledgeBaseIDs {
			if strings.TrimSpace(kbID) == "" || kbID != strings.TrimSpace(kbID) {
				return fmt.Errorf("workflow retrieval node %s has an invalid knowledge base id", node.ID)
			}
		}
		if cfg.TopK <= 0 || cfg.TopK > 50 {
			return fmt.Errorf("workflow retrieval node %s top_k must be between 1 and 50", node.ID)
		}
	case appTypes.WorkflowNodeTypeLLM:
		var cfg appTypes.WorkflowLLMNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.Prompt) == "" {
			return fmt.Errorf("workflow LLM node %s requires a prompt", node.ID)
		}
		if cfg.Temperature != nil && (*cfg.Temperature < 0 || *cfg.Temperature > 2) {
			return fmt.Errorf("workflow LLM node %s temperature must be between 0 and 2", node.ID)
		}
		if cfg.MaxTokens != nil && *cfg.MaxTokens <= 0 {
			return fmt.Errorf("workflow LLM node %s max_tokens must be greater than 0", node.ID)
		}
	case appTypes.WorkflowNodeTypeLLMDecision:
		var cfg appTypes.WorkflowLLMDecisionNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.Prompt) == "" {
			return fmt.Errorf("workflow LLM node %s requires a prompt", node.ID)
		}
		if len(cfg.Choices) < 2 || len(cfg.Choices) > 20 {
			return fmt.Errorf("workflow LLM node %s requires 2 to 20 choices", node.ID)
		}
		seen := make(map[string]struct{}, len(cfg.Choices))
		for _, choice := range cfg.Choices {
			choice = strings.TrimSpace(choice)
			if choice == "" || len([]rune(choice)) > 100 {
				return fmt.Errorf("workflow LLM node %s has an invalid choice", node.ID)
			}
			if _, ok := seen[choice]; ok {
				return fmt.Errorf("workflow LLM node %s has duplicate choice %q", node.ID, choice)
			}
			seen[choice] = struct{}{}
		}
	case appTypes.WorkflowNodeTypeHTTP:
		var cfg appTypes.WorkflowHTTPNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return err
		}
		method := strings.ToUpper(strings.TrimSpace(cfg.Method))
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return fmt.Errorf("workflow HTTP node %s uses unsupported method %q", node.ID, cfg.Method)
		}
		if err := validateHTTPTemplateURL(cfg.URL); err != nil {
			return fmt.Errorf("workflow HTTP node %s: %w", node.ID, err)
		}
		if len(cfg.Headers) > 50 {
			return fmt.Errorf("workflow HTTP node %s has too many headers", node.ID)
		}
		for name, value := range cfg.Headers {
			canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
			if canonical == "" || len(canonical) > 128 || len(value) > 8192 {
				return fmt.Errorf("workflow HTTP node %s has an invalid header", node.ID)
			}
			if _, blocked := blockedHTTPHeaders[canonical]; blocked || isCommonAPIKeyHeader(canonical) {
				return fmt.Errorf("workflow HTTP node %s cannot set sensitive header %s", node.ID, canonical)
			}
		}
	case appTypes.WorkflowNodeTypeTool:
		var cfg appTypes.WorkflowToolNodeConfig
		if err := decodeNodeConfig(node, &cfg); err != nil {
			return err
		}
		switch cfg.Kind {
		case appTypes.WorkflowToolKindBuiltin:
			if _, ok := BuiltinToolAllowlist[cfg.ToolName]; !ok {
				return fmt.Errorf("workflow tool node %s uses disallowed builtin tool %q", node.ID, cfg.ToolName)
			}
		case appTypes.WorkflowToolKindMCP:
			if strings.TrimSpace(cfg.ServiceID) == "" || cfg.ServiceID != strings.TrimSpace(cfg.ServiceID) ||
				strings.TrimSpace(cfg.ToolName) == "" || cfg.ToolName != strings.TrimSpace(cfg.ToolName) {
				return fmt.Errorf("workflow MCP node %s requires service_id and tool_name", node.ID)
			}
		case appTypes.WorkflowToolKindSkill:
			if strings.TrimSpace(cfg.SkillName) == "" || strings.TrimSpace(cfg.TaskTemplate) == "" {
				return fmt.Errorf("workflow Skill node %s requires skill_name and task_template", node.ID)
			}
		default:
			return fmt.Errorf("workflow tool node %s uses unsupported kind %q", node.ID, cfg.Kind)
		}
	case appTypes.WorkflowNodeTypeEnd:
		var cfg appTypes.WorkflowEndNodeConfig
		return decodeNodeConfig(node, &cfg)
	default:
		return fmt.Errorf("workflow node %s uses unsupported type %q", node.ID, node.Type)
	}
	return nil
}

func decodeNodeConfig(node appTypes.WorkflowNode, dst interface{}) error {
	raw := node.Config
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("workflow node %s has invalid config: %w", node.ID, err)
	}
	return nil
}

func validateHTTPTemplateURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("URL is required")
	}
	if len(trimmed) > 4096 {
		return fmt.Errorf("URL exceeds 4096 characters")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("URL must use http or https")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("URL requires a hostname")
	}
	if strings.Contains(parsed.Scheme, "{{") || strings.Contains(parsed.Host, "{{") {
		return fmt.Errorf("URL variables are allowed only in path, query, or fragment")
	}
	return nil
}

func isCommonAPIKeyHeader(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(name, "-", ""))
	return strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "accesstoken") ||
		strings.Contains(normalized, "authtoken")
}

func validateCondition(condition *appTypes.WorkflowCondition) error {
	if condition == nil || len(condition.Items) == 0 {
		return nil
	}
	if condition.Mode != appTypes.WorkflowConditionAll && condition.Mode != appTypes.WorkflowConditionAny {
		return fmt.Errorf("condition mode must be all or any")
	}
	if len(condition.Items) > 20 {
		return fmt.Errorf("condition group supports at most 20 items")
	}
	for _, item := range condition.Items {
		if _, ok := conditionOperators[item.Operator]; !ok {
			return fmt.Errorf("unsupported condition operator %q", item.Operator)
		}
		if !inputVariableRE.MatchString(item.Variable) && !nodeVariableRE.MatchString(item.Variable) {
			return fmt.Errorf("invalid condition variable %q", item.Variable)
		}
	}
	_, err := CompileCondition(condition)
	return err
}

func reachableNodes(start string, outgoing map[string][]appTypes.WorkflowEdge) map[string]bool {
	reachable := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range outgoing[current] {
			if reachable[edge.Target] {
				continue
			}
			reachable[edge.Target] = true
			queue = append(queue, edge.Target)
		}
	}
	return reachable
}

func ancestorSet(nodeID string, incoming map[string][]appTypes.WorkflowEdge) map[string]bool {
	allowed := make(map[string]bool)
	current := nodeID
	for current != "" && !allowed[current] {
		allowed[current] = true
		edges := incoming[current]
		if len(edges) == 0 {
			break
		}
		current = edges[0].Source
	}
	return allowed
}

// CompileCondition 将结构化条件安全转换为 CEL 程序。
//
// @param condition 结构化条件组。
// @returns 可并发复用的 CEL 程序；表达式无法编译时返回错误。
func CompileCondition(condition *appTypes.WorkflowCondition) (cel.Program, error) {
	expression, err := conditionExpression(condition)
	if err != nil {
		return nil, err
	}
	env, err := cel.NewEnv(
		cel.Variable("lefts", cel.ListType(cel.DynType)),
		cel.Variable("rights", cel.ListType(cel.DynType)),
		cel.Variable("empties", cel.ListType(cel.BoolType)),
	)
	if err != nil {
		return nil, fmt.Errorf("create CEL environment: %w", err)
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile workflow condition: %w", issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("build workflow condition program: %w", err)
	}
	return program, nil
}

func conditionExpression(condition *appTypes.WorkflowCondition) (string, error) {
	if condition == nil || len(condition.Items) == 0 {
		return "true", nil
	}
	joiner := " && "
	if condition.Mode == appTypes.WorkflowConditionAny {
		joiner = " || "
	} else if condition.Mode != appTypes.WorkflowConditionAll {
		return "", fmt.Errorf("condition mode must be all or any")
	}
	parts := make([]string, 0, len(condition.Items))
	for i, item := range condition.Items {
		left := fmt.Sprintf("lefts[%d]", i)
		right := fmt.Sprintf("rights[%d]", i)
		var part string
		switch item.Operator {
		case "eq":
			part = left + " == " + right
		case "neq":
			part = left + " != " + right
		case "gt":
			part = left + " > " + right
		case "gte":
			part = left + " >= " + right
		case "lt":
			part = left + " < " + right
		case "lte":
			part = left + " <= " + right
		case "contains":
			part = left + ".contains(" + right + ")"
		case "not_contains":
			part = "!(" + left + ".contains(" + right + "))"
		case "starts_with":
			part = left + ".startsWith(" + right + ")"
		case "ends_with":
			part = left + ".endsWith(" + right + ")"
		case "in":
			part = left + " in " + right
		case "not_in":
			part = "!(" + left + " in " + right + ")"
		case "is_empty":
			part = fmt.Sprintf("empties[%d]", i)
		case "is_not_empty":
			part = fmt.Sprintf("!empties[%d]", i)
		default:
			return "", fmt.Errorf("unsupported condition operator %q", item.Operator)
		}
		parts = append(parts, "("+part+")")
	}
	return strings.Join(parts, joiner), nil
}

func isEmptyValue(value interface{}) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return text == ""
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice:
		return rv.Len() == 0
	default:
		return false
	}
}
