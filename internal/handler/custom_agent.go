package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	workflowruntime "github.com/Tencent/WeKnora/internal/workflow"
	"github.com/gin-gonic/gin"
)

// sandboxConfigLookup is the existence check an agent's sandbox selection needs.
// Narrower than the full config service so this handler cannot grow a dependency
// on config mutation.
type sandboxConfigLookup interface {
	Get(ctx context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error)
}

type workflowDraftSaver interface {
	SaveWorkflowDraft(ctx context.Context, agentID string, expectedRevision int64, agent *types.CustomAgent, avatar *string) (*types.CustomAgent, error)
}

type workflowAgentProvider interface {
	ValidateWorkflowResources(ctx context.Context, config *types.CustomAgentConfig) error
	WorkflowCatalog(ctx context.Context, agent *types.CustomAgent) (*types.WorkflowCatalog, error)
}

type workflowImportPreviewer interface {
	PreviewWorkflowImport(ctx context.Context, agentID string, payload types.JSON) (*types.WorkflowImportPreview, error)
}

type workflowRunCommands interface {
	StartWorkflowDebugRun(ctx context.Context, agentID string, expectedRevision int64, input types.WorkflowDebugInput, idempotencyKey string) (*types.WorkflowRun, error)
	RetryWorkflowRun(ctx context.Context, agentID, runID string) (*types.WorkflowRun, error)
	RetryWorkflowNode(ctx context.Context, agentID, runID string, nodeRunID int64) (*types.WorkflowRun, error)
}

// CustomAgentHandler defines the HTTP handler for custom agent operations
type CustomAgentHandler struct {
	service      interfaces.CustomAgentService
	imService    *im.Service
	disabledRepo interfaces.TenantDisabledSharedAgentRepository
	// userService 仅用于 list 接口批量回填 creator_name，作用见
	// KnowledgeBaseHandler.userService。
	userService interfaces.UserService
	// sandboxConfigs validates an agent's sandbox backend selection. Optional —
	// nil in partially-wired unit tests, where the selection is left unchecked.
	sandboxConfigs sandboxConfigLookup
	// agentRuntime is optional in handler-only tests and exposes the narrow
	// workflow runtime without expanding the public AgentService interface.
	agentRuntime interfaces.AgentService
}

// NewCustomAgentHandler creates a new custom agent handler instance
func NewCustomAgentHandler(
	service interfaces.CustomAgentService,
	imService *im.Service,
	disabledRepo interfaces.TenantDisabledSharedAgentRepository,
	userService interfaces.UserService,
	sandboxConfigs *service.TenantSandboxConfigService,
	agentRuntime interfaces.AgentService,
) *CustomAgentHandler {
	return &CustomAgentHandler{
		service:        service,
		imService:      imService,
		disabledRepo:   disabledRepo,
		userService:    userService,
		sandboxConfigs: sandboxConfigs,
		agentRuntime:   agentRuntime,
	}
}

// CreateAgentRequest defines the request body for creating an agent
type CreateAgentRequest struct {
	Name        string                  `json:"name" binding:"required"`
	Description string                  `json:"description"`
	Avatar      string                  `json:"avatar"`
	Config      types.CustomAgentConfig `json:"config"`
}

// UpdateAgentRequest defines the request body for updating an agent
type UpdateAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Avatar travels as a pointer so an omitted field can be told apart from
	// an explicit clear: nil keeps the stored avatar, a pointer to "" wipes
	// it. As a plain string the two cases were indistinguishable, so a caller
	// that PUT only a config silently zeroed the avatar and still got a 200.
	Avatar *string `json:"avatar"`
	// Config travels as a pointer for the same reason as avatar: nil means the
	// caller did not send a configuration, so the stored one must survive. As a
	// value type an omitted config became a zero struct, and for a workflow
	// agent that replaced the stored graph with the default definition.
	Config           *types.CustomAgentConfig `json:"config"`
	ExpectedRevision *int64                   `json:"expected_revision"`
}

// CreateAgent godoc
// @Summary      创建智能体
// @Description  创建新的自定义智能体
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        request  body      CreateAgentRequest  true  "智能体信息"
// @Success      201      {object}  map[string]interface{}  "创建的智能体"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents [post]
func (h *CustomAgentHandler) CreateAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating custom agent")

	// Parse request body
	var req CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if err := workflowruntime.NormalizeDraftConfig(&req.Config); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if err := authorizeAgentKnowledgeScope(ctx, req.Config); err != nil {
		c.Error(err)
		return
	}
	if req.Config.AgentType != types.AgentTypeWorkflow {
		if err := h.validateAgentSandboxConfig(ctx, req.Config); err != nil {
			c.Error(err)
			return
		}
	}

	// Build agent object
	agent := &types.CustomAgent{
		Name:        req.Name,
		Description: req.Description,
		Avatar:      req.Avatar,
		Config:      req.Config,
	}
	agent.EnsureDefaults()
	// The DB column (varchar(64)) has no application-level guard, so an
	// oversized avatar used to reach postgres and come back as a raw driver
	// 500. Reject it here with a 400 that names the limit.
	if err := agent.ValidateAvatar(); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if err := agent.Config.QuestionSuggestions.Validate(); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Creating custom agent, name: %s, agent_mode: %s",
		secutils.SanitizeForLog(req.Name), req.Config.AgentMode)

	// Create agent using the service
	createdAgent, err := h.service.CreateAgent(ctx, agent)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		if err == service.ErrAgentNameRequired {
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		// Reached only after the typed sentinels and *errors.AppError
		// above, so whatever lands here is a raw repository/driver error.
		// Its text (SQLSTATE, column types) must not reach the client;
		// the full detail is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to create agent"))
		return
	}

	logger.Infof(ctx, "Custom agent created successfully, ID: %s, name: %s",
		secutils.SanitizeForLog(createdAgent.ID), secutils.SanitizeForLog(createdAgent.Name))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    createdAgent,
	})
}

