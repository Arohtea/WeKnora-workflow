package types

import "encoding/json"

const (
	// WorkflowVersion 是当前工作流配置格式版本。
	WorkflowVersion = 2

	// WorkflowBranchModeFirstMatch 按稳定顺序只执行首个命中的分支。
	WorkflowBranchModeFirstMatch = "first_match"
	// WorkflowBranchModeAllMatch 并行执行全部命中的分支。
	WorkflowBranchModeAllMatch = "all_match"

	// WorkflowNodeTypeStart 表示工作流唯一入口节点。
	WorkflowNodeTypeStart = "start"
	// WorkflowNodeTypeLLM 表示通用大模型处理与文本生成节点。
	WorkflowNodeTypeLLM = "llm"
	// WorkflowNodeTypeRetrieval 表示知识库检索节点。
	WorkflowNodeTypeRetrieval = "knowledge-retrieval"
	// WorkflowNodeTypeLLMDecision 表示由模型返回一个确定候选标签的判断节点。
	WorkflowNodeTypeLLMDecision = "llm-decision"
	// WorkflowNodeTypeHTTP 表示受 SSRF 防护约束的 HTTP 请求节点。
	WorkflowNodeTypeHTTP = "http-request"
	// WorkflowNodeTypeTool 表示内置工具、MCP 或 Skill 节点。
	WorkflowNodeTypeTool = "tool"
	// WorkflowNodeTypeEnd 表示工作流终点节点。
	WorkflowNodeTypeEnd = "end"

	// WorkflowConditionAll 要求条件组内全部条件成立。
	WorkflowConditionAll = "all"
	// WorkflowConditionAny 要求条件组内至少一个条件成立。
	WorkflowConditionAny = "any"

	// WorkflowToolKindBuiltin 表示直接执行受限内置工具。
	WorkflowToolKindBuiltin = "builtin"
	// WorkflowToolKindMCP 表示直接执行一个 MCP 工具。
	WorkflowToolKindMCP = "mcp"
	// WorkflowToolKindSkill 表示启动仅允许一个 Skill 的受限小智能体。
	WorkflowToolKindSkill = "skill"
)

// WorkflowDefinition 是持久化到 custom_agents.config 的版本化工作流定义。
type WorkflowDefinition struct {
	// Version 保留旧字段名，兼容已经保存的 v1/v2 工作流。
	Version int `yaml:"version,omitempty" json:"version,omitempty"`
	// SchemaVersion 是导入导出及新接口使用的明确格式版本。
	SchemaVersion int              `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`
	Nodes         []WorkflowNode   `yaml:"nodes" json:"nodes"`
	Edges         []WorkflowEdge   `yaml:"edges" json:"edges"`
	Viewport      WorkflowViewport `yaml:"viewport" json:"viewport"`
}

// WorkflowNode 是画布上的统一节点结构，Config 由节点类型决定。
type WorkflowNode struct {
	ID         string           `yaml:"id" json:"id"`
	Type       string           `yaml:"type" json:"type"`
	Name       string           `yaml:"name" json:"name"`
	BranchMode string           `yaml:"branch_mode,omitempty" json:"branch_mode,omitempty"`
	Position   WorkflowPosition `yaml:"position" json:"position"`
	Config     json.RawMessage  `yaml:"config" json:"config"`
}

// WorkflowEdge 描述两个节点间的确定性有向连接和可选路由条件。
type WorkflowEdge struct {
	ID           string             `yaml:"id" json:"id"`
	Source       string             `yaml:"source" json:"source"`
	Target       string             `yaml:"target" json:"target"`
	SourceHandle string             `yaml:"source_handle,omitempty" json:"source_handle,omitempty"`
	TargetHandle string             `yaml:"target_handle,omitempty" json:"target_handle,omitempty"`
	Order        int                `yaml:"order" json:"order"`
	IsDefault    bool               `yaml:"is_default" json:"is_default"`
	Condition    *WorkflowCondition `yaml:"condition,omitempty" json:"condition,omitempty"`
}

// WorkflowCondition 是不向用户暴露 CEL 文本的平铺条件组。
type WorkflowCondition struct {
	Mode  string                  `yaml:"mode" json:"mode"`
	Items []WorkflowConditionItem `yaml:"items" json:"items"`
}

// WorkflowConditionItem 描述一个变量、操作符和值组成的条件。
type WorkflowConditionItem struct {
	Variable string      `yaml:"variable" json:"variable"`
	Operator string      `yaml:"operator" json:"operator"`
	Value    interface{} `yaml:"value,omitempty" json:"value,omitempty"`
}

// WorkflowPosition 是节点在 Vue Flow 画布上的坐标。
type WorkflowPosition struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
}

// WorkflowViewport 保存画布缩放和平移状态。
type WorkflowViewport struct {
	X    float64 `yaml:"x" json:"x"`
	Y    float64 `yaml:"y" json:"y"`
	Zoom float64 `yaml:"zoom" json:"zoom"`
}

// WorkflowStartNodeConfig 是开始节点的配置，目前预留为空结构。
type WorkflowStartNodeConfig struct{}

// WorkflowRetrievalNodeConfig 控制一次知识库检索。
type WorkflowRetrievalNodeConfig struct {
	KnowledgeBaseIDs []string `yaml:"knowledge_base_ids" json:"knowledge_base_ids"`
	QueryTemplate    string   `yaml:"query_template" json:"query_template"`
	TopK             int      `yaml:"top_k" json:"top_k"`
}

