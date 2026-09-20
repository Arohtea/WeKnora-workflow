package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	workflowruntime "github.com/Tencent/WeKnora/internal/workflow"
)

// SaveWorkflowDraft 以乐观并发控制保存工作流草稿。
//
// 保存带期望修订号是草稿模型的核心约束：编辑器可能长时间开着，用户 A 的保存不能
// 悄悄覆盖用户 B 在同一期间的改动。修订号不匹配时返回
// repository.ErrWorkflowRevisionConflict，由 HTTP 层翻译成 409 并带回当前修订号，
// 让前端刷新而不是覆盖。
//
// 这里刻意不调用 PublishWorkflow 的严格校验：草稿允许处于中间状态（未完成的连线、
// 待补的节点配置），拦截它会让用户无法保存正在编辑的内容。严格校验只发生在发布。
//
// @param ctx 当前请求上下文。
// @param agentID 工作流智能体 ID。
// @param expectedRevision 客户端读取草稿时看到的修订号。
// @param agent 待保存的名称、描述与配置。
// @param avatar 头像字段的存在性：nil 表示未提交，需保留原值。
// @returns 保存后的智能体（含递增后的修订号）。
func (s *agentService) SaveWorkflowDraft(
	ctx context.Context,
	agentID string,
	expectedRevision int64,
	agent *types.CustomAgent,
	avatar *string,
) (*types.CustomAgent, error) {
	if agent == nil {
		return nil, fmt.Errorf("workflow draft agent is required")
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	agentRepo := repository.NewCustomAgentRepository(s.db)
	existing, err := agentRepo.GetAgentByID(ctx, agentID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, err
	}
	if existing.IsBuiltin {
		return nil, ErrCannotModifyBuiltin
	}
	if strings.TrimSpace(agent.Name) == "" {
		return nil, ErrAgentNameRequired
	}

	// 配置先归一化再落库：迁移会在这一步把 v1 定义补成 v2 并补齐分支模式，
	// 保证后续读取者面对的始终是当前格式。草稿允许结构不完整，因此只做归一化
	// 而不做发布级的严格校验。
	merged := *existing
	merged.Name = agent.Name
	merged.Description = agent.Description
	if avatar != nil {
		if err := (&types.CustomAgent{Avatar: *avatar}).ValidateAvatar(); err != nil {
			return nil, err
		}
		merged.Avatar = *avatar
	}
	if agent.Config.AgentType != "" {
		merged.Config = agent.Config
	}
	if err := workflowruntime.NormalizeDraftConfig(&merged.Config); err != nil {
		// 归一化失败意味着节点结构本身无法解析（例如配置不是合法 JSON）。
		// 这类草稿无法被任何执行器理解，拒绝保存比写入一份坏数据更安全。
		return nil, err
	}
	if err := merged.Config.QuestionSuggestions.Validate(); err != nil {
		return nil, err
	}
	merged.UpdatedAt = time.Now()

	if err := s.workflowRepo.SaveDraft(ctx, tenantID, agentID, expectedRevision, &merged); err != nil {
		return nil, err
	}
	return &merged, nil
}