// GetAgent godoc
// @Summary      获取智能体详情
// @Description  根据ID获取智能体详情
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"
// @Success      200  {object}  map[string]interface{}  "智能体详情"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id} [get]
func (h *CustomAgentHandler) GetAgent(c *gin.Context) {
	ctx := c.Request.Context()

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	agent, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		if err == service.ErrAgentNotFound {
			c.Error(errors.NewNotFoundError("Agent not found"))
			return
		}
		if appErr, ok := err.(*errors.AppError); ok {
			c.Error(appErr)
			return
		}
		// Reached only after the typed sentinels and *errors.AppError above, so
		// whatever lands here is a raw repository/driver error. Its text
		// (SQLSTATE, column names) must not reach the client; the full detail
		// is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to load agent"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    agent,
	})
}

// ListAgents godoc
// @Summary      获取智能体列表
// @Description  获取当前空间的所有智能体（包括内置智能体）
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "智能体列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents [get]
func (h *CustomAgentHandler) ListAgents(c *gin.Context) {
	ctx := c.Request.Context()

	// Get all agents for this tenant
	agents, err := h.service.ListAgents(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		// Reached only after the typed sentinels and *errors.AppError above, so
		// whatever lands here is a raw repository/driver error. Its text
		// (SQLSTATE, column names) must not reach the client; the full detail
		// is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to list agents"))
		return
	}

	// Optional creator filter — see the matching block in
	// KnowledgeBaseHandler.ListKnowledgeBases for rationale. Built-in
	// agents (IsBuiltin=true, CreatedBy="") are tenant-level fixtures
	// rather than user creations; we always keep them regardless of the
	// filter so the conversation dropdown never silently loses
	// quick-answer / smart-reasoning when a user picks "Created by me".
	creatorFilter := strings.ToLower(strings.TrimSpace(c.Query("creator")))
	if creatorFilter == "mine" || creatorFilter == "others" {
		callerUserID, _ := c.Get(types.UserIDContextKey.String())
		callerUserIDStr, _ := callerUserID.(string)
		filtered := make([]*types.CustomAgent, 0, len(agents))
		for _, ag := range agents {
			if ag.IsBuiltin {
				filtered = append(filtered, ag)
				continue
			}
			if ag.CreatedBy == "" {
				continue
			}
			if creatorFilter == "mine" && ag.CreatedBy == callerUserIDStr {
				filtered = append(filtered, ag)
			} else if creatorFilter == "others" && ag.CreatedBy != callerUserIDStr {
				filtered = append(filtered, ag)
			}
		}
		agents = filtered
	}

	// Per-tenant "disabled by me" for own agents (only affects this tenant's conversation dropdown)
	tenantIDVal, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Workspace ID not found in context")
		c.Error(errors.NewUnauthorizedError("Missing workspace context"))
		return
	}
	tenantID, ok := tenantIDVal.(uint64)
	if !ok {
		logger.Errorf(ctx, "Tenant ID has unexpected type %T in context", tenantIDVal)
		c.Error(errors.NewInternalServerError("Invalid workspace context type"))
		return
	}
	disabledOwnIDs, err := h.disabledRepo.ListDisabledOwnAgentIDs(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		c.Error(errors.NewInternalServerError("Failed to list disabled agent IDs: " + err.Error()))
		return
	}

	// 批量回填 creator_name，作用同 KB 列表：让前端能区分「我创建」与「同空间其他成员」。
	// 内建 agent（IsBuiltin=true, CreatedBy=""）不会有 creator_name，前端按 builtin
	// 分支单独渲染。
	enrichAgentCreatorNames(ctx, h.userService, agents)

	c.JSON(http.StatusOK, gin.H{
		"success":                true,
		"data":                   agents,
		"disabled_own_agent_ids": disabledOwnIDs,
	})
}

// enrichAgentCreatorNames 批量把 agent.CreatedBy 解析成展示名。失败吞掉，
// 不影响列表本身可用。与 enrichKBCreatorNames 行为对齐。
func enrichAgentCreatorNames(ctx context.Context, userSvc interfaces.UserService, agents []*types.CustomAgent) {
	if userSvc == nil || len(agents) == 0 {
		return
	}
	idSet := make(map[string]struct{}, len(agents))
	for _, ag := range agents {
		if ag.IsBuiltin || ag.CreatedBy == "" {
			continue
		}
		idSet[ag.CreatedBy] = struct{}{}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := userSvc.GetUsersByIDs(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "Failed to resolve agent creator names: %v", err)
		return
	}
	for _, ag := range agents {
		if ag.IsBuiltin || ag.CreatedBy == "" {
			continue
		}
		u, ok := users[ag.CreatedBy]
		if !ok || u == nil {
			continue
		}
		ag.CreatorName = pickUserDisplayName(u)
	}
}

