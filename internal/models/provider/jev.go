package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// JevBaseURL TypeSafe Jev 官方 API BaseURL (System One)
	JevBaseURL = "https://api.typesafe.ai/v1/systemone"
	// ProviderJev Jev 结构化概率决策与分类模型服务商
	ProviderJev ProviderName = "jev"
)

// JevProvider 实现 TypeSafe Jev 的 Provider 接口
type JevProvider struct{}

func init() {
	Register(&JevProvider{})
}

// Info 返回 Jev provider 的元数据
func (p *JevProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderJev,
		DisplayName: "Jev (TypeSafe)",
		Description: "TypeSafe Jev (System One) 结构化概率决策与分类模型，专用于工作流分支判断",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: JevBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Jev provider 配置
func (p *JevProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Jev provider")
	}
	if config.ModelName == "" {
		config.ModelName = "jev-latest"
	}
	return nil
}
