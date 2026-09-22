package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
)

// JevChat 实现 TypeSafe Jev (System One) 结构化概率决策与分类模型客户端
type JevChat struct {
	baseURL    string
	apiKey     string
	modelName  string
	modelID    string
	httpClient *http.Client
}

// JevQuestionWire 表示发送给 Jev API 的问句定义
type JevQuestionWire struct {
	Type         string      `json:"type"`               // noul | choice | score
	Instructions string      `json:"instructions"`       // 问句自然语言描述
	Criteria     interface{} `json:"criteria,omitempty"` // choice: map[string]string, score: []string
}

// JevRequestWire 表示 Jev API 请求体
type JevRequestWire struct {
	State     string                     `json:"state"`
	Model     string                     `json:"model"`
	Questions map[string]JevQuestionWire `json:"questions"`
}

// JevAnswerWire 表示 Jev API 返回的单个问句答案
type JevAnswerWire struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

// JevResponseWire 表示 Jev API 响应体
type JevResponseWire struct {
	Model   string                   `json:"model"`
	Answers map[string]JevAnswerWire `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Detail *struct {
		Message string `json:"message"`
	} `json:"detail,omitempty"`
}

// JevDecisionResult 结构化决策结果
type JevDecisionResult struct {
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
	Model         string
	Usage         types.TokenUsage
}

// JevDecisionCaller 接口，标识支持 Jev 原生结构化决策的模型
type JevDecisionCaller interface {
	Decision(ctx context.Context, state string, choices []string) (*JevDecisionResult, error)
}

// NewJevChat 创建 Jev 客户端实例
func NewJevChat(config *ChatConfig) (*JevChat, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = provider.JevBaseURL
	}
	modelName := strings.TrimSpace(config.ModelName)
	if modelName == "" {
		modelName = "jev-latest"
	}

	return &JevChat{
		baseURL:   baseURL,
		apiKey:    strings.TrimSpace(config.APIKey),
		modelName: modelName,
		modelID:   config.ModelID,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

func (j *JevChat) GetModelName() string {
	return j.modelName
}

func (j *JevChat) GetModelID() string {
	return j.modelID
}

// Decision 原生结构化分支决策方法
func (j *JevChat) Decision(ctx context.Context, state string, choices []string) (*JevDecisionResult, error) {
	if len(choices) < 2 {
		return nil, fmt.Errorf("jev decision requires at least 2 choices")
	}

	criteria := make(map[string]string, len(choices))
	for _, c := range choices {
		trimmed := strings.TrimSpace(c)
		if trimmed != "" {
			criteria[trimmed] = trimmed
		}
	}

	questions := map[string]JevQuestionWire{
		"branch_decision": {
			Type:         "choice",
			Instructions: "请根据输入内容在候选标签中选择最匹配的一项",
			Criteria:     criteria,
		},
	}

	respWire, err := j.callJevAPI(ctx, state, questions)
	if err != nil {
		return nil, err
	}

	ans, ok := respWire.Answers["branch_decision"]
	if !ok {
		// 容错取任意一个 answer
		for _, a := range respWire.Answers {
			ans = a
			break
		}
	}

	confidence := 0.0
	if ans.Confidence != nil {
		confidence = *ans.Confidence
	}

	usage := types.TokenUsage{
		PromptTokens:     respWire.Usage.InputTokens,
		CompletionTokens: respWire.Usage.OutputTokens,
		TotalTokens:      respWire.Usage.InputTokens + respWire.Usage.OutputTokens,
	}

	return &JevDecisionResult{
		Choice:        ans.Choice,
		Confidence:    confidence,
		Probabilities: ans.Probabilities,
		Model:         respWire.Model,
		Usage:         usage,
	}, nil
}

// Chat 通用聊天适配方法（兼容连通性测试与一般调用）
func (j *JevChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	var stateBuilder strings.Builder
	var choices []string

	// 从 messages 提取 state 与候选标签（如有）
	for _, msg := range messages {
		if msg.Role == "system" {
			// 尝试从 system 提示词中解析 choices（例如 workflow 的 "候选标签：A, B"）
			parsed := extractChoicesFromSystem(msg.Content)
			if len(parsed) >= 2 {
				choices = parsed
			}
		} else if msg.Role == "user" {
			if stateBuilder.Len() > 0 {
				stateBuilder.WriteString("\n\n")
			}
			stateBuilder.WriteString(msg.Content)
		}
	}

	state := strings.TrimSpace(stateBuilder.String())
	if state == "" {
		state = "ping"
	}

	// 如果提取到了 choices，走选择题分支；否则走是非/质量问句（支持连通性测试）
	var questions map[string]JevQuestionWire
	if len(choices) >= 2 {
		criteria := make(map[string]string, len(choices))
		for _, c := range choices {
			criteria[c] = c
		}
		questions = map[string]JevQuestionWire{
			"branch_decision": {
				Type:         "choice",
				Instructions: "请根据输入内容在候选标签中选择最符合的一项",
				Criteria:     criteria,
			},
		}
	} else {
		questions = map[string]JevQuestionWire{
			"evaluate": {
				Type:         "noul",
				Instructions: "待判断内容是否有效？",
			},
		}
	}

	respWire, err := j.callJevAPI(ctx, state, questions)
	if err != nil {
		return nil, err
	}

	usage := types.TokenUsage{
		PromptTokens:     respWire.Usage.InputTokens,
		CompletionTokens: respWire.Usage.OutputTokens,
		TotalTokens:      respWire.Usage.InputTokens + respWire.Usage.OutputTokens,
	}

	// 组装格式化响应：优先输出包含 choice、confidence 的 JSON，满足工作流与通用场景
	var content string
	if ans, ok := respWire.Answers["branch_decision"]; ok && ans.Choice != "" {
		conf := 0.0
		if ans.Confidence != nil {
			conf = *ans.Confidence
		}
		resultMap := map[string]interface{}{
			"choice":        ans.Choice,
			"confidence":    conf,
			"probabilities": ans.Probabilities,
			"reason":        "Jev System One 概率判断模型评估得出",
		}
		bytes, _ := json.Marshal(resultMap)
		content = string(bytes)
	} else {
		// 连通性测试或一般输出
		resultMap := map[string]interface{}{
			"model":   respWire.Model,
			"answers": respWire.Answers,
		}
		bytes, _ := json.Marshal(resultMap)
		content = string(bytes)
	}

	return &types.ChatResponse{
		Content: content,
		Usage:   usage,
	}, nil
}

// ChatStream 流式调用适配（以单包流式输出，保持对流式框架的透明兼容）
func (j *JevChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	resp, err := j.Chat(ctx, messages, opts)
	if err != nil {
		return nil, err
	}

	out := make(chan types.StreamResponse, 1)
	go func() {
		defer close(out)
		out <- types.StreamResponse{
			Content:      resp.Content,
			Usage:        &resp.Usage,
			ResponseType: types.ResponseTypeAnswer,
		}
	}()
	return out, nil
}

// callJevAPI 裸调 TypeSafe Jev HTTP API，不隐藏任何原始请求/响应
func (j *JevChat) callJevAPI(ctx context.Context, state string, questions map[string]JevQuestionWire) (*JevResponseWire, error) {
	if j.apiKey == "" {
		return nil, fmt.Errorf("Jev API key is empty")
	}

	reqBody := JevRequestWire{
		State:     state,
		Model:     j.modelName,
		Questions: questions,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Jev request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, j.baseURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create Jev request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+j.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := j.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call Jev upstream: %w", err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Jev response body: %w", err)
	}

	var parsed JevResponseWire
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, fmt.Errorf("Jev upstream returned invalid JSON (HTTP %d): %s", httpResp.StatusCode, string(respBytes))
	}

	if httpResp.StatusCode >= 400 {
		errMsg := fmt.Sprintf("Jev API error (HTTP %d)", httpResp.StatusCode)
		if parsed.Error != nil && parsed.Error.Message != "" {
			errMsg = parsed.Error.Message
		} else if parsed.Detail != nil && parsed.Detail.Message != "" {
			errMsg = parsed.Detail.Message
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	return &parsed, nil
}

// extractChoicesFromSystem 从 system 提示词中提取候选分支列表
func extractChoicesFromSystem(systemPrompt string) []string {
	markers := []string{"候选标签：", "候选标签:", "候选标签 :", "candidates:", "choices:"}
	for _, marker := range markers {
		if idx := strings.Index(systemPrompt, marker); idx != -1 {
			sub := systemPrompt[idx+len(marker):]
			// 截取到句末或换行
			if endIdx := strings.IndexAny(sub, "\n。"); endIdx != -1 {
				sub = sub[:endIdx]
			}
			parts := strings.Split(sub, ",")
			if len(parts) < 2 {
				parts = strings.Split(sub, "，")
			}
			choices := make([]string, 0, len(parts))
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					choices = append(choices, trimmed)
				}
			}
			if len(choices) >= 2 {
				return choices
			}
		}
	}
	return nil
}