// UpdateAgent godoc
// @Summary      更新智能体
// @Description  更新智能体的名称、描述和配置
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id       path      string              true  "智能体ID"
// @Param        request  body      UpdateAgentRequest  true  "更新请求"
// @Success      200      {object}  map[string]interface{}  "更新后的智能体"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      403      {object}  errors.AppError         "无法修改内置智能体"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id} [put]
func (h *CustomAgentHandler) UpdateAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating custom agent")

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	// Parse request body
	var req UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	// Configuration-scoped validation only runs when the caller actually sent a
	// configuration. Running it against a nil config would validate the zero
	// value and could reject a metadata-only PUT that should be a no-op.
	if req.Config != nil {
		if err := workflowruntime.NormalizeDraftConfig(req.Config); err != nil {
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		if err := authorizeAgentKnowledgeScope(ctx, *req.Config); err != nil {
			c.Error(err)
			return
		}
		if req.Config.AgentType != types.AgentTypeWorkflow {
			if err := h.validateAgentSandboxConfig(ctx, *req.Config); err != nil {
				c.Error(err)
				return
			}
		}
	}

	// Only a sent avatar is validated: nil means the caller never touched the
	// field, so there is no value to bound. Checking the length here is what
	// turns an oversized avatar into a 400 that names the limit, instead of
	// the 500 that used to carry the database's own varchar(64) complaint.
	if req.Avatar != nil {
		if err := (&types.CustomAgent{Avatar: *req.Avatar}).ValidateAvatar(); err != nil {
			logger.Error(ctx, "Invalid avatar", err)
			_ = c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
	}

	// Build agent object. Avatar and Config are deliberately absent here — both
	// reach the service as separate pointers, so that "not sent" survives the
	// trip. The name and description are the only fields this PUT must carry.
	agent := &types.CustomAgent{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
	}
	if req.Config != nil {
		agent.Config = *req.Config
	}
	agent.EnsureDefaults()
	if err := agent.Config.QuestionSuggestions.Validate(); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating custom agent, ID: %s, name: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(req.Name))

	var updatedAgent *types.CustomAgent
	var err error
	if req.Config != nil && req.Config.AgentType == types.AgentTypeWorkflow {
		if req.ExpectedRevision == nil {
			c.Error(errors.NewBadRequestError("expected_revision is required for workflow updates"))
			return
		}
		saver, ok := h.agentRuntime.(workflowDraftSaver)
		if !ok {
			c.Error(errors.NewInternalServerError("Workflow draft persistence is unavailable"))
			return
		}
		updatedAgent, err = saver.SaveWorkflowDraft(ctx, id, *req.ExpectedRevision, agent, req.Avatar)
	} else {
		updatedAgent, err = h.service.UpdateAgent(ctx, agent, req.Avatar, req.Config)
	}

	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch {
		case stderrors.Is(err, repository.ErrWorkflowRevisionConflict):
			c.Error(errors.NewConflictError("Workflow draft was modified by another update; refresh and retry").
				WithDetails(gin.H{"current_revision": h.currentDraftRevision(ctx, id)}))
		case stderrors.Is(err, service.ErrAgentNotFound):
			c.Error(errors.NewNotFoundError("Agent not found"))
		case stderrors.Is(err, service.ErrCannotModifyBuiltin):
			c.Error(errors.NewForbiddenError("Cannot modify built-in agent"))
		case stderrors.Is(err, service.ErrAgentNameRequired):
			c.Error(errors.NewBadRequestError(err.Error()))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to update agent"))
		}
		return
	}

	logger.Infof(ctx, "Custom agent updated successfully, ID: %s", secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedAgent,
	})
}

// DeleteAgent godoc
// @Summary      删除智能体
// @Description  删除指定的智能体
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "无法删除内置智能体"
// @Failure      404  {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id} [delete]
func (h *CustomAgentHandler) DeleteAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting custom agent")

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Deleting custom agent, ID: %s", secutils.SanitizeForLog(id))

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	if err := h.imService.DeleteChannelsByAgent(id, tenantID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		c.Error(errors.NewInternalServerError("Failed to delete agent IM channels"))
		return
	}

	// Delete the agent
	err := h.service.DeleteAgent(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		case service.ErrCannotDeleteBuiltin:
			c.Error(errors.NewForbiddenError("Cannot delete built-in agent"))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to delete agent"))
		}
		return
	}

	logger.Infof(ctx, "Custom agent deleted successfully, ID: %s", secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Agent deleted successfully",
	})
}

// CopyAgent godoc
// @Summary      复制智能体
// @Description  复制指定的智能体
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"
// @Success      201  {object}  map[string]interface{}  "复制成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/copy [post]
func (h *CustomAgentHandler) CopyAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start copying custom agent")

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Copying custom agent, ID: %s", secutils.SanitizeForLog(id))
	sourceAgent, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to copy agent"))
		}
		return
	}
	if err := workflowruntime.NormalizeDraftConfig(&sourceAgent.Config); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if err := authorizeAgentKnowledgeScope(ctx, sourceAgent.Config); err != nil {
		c.Error(err)
		return
	}

	// Copy the agent
	copiedAgent, err := h.service.CopyAgent(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to copy agent"))
		}
		return
	}

	logger.Infof(ctx, "Custom agent copied successfully, source ID: %s, new ID: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(copiedAgent.ID))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    copiedAgent,
	})
}

// GetPlaceholders godoc
// @Summary      获取占位符定义
// @Description  获取所有可用的提示词占位符定义，按字段类型分组
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "占位符定义"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/placeholders [get]
func (h *CustomAgentHandler) GetPlaceholders(c *gin.Context) {
	// Return all placeholder definitions grouped by field type
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"all":                   types.AllPlaceholders(),
			"system_prompt":         types.PlaceholdersByField(types.PromptFieldSystemPrompt),
			"agent_system_prompt":   types.PlaceholdersByField(types.PromptFieldAgentSystemPrompt),
			"context_template":      types.PlaceholdersByField(types.PromptFieldContextTemplate),
			"rewrite_system_prompt": types.PlaceholdersByField(types.PromptFieldRewriteSystemPrompt),
			"rewrite_prompt":        types.PlaceholdersByField(types.PromptFieldRewritePrompt),
			"fallback_prompt":       types.PlaceholdersByField(types.PromptFieldFallbackPrompt),
		},
	})
}

