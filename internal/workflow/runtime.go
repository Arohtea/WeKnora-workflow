package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	appTypes "github.com/Tencent/WeKnora/internal/types"
)

var workflowTemplateRE = regexp.MustCompile(`\{\{\s*(input\.(?:query|attachments_text)|nodes\.[A-Za-z0-9_-]+\.(?:text|status|data(?:\.[A-Za-z0-9_-]+)*))\s*\}\}`)

// EvaluateCondition 使用 CEL 程序计算一个结构化工作流条件。
//
// @param condition 已验证的结构化条件。
// @param variables 包含 input 和 nodes 的运行时变量树。
// @returns 条件是否成立；变量缺失或 CEL 类型不匹配时返回错误。
func EvaluateCondition(condition *appTypes.WorkflowCondition, variables map[string]interface{}) (bool, error) {
	if condition == nil || len(condition.Items) == 0 {
		return true, nil
	}
	program, err := CompileCondition(condition)
	if err != nil {
		return false, err
	}
	lefts := make([]interface{}, 0, len(condition.Items))
	rights := make([]interface{}, 0, len(condition.Items))
	empties := make([]bool, 0, len(condition.Items))
	for _, item := range condition.Items {
		value, ok := ResolveVariable(variables, item.Variable)
		if !ok {
			return false, fmt.Errorf("workflow variable %s is unavailable on this branch", item.Variable)
		}
		lefts = append(lefts, value)
		rights = append(rights, item.Value)
		empties = append(empties, isEmptyValue(value))
	}
	out, _, err := program.Eval(map[string]interface{}{
		"lefts":   lefts,
		"rights":  rights,
		"empties": empties,
	})
	if err != nil {
		return false, fmt.Errorf("evaluate workflow condition: %w", err)
	}
	matched, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("workflow condition did not return a boolean")
	}
	return matched, nil
}

// ResolveVariable 从固定变量空间读取一个值。
//
// @param variables 包含 input 和 nodes 的运行时变量树。
// @param path 固定格式的变量路径。
// @returns 变量值以及是否存在。
func ResolveVariable(variables map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return nil, false
	}
	var current interface{} = variables
	for _, part := range parts {
		switch value := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = value[part]
			if !ok {
				return nil, false
			}
		case appTypes.WorkflowNodeOutput:
			switch part {
			case "text":
				current = value.Text
			case "status":
				current = value.Status
			case "data":
				current = value.Data
			default:
				return nil, false
			}
		case *appTypes.WorkflowNodeOutput:
			if value == nil {
				return nil, false
			}
			switch part {
			case "text":
				current = value.Text
			case "status":
				current = value.Status
			case "data":
				current = value.Data
			default:
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return current, true
}

// RenderTemplate 替换字符串中的工作流变量占位符。
//
// @param template 可包含 {{input.query}} 或 {{nodes.<id>.*}} 的字符串。
// @param variables 当前分支变量树。
// @returns 渲染后的文本；变量缺失时返回错误。
func RenderTemplate(template string, variables map[string]interface{}) (string, error) {
	var renderErr error
	rendered := workflowTemplateRE.ReplaceAllStringFunc(template, func(match string) string {
		if renderErr != nil {
			return ""
		}
		parts := workflowTemplateRE.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		value, ok := ResolveVariable(variables, parts[1])
		if !ok {
			renderErr = fmt.Errorf("workflow variable %s is unavailable on this branch", parts[1])
			return ""
		}
		return stringifyTemplateValue(value)
	})
	if renderErr != nil {
		return "", renderErr
	}
	return rendered, nil
}

// RenderValue 递归渲染工具参数中的字符串占位符。
//
// @param value JSON 兼容的参数值。
// @param variables 当前分支变量树。
// @returns 渲染后的 JSON 兼容值；变量缺失时返回错误。
func RenderValue(value interface{}, variables map[string]interface{}) (interface{}, error) {
	switch item := value.(type) {
	case string:
		trimmed := strings.TrimSpace(item)
		match := workflowTemplateRE.FindStringSubmatch(trimmed)
		if len(match) >= 2 && match[0] == trimmed {
			resolved, ok := ResolveVariable(variables, match[1])
			if !ok {
				return nil, fmt.Errorf("workflow variable %s is unavailable on this branch", match[1])
			}
			return resolved, nil
		}
		return RenderTemplate(item, variables)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(item))
		for key, child := range item {
			rendered, err := RenderValue(child, variables)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(item))
		for index, child := range item {
			rendered, err := RenderValue(child, variables)
			if err != nil {
				return nil, err
			}
			out[index] = rendered
		}
		return out, nil
	default:
		return value, nil
	}
}

func stringifyTemplateValue(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err == nil {
			return string(encoded)
		}
		return fmt.Sprint(typed)
	}
}

// SortedOutgoingEdges 返回按 order 和边 ID 稳定排序的出边。
//
// @param edges 待排序的边集合。
// @returns 不修改输入切片的排序副本。
func SortedOutgoingEdges(edges []appTypes.WorkflowEdge) []appTypes.WorkflowEdge {
	out := append([]appTypes.WorkflowEdge(nil), edges...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order == out[j].Order {
			return out[i].ID < out[j].ID
		}
		return out[i].Order < out[j].Order
	})
	return out
}

// CloneVariables 为并行分支复制变量树的可变顶层映射。
//
// @param variables 当前分支变量树。
// @returns input 共享只读、nodes 独立复制的新变量树。
func CloneVariables(variables map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(variables))
	for key, value := range variables {
		if key != "nodes" {
			out[key] = value
			continue
		}
		nodes, _ := value.(map[string]interface{})
		cloned := make(map[string]interface{}, len(nodes))
		for nodeID, output := range nodes {
			cloned[nodeID] = output
		}
		out[key] = cloned
	}
	return out
}