// WorkflowLLMNodeConfig 控制通用大模型处理与文本生成。
type WorkflowLLMNodeConfig struct {
	SystemPrompt string   `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	Prompt       string   `yaml:"prompt" json:"prompt"`
	Temperature  *float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	MaxTokens    *int     `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
}

// WorkflowLLMDecisionNodeConfig 控制确定标签的模型判断。
type WorkflowLLMDecisionNodeConfig struct {
	Prompt  string   `yaml:"prompt" json:"prompt"`
	Choices []string `yaml:"choices" json:"choices"`
	ModelID string   `yaml:"model_id,omitempty" json:"model_id,omitempty"`
}

// WorkflowHTTPNodeConfig 控制受限 HTTP 请求。
type WorkflowHTTPNodeConfig struct {
	Method                 string            `yaml:"method" json:"method"`
	URL                    string            `yaml:"url" json:"url"`
	Headers                map[string]string `yaml:"headers" json:"headers"`
	BodyTemplate           string            `yaml:"body_template" json:"body_template"`
	IdempotencyKeyTemplate string            `yaml:"idempotency_key_template,omitempty" json:"idempotency_key_template,omitempty"`
}

// WorkflowToolNodeConfig 控制内置工具、MCP 工具或 Skill 小智能体。
type WorkflowToolNodeConfig struct {
	Kind         string                 `yaml:"kind" json:"kind"`
	ToolName     string                 `yaml:"tool_name,omitempty" json:"tool_name,omitempty"`
	ServiceID    string                 `yaml:"service_id,omitempty" json:"service_id,omitempty"`
	Arguments    map[string]interface{} `yaml:"arguments,omitempty" json:"arguments,omitempty"`
	SkillName    string                 `yaml:"skill_name,omitempty" json:"skill_name,omitempty"`
	TaskTemplate string                 `yaml:"task_template,omitempty" json:"task_template,omitempty"`
	RetrySafe    bool                   `yaml:"retry_safe,omitempty" json:"retry_safe,omitempty"`
}

// WorkflowEndNodeConfig 控制结束节点输出文本；空模板沿用上游节点文本。
type WorkflowEndNodeConfig struct {
	TextTemplate string `yaml:"text_template" json:"text_template"`
}

// WorkflowNodeOutput 是节点间传递的固定输出封装。
type WorkflowNodeOutput struct {
	Text   string                 `json:"text"`
	Data   map[string]interface{} `json:"data"`
	Status string                 `json:"status"`
}

// WorkflowCatalog 是工作流编辑器可选择资源的授权目录。
type WorkflowCatalog struct {
	BuiltinTools []WorkflowCatalogTool    `json:"builtin_tools"`
	MCPServices  []WorkflowCatalogService `json:"mcp_services"`
	Skills       []WorkflowCatalogSkill   `json:"skills"`
}

// WorkflowCatalogTool 描述一个可直接配置参数的工具。
type WorkflowCatalogTool struct {
	Name            string          `json:"name"`
	DisplayName     string          `json:"display_name,omitempty"`
	Description     string          `json:"description,omitempty"`
	Parameters      json.RawMessage `json:"parameters"`
	RequireApproval bool            `json:"require_approval,omitempty"`
}

// WorkflowCatalogService 描述一个 MCP 服务及其工具定义。
type WorkflowCatalogService struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Tools       []WorkflowCatalogTool `json:"tools"`
}

// WorkflowCatalogSkill 描述当前沙箱中可用的已安装 Skill。
type WorkflowCatalogSkill struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// DefaultWorkflowDefinition 返回可直接保存和执行的“开始到结束”默认图。
//
// @returns 独立的新工作流定义，调用方可以安全修改。
func DefaultWorkflowDefinition() *WorkflowDefinition {
	return &WorkflowDefinition{
		Version:       WorkflowVersion,
		SchemaVersion: WorkflowVersion,
		Nodes: []WorkflowNode{
			{
				ID:         "start",
				Type:       WorkflowNodeTypeStart,
				Name:       "开始",
				BranchMode: WorkflowBranchModeFirstMatch,
				Position:   WorkflowPosition{X: 80, Y: 160},
				Config:     json.RawMessage(`{}`),
			},
			{
				ID:         "end",
				Type:       WorkflowNodeTypeEnd,
				Name:       "结束",
				BranchMode: WorkflowBranchModeFirstMatch,
				Position:   WorkflowPosition{X: 420, Y: 160},
				Config:     json.RawMessage(`{"text_template":"{{input.query}}"}`),
			},
		},
		Edges: []WorkflowEdge{
			{ID: "start-end", Source: "start", Target: "end", Order: 0},
		},
		Viewport: WorkflowViewport{X: 0, Y: 0, Zoom: 1},
	}
}

// IsWorkflowAgent 判断配置是否为工作流智能体。
//
// @param config 智能体配置。
// @returns 当运行模式和类型均匹配工作流时返回 true。
func IsWorkflowAgent(config *CustomAgentConfig) bool {
	return config != nil &&
		config.AgentMode == AgentModeSmartReasoning &&
		config.AgentType == AgentTypeWorkflow
}
