package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	appTypes "github.com/Tencent/WeKnora/internal/types"
)

// buildDefinition 构造一个 start → retrieval → end 的最小合法图，便于各用例只改
// 自己关心的那一处配置。
func buildDefinition(retrievalQuery, endTemplate string) *appTypes.WorkflowDefinition {
	retrievalConfig, _ := json.Marshal(appTypes.WorkflowRetrievalNodeConfig{
		KnowledgeBaseIDs: []string{"kb-1"},
		QueryTemplate:    retrievalQuery,
		TopK:             5,
	})
	endConfig, _ := json.Marshal(appTypes.WorkflowEndNodeConfig{TextTemplate: endTemplate})
	return &appTypes.WorkflowDefinition{
		Version: appTypes.WorkflowVersion,
		Nodes: []appTypes.WorkflowNode{
			{ID: "start", Type: appTypes.WorkflowNodeTypeStart, Name: "开始",
				Position: appTypes.WorkflowPosition{X: 0, Y: 0}, Config: json.RawMessage(`{}`)},
			{ID: "find", Type: appTypes.WorkflowNodeTypeRetrieval, Name: "查知识库",
				Position: appTypes.WorkflowPosition{X: 1, Y: 0}, Config: retrievalConfig},
			{ID: "end", Type: appTypes.WorkflowNodeTypeEnd, Name: "输出",
				Position: appTypes.WorkflowPosition{X: 2, Y: 0}, Config: endConfig},
		},
		Edges: []appTypes.WorkflowEdge{
			{ID: "start-find", Source: "start", Target: "find"},
			{ID: "find-end", Source: "find", Target: "end"},
		},
		Viewport: appTypes.WorkflowViewport{Zoom: 1},
	}
}

// TestValidateAcceptsUpstreamTemplateVariables 确认正常引用的模板可以通过校验：
// 检索节点用提问，输出节点用检索结果，都是既有模板实际使用的写法。
func TestValidateAcceptsUpstreamTemplateVariables(t *testing.T) {
	definition := buildDefinition("{{input.query}}", "{{nodes.find.text}}")
	if err := Validate(definition); err != nil {
		t.Fatalf("expected valid workflow, got error: %v", err)
	}
}

// TestValidateRejectsUnknownNodeInTemplate 覆盖本次修复的核心问题：模板里写了不存在
// 的节点 ID 时必须在保存期报错，而不是等到运行时才失败。
func TestValidateRejectsUnknownNodeInTemplate(t *testing.T) {
	definition := buildDefinition("{{input.query}}", "{{nodes.typo-1.text}}")
	err := Validate(definition)
	if err == nil {
		t.Fatal("expected error for unknown node reference, got nil")
	}
	if !strings.Contains(err.Error(), "typo-1") {
		t.Fatalf("error should name the missing node, got: %v", err)
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("error should explain the node does not exist, got: %v", err)
	}
}

// TestValidateRejectsDownstreamTemplateReference 确认模板只能引用上游节点。
// 这里让 end 引用一个与它平级（同属 find 的分支）的节点，属于"引用了非上游节点"。
func TestValidateRejectsDownstreamTemplateReference(t *testing.T) {
	definition := buildDefinition("{{input.query}}", "{{nodes.later.text}}")
	laterConfig, _ := json.Marshal(appTypes.WorkflowRetrievalNodeConfig{
		KnowledgeBaseIDs: []string{"kb-1"}, QueryTemplate: "{{input.query}}", TopK: 5,
	})
	laterEndConfig, _ := json.Marshal(appTypes.WorkflowEndNodeConfig{TextTemplate: "{{input.query}}"})
	// find 有两条出边时必须都带条件，因此给通往 end 的那条补一个恒真条件，
	// 让图本身合法，从而把断言聚焦在"end 引用了非上游节点"这一点上。
	definition.Edges[1].Condition = &appTypes.WorkflowCondition{
		Mode:  "all",
		Items: []appTypes.WorkflowConditionItem{{Variable: "input.query", Operator: "is_not_empty"}},
	}
	definition.Nodes = append(definition.Nodes,
		appTypes.WorkflowNode{
			ID: "later", Type: appTypes.WorkflowNodeTypeRetrieval, Name: "并行分支",
			Position: appTypes.WorkflowPosition{X: 3, Y: 0}, Config: laterConfig,
		},
		appTypes.WorkflowNode{
			ID: "end2", Type: appTypes.WorkflowNodeTypeEnd, Name: "输出2",
			Position: appTypes.WorkflowPosition{X: 4, Y: 0}, Config: laterEndConfig,
		},
	)
	definition.Edges = append(definition.Edges,
		appTypes.WorkflowEdge{
			ID: "find-later", Source: "find", Target: "later",
			Condition: &appTypes.WorkflowCondition{
				Mode:  "all",
				Items: []appTypes.WorkflowConditionItem{{Variable: "input.query", Operator: "is_empty"}},
			},
		},
		appTypes.WorkflowEdge{ID: "later-end2", Source: "later", Target: "end2"},
	)

	err := Validate(definition)
	if err == nil {
		t.Fatal("expected error for non-upstream reference, got nil")
	}
	if !strings.Contains(err.Error(), "上游") {
		t.Fatalf("error should explain the upstream requirement, got: %v", err)
	}
}