// GetAgentTypePresets godoc
// @Summary      获取智能体类型预设列表
// @Description  返回所有 smart-reasoning 下可用的智能体类型预设（RAG/Wiki/Hybrid/Custom），用于编辑器自动填充系统提示词、工具和 KB 兼容性
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "预设列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/type-presets [get]
func (h *CustomAgentHandler) GetAgentTypePresets(c *gin.Context) {
	ctx := c.Request.Context()
	presets := types.ListAgentTypePresetsWithContext(ctx)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    presets,
	})
}

// GetWorkflowCatalog godoc
// @Summary      获取工作流资源目录
// @Description  返回当前智能体有权使用的内置工具、MCP 工具和已安装 Skill
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"
// @Success      200  {object}  map[string]interface{}  "工作流资源目录"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/workflow/catalog [get]
func (h *CustomAgentHandler) GetWorkflowCatalog(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}
	provider, ok := h.agentRuntime.(workflowAgentProvider)
	if !ok {
		c.Error(errors.NewInternalServerError("Workflow runtime is unavailable"))
		return
	}
	agent, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		if err == service.ErrAgentNotFound {
			c.Error(errors.NewNotFoundError("Agent not found"))
			return
		}
		if appErr, ok := err.(*errors.AppError); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		c.Error(errors.NewInternalServerError("Failed to load agent"))
		return
	}
	catalog, err := provider.WorkflowCatalog(ctx, agent)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": catalog})
}

// PublishWorkflowRequest 描述发布工作流时需要客户端回传的乐观锁信息。
// expected_revision 是客户端最后看到的草稿修订号；服务端据此判断是否有他人在
// 此期间改过草稿，避免用陈旧内容静默覆盖别人刚保存的改动。
type PublishWorkflowRequest struct {
	ExpectedRevision int64 `json:"expected_revision" binding:"required"`
}

// ValidateWorkflowRequest 允许两种请求形态：直接提交完整配置，或以
// {"config": {...}} 包裹。包一层是为了让前端能把编辑器中未保存的草稿原样送去
// 校验，而不必先落库。
type ValidateWorkflowRequest struct {
	Config *types.CustomAgentConfig `json:"config"`
}

// RestoreWorkflowVersionRequest 描述恢复版本时的草稿乐观锁信息。
type RestoreWorkflowVersionRequest struct {
	ExpectedRevision int64 `json:"expected_revision" binding:"required"`
}

// WorkflowDebugRunRequest 描述编辑器内试跑的草稿修订和样例输入。
type WorkflowDebugRunRequest struct {
	ExpectedRevision int64                    `json:"expected_revision" binding:"required"`
	Input            types.WorkflowDebugInput `json:"input"`
	IdempotencyKey   string                   `json:"idempotency_key,omitempty"`
}

// WorkflowImportPreviewRequest 包装待预检的原始 JSON，服务端据此识别标准导出包、
// Agent 配置包或纯 WorkflowDefinition。原始内容只用于预检，不会直接落库。
type WorkflowImportPreviewRequest struct {
	Document json.RawMessage `json:"document" binding:"required"`
}

// workflowRunCursor 是运行列表的游标，指向上一页最后一条记录。
// 只用 id 排序会在 started_at 相同时产生翻页抖动，因此游标同时带上
// started_at 和 id，与仓储层的排序键保持一致。
type workflowRunCursor struct {
	StartedAt time.Time `json:"started_at"`
	ID        string    `json:"id"`
}

// PublishWorkflow godoc
// @Summary      发布工作流版本
// @Description  校验当前草稿并创建不可变发布版本。expected_revision 与当前草稿修订号不一致时返回 409，body 中带上服务端当前的 current_revision，客户端必须刷新后重试，禁止静默覆盖
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id       path      string                 true  "智能体ID"
// @Param        request  body      PublishWorkflowRequest true  "发布信息"
// @Success      200      {object}  map[string]interface{} "新发布版本记录"
// @Failure      400      {object}  errors.AppError         "草稿校验失败"
// @Failure      404      {object}  errors.AppError         "智能体不存在"
// @Failure      409      {object}  errors.AppError         "草稿已被他人修改"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/workflow/publish [post]
func (h *CustomAgentHandler) PublishWorkflow(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	var req PublishWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse publish workflow request", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	record, err := h.agentRuntime.PublishWorkflow(ctx, id, req.ExpectedRevision)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		switch {
		case stderrors.Is(err, repository.ErrWorkflowRevisionConflict):
			// 冲突时把服务端当前 revision 一并回传，前端据此刷新编辑器后重试；
			// 不带这个字段前端只能提示"保存失败"，用户无从判断要合并什么。
			c.Error(errors.NewConflictError("Workflow draft was modified by another update; refresh and retry").
				WithDetails(gin.H{"current_revision": h.currentDraftRevision(ctx, id)}))
			return
		case stderrors.Is(err, service.ErrAgentNotFound):
			c.Error(errors.NewNotFoundError("Agent not found"))
			return
		default:
			if appErr, ok := err.(*errors.AppError); ok {
				c.Error(appErr)
				return
			}
			// 非资源类的轻量校验失败（如工作流结构非法）作为 400 返回，
			// 前端把消息展示在发布按钮旁。
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": record})
}