// TestValidateChecksToolArgumentTemplates 确认嵌套在工具参数里的字符串同样被校验。
// 工具参数是用户最容易写错变量又最难排查的位置。
func TestValidateChecksToolArgumentTemplates(t *testing.T) {
	toolConfig, _ := json.Marshal(appTypes.WorkflowToolNodeConfig{
		Kind:     appTypes.WorkflowToolKindBuiltin,
		ToolName: "web_search",
		Arguments: map[string]interface{}{
			"nested": map[string]interface{}{"value": "{{nodes.missing.text}}"},
		},
	})
	endConfig, _ := json.Marshal(appTypes.WorkflowEndNodeConfig{TextTemplate: "{{input.query}}"})
	definition := &appTypes.WorkflowDefinition{
		Version: appTypes.WorkflowVersion,
		Nodes: []appTypes.WorkflowNode{
			{ID: "start", Type: appTypes.WorkflowNodeTypeStart, Name: "开始",
				Position: appTypes.WorkflowPosition{}, Config: json.RawMessage(`{}`)},
			{ID: "search", Type: appTypes.WorkflowNodeTypeTool, Name: "联网搜索",
				Position: appTypes.WorkflowPosition{X: 1}, Config: toolConfig},
			{ID: "end", Type: appTypes.WorkflowNodeTypeEnd, Name: "输出",
				Position: appTypes.WorkflowPosition{X: 2}, Config: endConfig},
		},
		Edges: []appTypes.WorkflowEdge{
			{ID: "start-search", Source: "start", Target: "search"},
			{ID: "search-end", Source: "search", Target: "end"},
		},
		Viewport: appTypes.WorkflowViewport{Zoom: 1},
	}
	err := Validate(definition)
	if err == nil {
		t.Fatal("expected error for bad variable inside tool arguments, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error should name the bad node id, got: %v", err)
	}
}

// TestValidateChecksHTTPTemplates 确认 HTTP 节点的地址、请求头与请求体都会被校验。
func TestValidateChecksHTTPTemplates(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		headers   map[string]string
		body      string
		wantInErr string
	}{
		{
			name: "地址引用不存在节点", url: "https://example.com/{{nodes.ghost.text}}",
			wantInErr: "ghost",
		},
		{
			name: "请求头引用不存在节点", url: "https://example.com",
			headers: map[string]string{"X-Trace": "{{nodes.ghost.text}}"}, wantInErr: "ghost",
		},
		{
			name: "请求体引用不存在节点", url: "https://example.com",
			body: "{{nodes.ghost.text}}", wantInErr: "ghost",
		},
		{
			name: "正常引用提问", url: "https://example.com/{{input.query}}",
			body: `{"q":"{{input.query}}"}`, wantInErr: "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			httpConfig, _ := json.Marshal(appTypes.WorkflowHTTPNodeConfig{
				Method: "POST", URL: testCase.url, Headers: testCase.headers, BodyTemplate: testCase.body,
			})
			endConfig, _ := json.Marshal(appTypes.WorkflowEndNodeConfig{TextTemplate: "{{input.query}}"})
			definition := &appTypes.WorkflowDefinition{
				Version: appTypes.WorkflowVersion,
				Nodes: []appTypes.WorkflowNode{
					{ID: "start", Type: appTypes.WorkflowNodeTypeStart, Name: "开始",
						Position: appTypes.WorkflowPosition{}, Config: json.RawMessage(`{}`)},
					{ID: "call", Type: appTypes.WorkflowNodeTypeHTTP, Name: "调用接口",
						Position: appTypes.WorkflowPosition{X: 1}, Config: httpConfig},
					{ID: "end", Type: appTypes.WorkflowNodeTypeEnd, Name: "输出",
						Position: appTypes.WorkflowPosition{X: 2}, Config: endConfig},
				},
				Edges: []appTypes.WorkflowEdge{
					{ID: "start-call", Source: "start", Target: "call"},
					{ID: "call-end", Source: "call", Target: "end"},
				},
				Viewport: appTypes.WorkflowViewport{Zoom: 1},
			}
			err := Validate(definition)
			if testCase.wantInErr == "" {
				if err != nil {
					t.Fatalf("expected valid workflow, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), testCase.wantInErr) {
				t.Fatalf("error should mention %q, got: %v", testCase.wantInErr, err)
			}
		})
	}
}

// TestValidateAcceptsStatusVariable 确认 nodes.<id>.status 在模板中可用，
// 与执行期把失败节点也写入变量表的行为配套。
func TestValidateAcceptsStatusVariable(t *testing.T) {
	definition := buildDefinition("{{input.query}}", "{{nodes.find.status}}")
	if err := Validate(definition); err != nil {
		t.Fatalf("status should be a valid template variable, got error: %v", err)
	}
}

// TestValidateIgnoresPlaceholdersOutsideVariableSpace 确认渲染正则不认识的占位符
// （例如前缀写错）会被原样保留，校验不会把它误判成合法变量引用而放行。
func TestValidateIgnoresPlaceholdersOutsideVariableSpace(t *testing.T) {
	definition := buildDefinition("{{input.query}}", "{{nodez.find.text}}")
	// 该占位符不匹配 workflowTemplateRE，因此渲染期不会替换它；校验期同样不应
	// 把它当成 nodes.* 变量。这里只要求校验不因为一个未知前缀而 panic 或误报。
	if err := Validate(definition); err != nil {
		t.Fatalf("unrecognized placeholder should not fail validation, got: %v", err)
	}
}