// currentDraftRevision 尽最大努力读取智能体当前草稿修订号，供 409 冲突响应回传。
// 读取失败不改变冲突语义——退化为 0，前端仍可通过重新拉取智能体详情拿到真值。
func (h *CustomAgentHandler) currentDraftRevision(ctx context.Context, agentID string) int64 {
	agent, err := h.service.GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		return 0
	}
	return agent.DraftRevision
}

// ListWorkflowVersions godoc
// @Summary      获取工作流发布历史
// @Description  返回该智能体的不可变发布版本列表，按版本号从新到旧
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"
// @Success      200  {object}  map[string]interface{}  "发布历史"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/workflow/versions [get]
func (h *CustomAgentHandler) ListWorkflowVersions(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	versions, err := h.agentRuntime.ListWorkflowVersions(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		c.Error(errors.NewInternalServerError("Failed to list workflow versions"))
		return
	}
	if versions == nil {
		// 未发布过任何版本时返回空数组而非 null，前端可无条件 map。
		versions = []*types.WorkflowVersionRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": versions})
}

// GetWorkflowVersion godoc
// @Summary      获取指定工作流版本
// @Description  返回指定不可变发布版本；旧版本只读，不能原地修改
// @Tags         智能体
// @Produce      json
// @Param        id       path      string  true  "智能体ID"
// @Param        version  path      int     true  "版本号"
// @Success      200      {object}  map[string]interface{}
// @Failure      404      {object}  errors.AppError
// @Security     Bearer
// @Router       /agents/{id}/workflow/versions/{version} [get]
func (h *CustomAgentHandler) GetWorkflowVersion(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	version, err := strconv.ParseInt(strings.TrimSpace(c.Param("version")), 10, 64)
	if agentID == "" || err != nil || version <= 0 {
		c.Error(errors.NewBadRequestError("Agent ID and positive workflow version are required"))
		return
	}
	record, err := h.agentRuntime.GetWorkflowVersion(ctx, agentID, version)
	if err != nil {
		if stderrors.Is(err, repository.ErrWorkflowVersionNotFound) {
			c.Error(errors.NewNotFoundError("Workflow version not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": agentID, "version": version})
		c.Error(errors.NewInternalServerError("Failed to load workflow version"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": record})
}

// RestoreWorkflowVersion godoc
// @Summary      恢复工作流版本为新草稿
// @Description  把指定不可变版本复制到当前草稿并递增修订号；不会修改旧版本，也不会自动发布
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id       path      string                         true  "智能体ID"
// @Param        version  path      int                            true  "版本号"
// @Param        request  body      RestoreWorkflowVersionRequest  true  "恢复信息"
// @Success      200      {object}  map[string]interface{}
// @Failure      409      {object}  errors.AppError
// @Security     Bearer
// @Router       /agents/{id}/workflow/versions/{version}/restore [post]
func (h *CustomAgentHandler) RestoreWorkflowVersion(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	version, err := strconv.ParseInt(strings.TrimSpace(c.Param("version")), 10, 64)
	if agentID == "" || err != nil || version <= 0 {
		c.Error(errors.NewBadRequestError("Agent ID and positive workflow version are required"))
		return
	}
	var req RestoreWorkflowVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	restored, err := h.agentRuntime.RestoreWorkflowVersion(ctx, agentID, version, req.ExpectedRevision)
	if err != nil {
		switch {
		case stderrors.Is(err, repository.ErrWorkflowRevisionConflict):
			c.Error(errors.NewConflictError("Workflow draft was modified by another update; refresh and retry").
				WithDetails(gin.H{"current_revision": h.currentDraftRevision(ctx, agentID)}))
		case stderrors.Is(err, repository.ErrWorkflowVersionNotFound):
			c.Error(errors.NewNotFoundError("Workflow version not found"))
		case stderrors.Is(err, service.ErrAgentNotFound):
			c.Error(errors.NewNotFoundError("Agent not found"))
		default:
			logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": agentID, "version": version})
			c.Error(errors.NewBadRequestError(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": restored})
}

// ValidateWorkflowDefinition godoc
// @Summary      校验工作流定义
// @Description  对未保存的工作流配置做结构化静态校验，返回可定位到编辑器的 issue 列表（code/message/node_id/edge_id/field_path）。不落库、不改变任何状态
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "智能体ID"
// @Param        request  body      ValidateWorkflowRequest  true  "待校验的工作流配置"
// @Success      200      {object}  map[string]interface{}   "校验问题列表"
// @Failure      400      {object}  errors.AppError          "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/workflow/validate [post]
func (h *CustomAgentHandler) ValidateWorkflowDefinition(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	var req ValidateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse workflow validate request", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	config := req.Config

	issues, err := h.agentRuntime.ValidateWorkflowDefinition(ctx, config)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if issues == nil {
		issues = []types.WorkflowValidationIssue{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": issues})
}

// PreviewWorkflowImport godoc
// @Summary      预检导入工作流
// @Description  服务端迁移导入 schema、脱敏敏感字段并检查当前租户的资源映射；不修改草稿
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id       path      string                         true  "智能体ID"
// @Param        request  body      WorkflowImportPreviewRequest   true  "待预检的原始工作流 JSON"
// @Success      200      {object}  map[string]interface{}         "导入预检结果"
// @Failure      400      {object}  errors.AppError                "导入格式错误"
// @Failure      404      {object}  errors.AppError                "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/workflow/import/preview [post]
func (h *CustomAgentHandler) PreviewWorkflowImport(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	if agentID == "" {
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}
	var req WorkflowImportPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Document) == 0 {
		c.Error(errors.NewBadRequestError("A workflow import document is required"))
		return
	}
	previewer, ok := h.agentRuntime.(workflowImportPreviewer)
	if !ok {
		c.Error(errors.NewInternalServerError("Workflow import preview is unavailable"))
		return
	}
	preview, err := previewer.PreviewWorkflowImport(ctx, agentID, types.JSON(req.Document))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": agentID})
		switch {
		case stderrors.Is(err, repository.ErrCustomAgentNotFound), stderrors.Is(err, service.ErrAgentNotFound):
			c.Error(errors.NewNotFoundError("Agent not found"))
		default:
			c.Error(errors.NewBadRequestError(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": preview})
}

// GetWorkflowRuns godoc
// @Summary      获取工作流运行列表
// @Description  按 started_at、id 倒序返回运行记录，支持游标分页与状态/时间筛选。next_cursor 由最后一条的 started_at 与 id 组成，has_more 为 false 时为 null
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id                 path      string  true   "智能体ID"
// @Param        limit              query     int     false  "每页数量（默认20，上限100）"
// @Param        status             query     string  false  "运行状态筛选"
// @Param        started_after      query     string  false  "起始时间（RFC3339）"
// @Param        started_before     query     string  false  "结束时间（RFC3339，不含）"
// @Param        before_started_at  query     string  false  "游标：上一页最后一条的 started_at（RFC3339）"
// @Param        before_id          query     string  false  "游标：上一页最后一条的 id"
// @Success      200                {object}  map[string]interface{}  "运行列表"
// @Failure      400                {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/workflow/runs [get]
func (h *CustomAgentHandler) GetWorkflowRuns(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	query := types.WorkflowRunQuery{
		Status:  strings.TrimSpace(c.Query("status")),
		RunMode: strings.TrimSpace(c.Query("run_mode")),
		Limit:   parseWorkflowRunLimit(c.Query("limit")),
	}
	// 时间筛选都是可选的；格式错误直接 400，避免静默忽略条件后前端拿到与筛选
	// 意图不符的全量数据。
	if raw := strings.TrimSpace(c.Query("started_after")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.Error(errors.NewBadRequestError("started_after must be RFC3339").WithDetails(err.Error()))
			return
		}
		query.StartedAfter = &parsed
	}
	if raw := strings.TrimSpace(c.Query("started_before")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.Error(errors.NewBadRequestError("started_before must be RFC3339").WithDetails(err.Error()))
			return
		}
		query.StartedBefore = &parsed
	}
	if raw := strings.TrimSpace(c.Query("before_started_at")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.Error(errors.NewBadRequestError("before_started_at must be RFC3339").WithDetails(err.Error()))
			return
		}
		query.BeforeStartedAt = &parsed
	}
	query.BeforeID = strings.TrimSpace(c.Query("before_id"))
	// 游标两字段必须成对出现，否则仓储层会退化成仅按 id 过滤，造成跳页/重复。
	if (query.BeforeStartedAt == nil) != (query.BeforeID == "") {
		c.Error(errors.NewBadRequestError("before_started_at and before_id must be provided together"))
		return
	}

	runs, hasMore, err := h.agentRuntime.ListWorkflowRuns(ctx, id, query)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		c.Error(errors.NewInternalServerError("Failed to list workflow runs"))
		return
	}
	if runs == nil {
		runs = []*types.WorkflowRun{}
	}

	var nextCursor *workflowRunCursor
	if hasMore && len(runs) > 0 {
		last := runs[len(runs)-1]
		nextCursor = &workflowRunCursor{StartedAt: last.StartedAt, ID: last.ID}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":       runs,
			"has_more":    hasMore,
			"next_cursor": nextCursor,
		},
	})
}

// GetWorkflowRun godoc
// @Summary      获取工作流运行详情
// @Description  返回单次运行元数据、当次执行所用的定义快照以及节点执行记录。详情严格基于当次定义快照渲染，不受当前草稿或最新发布影响
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id      path      string  true  "智能体ID"
// @Param        run_id  path      string  true  "运行记录ID"
// @Success      200     {object}  map[string]interface{}  "运行详情"
// @Failure      400     {object}  errors.AppError         "请求参数错误"
// @Failure      404     {object}  errors.AppError         "运行记录不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/workflow/runs/{run_id} [get]
func (h *CustomAgentHandler) GetWorkflowRun(c *gin.Context) {
	ctx := c.Request.Context()
	id := secutils.SanitizeForLog(c.Param("id"))
	runID := secutils.SanitizeForLog(c.Param("run_id"))
	if id == "" || runID == "" {
		c.Error(errors.NewBadRequestError("Agent ID and run ID cannot be empty"))
		return
	}

	run, err := h.agentRuntime.GetWorkflowRun(ctx, id, runID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id, "run_id": runID})
		if stderrors.Is(err, repository.ErrWorkflowRunNotFound) {
			c.Error(errors.NewNotFoundError("Workflow run not found"))
			return
		}
		c.Error(errors.NewInternalServerError("Failed to load workflow run"))
		return
	}
	// run.DefinitionSnapshot 即当次运行的不可变定义快照，原样返回给前端渲染，
	// 保证历史运行的节点/边展示与执行时一致。
	c.JSON(http.StatusOK, gin.H{"success": true, "data": run})
}

// StreamWorkflowRun 按 sequence 续接工作流生命周期事件。
func (h *CustomAgentHandler) StreamWorkflowRun(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	runID := secutils.SanitizeForLog(c.Param("run_id"))
	if agentID == "" || runID == "" {
		c.Error(errors.NewBadRequestError("Agent ID and run ID cannot be empty"))
		return
	}
	afterSequence := int64(0)
	rawSequence := strings.TrimSpace(c.Query("after_sequence"))
	if rawSequence == "" {
		rawSequence = strings.TrimSpace(c.GetHeader("Last-Event-ID"))
	}
	if rawSequence != "" {
		parsed, err := strconv.ParseInt(rawSequence, 10, 64)
		if err != nil || parsed < 0 {
			c.Error(errors.NewBadRequestError("after_sequence must be a non-negative integer"))
			return
		}
		afterSequence = parsed
	}
	if _, err := h.agentRuntime.GetWorkflowRun(ctx, agentID, runID); err != nil {
		if stderrors.Is(err, repository.ErrWorkflowRunNotFound) {
			c.Error(errors.NewNotFoundError("Workflow run not found"))
			return
		}
		c.Error(errors.NewInternalServerError("Failed to load workflow run"))
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Error(errors.NewInternalServerError("Streaming is unsupported"))
		return
	}

	pollTicker := time.NewTicker(750 * time.Millisecond)
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		events, err := h.agentRuntime.ListWorkflowRunEvents(ctx, agentID, runID, afterSequence, 200)
		if err != nil {
			payload, _ := json.Marshal(gin.H{"message": "Failed to read workflow run events"})
			_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
			flusher.Flush()
			return
		}
		for _, runEvent := range events {
			payload, marshalErr := json.Marshal(runEvent)
			if marshalErr != nil {
				continue
			}
			eventName := strings.NewReplacer("\n", "", "\r", "").Replace(runEvent.EventType)
			_, _ = fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", runEvent.Sequence, eventName, payload)
			afterSequence = runEvent.Sequence
		}
		if len(events) > 0 {
			flusher.Flush()
		}

		run, err := h.agentRuntime.GetWorkflowRun(ctx, agentID, runID)
		if err == nil && workflowRunTerminal(run.Status) && len(events) == 0 {
			_, _ = fmt.Fprintf(c.Writer, "event: end\ndata: {\"status\":%q}\n\n", run.Status)
			flusher.Flush()
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			_, _ = c.Writer.WriteString(": keep-alive\n\n")
			flusher.Flush()
		case <-pollTicker.C:
		}
	}
}

// CancelWorkflowRun 幂等请求取消一次工作流运行。
func (h *CustomAgentHandler) CancelWorkflowRun(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	runID := secutils.SanitizeForLog(c.Param("run_id"))
	if agentID == "" || runID == "" {
		c.Error(errors.NewBadRequestError("Agent ID and run ID cannot be empty"))
		return
	}
	run, changed, err := h.agentRuntime.RequestWorkflowRunCancel(ctx, agentID, runID)
	if err != nil {
		if stderrors.Is(err, repository.ErrWorkflowRunNotFound) {
			c.Error(errors.NewNotFoundError("Workflow run not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": agentID, "run_id": runID})
		c.Error(errors.NewInternalServerError("Failed to cancel workflow run"))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"run": run, "cancel_requested": changed}})
}

// StartWorkflowDebugRun 创建一次绑定当前草稿快照的编辑器试跑。
func (h *CustomAgentHandler) StartWorkflowDebugRun(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	if agentID == "" {
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}
	var req WorkflowDebugRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid debug run request").WithDetails(err.Error()))
		return
	}
	commands, ok := h.agentRuntime.(workflowRunCommands)
	if !ok {
		c.Error(errors.NewInternalServerError("Workflow runtime is unavailable"))
		return
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	run, err := commands.StartWorkflowDebugRun(ctx, agentID, req.ExpectedRevision, req.Input, idempotencyKey)
	if err != nil {
		if stderrors.Is(err, repository.ErrWorkflowRevisionConflict) {
			c.Error(errors.NewConflictError("Workflow draft was modified; refresh before trying again").WithDetails(
				gin.H{"current_revision": h.currentDraftRevision(ctx, agentID)},
			))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": agentID})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": run})
}

// RetryWorkflowRun 使用原快照创建一次全量重跑。
func (h *CustomAgentHandler) RetryWorkflowRun(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	runID := secutils.SanitizeForLog(c.Param("run_id"))
	commands, ok := h.agentRuntime.(workflowRunCommands)
	if agentID == "" || runID == "" {
		c.Error(errors.NewBadRequestError("Agent ID and run ID cannot be empty"))
		return
	}
	if !ok {
		c.Error(errors.NewInternalServerError("Workflow runtime is unavailable"))
		return
	}
	run, err := commands.RetryWorkflowRun(ctx, agentID, runID)
	if err != nil {
		if stderrors.Is(err, repository.ErrWorkflowRunNotFound) {
			c.Error(errors.NewNotFoundError("Workflow run not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": agentID, "run_id": runID})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": run})
}

// RetryWorkflowNode 从指定失败节点继续运行，并保留该节点之前的变量检查点。
func (h *CustomAgentHandler) RetryWorkflowNode(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := secutils.SanitizeForLog(c.Param("id"))
	runID := secutils.SanitizeForLog(c.Param("run_id"))
	nodeRunID, err := strconv.ParseInt(strings.TrimSpace(c.Param("node_run_id")), 10, 64)
	if agentID == "" || runID == "" || err != nil || nodeRunID <= 0 {
		c.Error(errors.NewBadRequestError("Agent ID, run ID and positive node run ID are required"))
		return
	}
	commands, ok := h.agentRuntime.(workflowRunCommands)
	if !ok {
		c.Error(errors.NewInternalServerError("Workflow runtime is unavailable"))
		return
	}
	run, err := commands.RetryWorkflowNode(ctx, agentID, runID, nodeRunID)
	if err != nil {
		if stderrors.Is(err, repository.ErrWorkflowRunNotFound) {
			c.Error(errors.NewNotFoundError("Workflow run or node not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": agentID, "run_id": runID, "node_run_id": nodeRunID})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": run})
}

func workflowRunTerminal(status string) bool {
	return status != "" && status != types.WorkflowRunStatusRunning
}

// parseWorkflowRunLimit 归一化 limit：未提供或非法时回落默认值，并强制上限，
// 与仓储层的边界保持一致，避免把超大 limit 透传给数据库。
func parseWorkflowRunLimit(raw string) int {
	const (
		defaultLimit = 20
		maxLimit     = 100
	)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLimit
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return defaultLimit
	}
	if parsed > maxLimit {
		return maxLimit
	}
	return parsed
}

// GetSuggestedQuestions godoc
// @Summary      获取推荐问题
// @Description  基于智能体关联的知识库，返回推荐问题供用户快捷提问
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id                  path      string  true   "智能体ID"
// @Param        knowledge_base_ids  query     string  false  "知识库ID列表（逗号分隔），覆盖智能体默认配置"
// @Param        knowledge_ids       query     string  false  "知识ID列表（逗号分隔），限定到具体文档"
// @Param        tag_scopes          query     string  false  "带知识库归属的标签范围（JSON）"
// @Param        limit               query     int     false  "返回数量上限（未传时使用智能体配置的开场问题数量，最大30）"
// @Success      200                 {object}  map[string]interface{}  "推荐问题列表"
// @Failure      400                 {object}  errors.AppError         "请求参数错误"
// @Failure      404                 {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/suggested-questions [get]
func (h *CustomAgentHandler) GetSuggestedQuestions(c *gin.Context) {
	ctx := c.Request.Context()

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	// Parse optional query parameters
	var kbIDs []string
	if kbIDsStr := strings.TrimSpace(c.Query("knowledge_base_ids")); kbIDsStr != "" {
		for _, id := range strings.Split(kbIDsStr, ",") {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				kbIDs = append(kbIDs, trimmed)
			}
		}
	}

	var knowledgeIDs []string
	if kIDsStr := strings.TrimSpace(c.Query("knowledge_ids")); kIDsStr != "" {
		for _, id := range strings.Split(kIDsStr, ",") {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				knowledgeIDs = append(knowledgeIDs, trimmed)
			}
		}
	}

	var tagScopes []types.TagScope
	if raw := strings.TrimSpace(c.Query("tag_scopes")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &tagScopes); err != nil {
			c.Error(errors.NewBadRequestError("tag_scopes must be valid JSON"))
			return
		}
	}

	// limit == 0 signals "unspecified" so the service falls back to the agent's
	// configured starter count. A provided value is passed through unchanged and
	// bounded by the service's safety cap.
	limit := 0
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	logger.Infof(ctx, "Getting suggested questions for agent %s, kbIDs: %v, tagScopes: %d, limit: %d",
		secutils.SanitizeForLog(id), kbIDs, len(tagScopes), limit)

	questions, err := h.service.GetSuggestedQuestions(ctx, id, kbIDs, knowledgeIDs, tagScopes, limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		if err == service.ErrAgentNotFound {
			c.Error(errors.NewNotFoundError("Agent not found"))
			return
		}
		if appErr, ok := err.(*errors.AppError); ok {
			c.Error(appErr)
			return
		}
		// Reached only after the typed sentinels and *errors.AppError above, so
		// whatever lands here is a raw repository/driver error. Its text
		// (SQLSTATE, column names) must not reach the client; the full detail
		// is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to build suggested questions"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"questions": questions,
		},
	})
}

// validateAgentSandboxConfig rejects a selection the workspace does not have.
//
// Checking at save time is what makes the mistake fixable: a dangling reference
// only fails when the agent next runs a skill, mid-conversation, as an opaque
// resolution error with no hint about which agent to edit.
func (h *CustomAgentHandler) validateAgentSandboxConfig(
	ctx context.Context, cfg types.CustomAgentConfig,
) error {
	configID := strings.TrimSpace(cfg.SandboxConfigID)
	if configID == "" || h.sandboxConfigs == nil {
		// Empty means the deployment-wide default, which always exists.
		return nil
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return errors.NewUnauthorizedError("Missing workspace context")
	}
	stored, err := h.sandboxConfigs.Get(ctx, tenantID, configID)
	if err != nil {
		return errors.NewInternalServerError("Failed to verify sandbox config").
			WithDetails(err.Error())
	}
	if stored == nil {
		return errors.NewBadRequestError("所选沙箱后端配置不存在，请重新选择")
	}
	return nil
}

func (h *CustomAgentHandler) validateWorkflowResources(
	ctx context.Context, cfg *types.CustomAgentConfig,
) error {
	if cfg == nil || cfg.AgentType != types.AgentTypeWorkflow {
		return nil
	}
	provider, ok := h.agentRuntime.(workflowAgentProvider)
	if !ok {
		return errors.NewInternalServerError("Workflow runtime is unavailable")
	}
	if err := provider.ValidateWorkflowResources(ctx, cfg); err != nil {
		return errors.NewBadRequestError(err.Error())
	}
	return nil
}

func authorizeAgentKnowledgeScope(ctx context.Context, cfg types.CustomAgentConfig) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(cfg.KBSelectionMode)) {
	case "none":
		return nil
	case "all":
		return errors.NewForbiddenError("API key scope does not allow agents that use all knowledge bases")
	case "selected":
		return types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, cfg.KnowledgeBases...)
	default:
		if len(cfg.KnowledgeBases) == 0 {
			return nil
		}
		return types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, cfg.KnowledgeBases...)
	}
}
