package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	workflowruntime "github.com/Tencent/WeKnora/internal/workflow"
)

// ValidateWorkflowResources 校验工作流引用的知识库、MCP 工具和 Skill 仍然可用。
//
// @param ctx 当前请求上下文。
// @param config 待校验的智能体配置。
// @returns 资源不存在、被禁用或权限范围不匹配时返回错误。
func (s *agentService) ValidateWorkflowResources(
	ctx context.Context,
	config *types.CustomAgentConfig,
) error {
	if config == nil || config.AgentType != types.AgentTypeWorkflow {
		return nil
	}
	if err := workflowruntime.ValidatePublishedConfig(config); err != nil {
		return err
	}

	if s.knowledgeBaseService != nil {
		permissions := kbReadPermissions(ctx, s.kbShareService)
		for _, kbID := range config.KnowledgeBases {
			kb, err := s.knowledgeBaseService.GetKnowledgeBaseByIDOnly(ctx, kbID)
			if err != nil || kb == nil {
				return fmt.Errorf("workflow knowledge base %s is unavailable", kbID)
			}
			allowed, permissionErr := permissions.Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
			if permissionErr != nil || !allowed {
				return fmt.Errorf("workflow knowledge base %s is unavailable", kbID)
			}
		}
	} else if len(config.KnowledgeBases) > 0 {
		return fmt.Errorf("workflow knowledge base service is unavailable")
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	var usableSkills map[string]struct{}
	if tenantID != 0 && strings.TrimSpace(config.SandboxConfigID) != "" && s.db != nil {
		rows := effectiveTenantSkills(
			ctx,
			repository.NewTenantSandboxConfigRepository(s.db),
			repository.NewTenantSkillRepository(s.db),
			tenantID,
			config.SandboxConfigID,
		)
		usableSkills = make(map[string]struct{}, len(rows))
		for _, row := range rows {
			if row != nil {
				usableSkills[row.Name] = struct{}{}
			}
		}
	}
	for _, node := range config.Workflow.Nodes {
		if node.Type != types.WorkflowNodeTypeTool {
			continue
		}
		var nodeConfig types.WorkflowToolNodeConfig
		if err := json.Unmarshal(node.Config, &nodeConfig); err != nil {
			return fmt.Errorf("workflow tool node %s has invalid config: %w", node.ID, err)
		}
		switch nodeConfig.Kind {
		case types.WorkflowToolKindMCP:
			if s.mcpServiceService == nil || tenantID == 0 {
				return fmt.Errorf("workflow MCP tool %s is unavailable", nodeConfig.ToolName)
			}
			mcpService, err := s.mcpServiceService.GetMCPServiceByID(ctx, tenantID, nodeConfig.ServiceID)
			if err != nil || mcpService == nil || !mcpService.Enabled {
				return fmt.Errorf("workflow MCP service %s is unavailable", nodeConfig.ServiceID)
			}
			mcpTools, err := s.mcpServiceService.GetMCPServiceTools(ctx, tenantID, nodeConfig.ServiceID)
			if err != nil || !hasMCPTool(mcpTools, nodeConfig.ToolName) {
				return fmt.Errorf("workflow MCP tool %s is unavailable", nodeConfig.ToolName)
			}
		case types.WorkflowToolKindSkill:
			if _, ok := usableSkills[nodeConfig.SkillName]; !ok {
				return fmt.Errorf("workflow Skill %s is unavailable", nodeConfig.SkillName)
			}
		}
	}
	return nil
}

// WorkflowCatalog 返回当前智能体有权使用的工作流资源目录。
//
// @param ctx 当前请求上下文。
// @param agent 要读取目录的智能体。
// @returns 内置工具、MCP 工具和已安装 Skill 的目录。
func (s *agentService) WorkflowCatalog(
	ctx context.Context,
	agent *types.CustomAgent,
) (*types.WorkflowCatalog, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent is required")
	}
	agent.EnsureDefaults()
	if agent.Config.AgentType != types.AgentTypeWorkflow {
		return nil, fmt.Errorf("agent is not a workflow agent")
	}
	if err := workflowruntime.NormalizeDraftConfig(&agent.Config); err != nil {
		return nil, err
	}

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, fmt.Errorf("workspace context is required")
	}

	searchTargets := make(types.SearchTargets, 0, len(agent.Config.KnowledgeBases))
	for _, kbID := range agent.Config.KnowledgeBases {
		searchTargets = append(searchTargets, &types.SearchTarget{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: kbID,
			TenantID:        tenantID,
		})
	}
	allowed := make([]string, 0, len(workflowruntime.BuiltinToolAllowlist))
	for toolName := range workflowruntime.BuiltinToolAllowlist {
		allowed = append(allowed, toolName)
	}
	runtimeConfig := &types.AgentConfig{
		AllowedTools:   allowed,
		KnowledgeBases: append([]string(nil), agent.Config.KnowledgeBases...),
		SearchTargets:  searchTargets,
		// 目录不能依赖当前工作流已经引用了哪些工具，否则默认的
		// “开始 -> 结束”图无法添加第一个工具节点。
		WebSearchEnabled:    true,
		WebSearchMaxResults: agent.Config.WebSearchMaxResults,
		WebSearchProviderID: agent.Config.WebSearchProviderID,
		MemoryEnabled:       agent.Config.MemoryEnabled,
	}
	registry := agenttools.NewToolRegistry()
	if err := s.registerTools(ctx, registry, runtimeConfig, nil, nil, "workflow-catalog"); err != nil {
		return nil, fmt.Errorf("build workflow tool catalog: %w", err)
	}
	catalog := &types.WorkflowCatalog{BuiltinTools: make([]types.WorkflowCatalogTool, 0)}
	for _, definition := range registry.GetFunctionDefinitions() {
		if _, allowed := workflowruntime.BuiltinToolAllowlist[definition.Name]; !allowed {
			continue
		}
		displayName := definition.Name
		if label, ok := builtinToolDisplayNames[definition.Name]; ok {
			displayName = label
		}
		catalog.BuiltinTools = append(catalog.BuiltinTools, types.WorkflowCatalogTool{
			Name:        definition.Name,
			DisplayName: displayName,
			Description: definition.Description,
			Parameters:  definition.Parameters,
		})
	}

	catalog.MCPServices = s.workflowMCPCatalog(ctx, tenantID, &agent.Config)
	if s.db != nil && strings.TrimSpace(agent.Config.SandboxConfigID) != "" {
		skills := effectiveTenantSkills(
			ctx,
			repository.NewTenantSandboxConfigRepository(s.db),
			repository.NewTenantSkillRepository(s.db),
			tenantID,
			agent.Config.SandboxConfigID,
		)
		for _, skill := range skills {
			if skill == nil {
				continue
			}
			catalog.Skills = append(catalog.Skills, types.WorkflowCatalogSkill{
				Name:        skill.Name,
				Version:     skill.Version,
				Description: skill.Description,
			})
		}
	}
	return catalog, nil
}

func (s *agentService) workflowMCPCatalog(
	ctx context.Context,
	tenantID uint64,
	config *types.CustomAgentConfig,
) []types.WorkflowCatalogService {
	if s.mcpServiceService == nil || config == nil {
		return []types.WorkflowCatalogService{}
	}
	// 工作流配置本身就是 MCP 的选择器。目录必须展示当前空间中可用的
	// 服务，不能只展示已被当前图引用的服务，否则新图无法添加第一个 MCP 节点。
	services, err := s.mcpServiceService.ListMCPServices(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list workflow MCP services: %v", err)
		return []types.WorkflowCatalogService{}
	}
	out := make([]types.WorkflowCatalogService, 0, len(services))
	for _, service := range services {
		if service == nil || !service.Enabled {
			continue
		}
		entry := types.WorkflowCatalogService{
			ID:          service.ID,
			Name:        service.Name,
			Description: service.Description,
			Tools:       []types.WorkflowCatalogTool{},
		}
		mcpTools, toolErr := s.mcpServiceService.GetMCPServiceTools(ctx, tenantID, service.ID)
		if toolErr != nil {
			logger.Warnf(ctx, "Failed to list tools for workflow MCP service %s: %v", service.ID, toolErr)
			out = append(out, entry)
			continue
		}
		for _, tool := range mcpTools {
			if tool == nil {
				continue
			}
			entry.Tools = append(entry.Tools, types.WorkflowCatalogTool{
				Name:            tool.Name,
				DisplayName:     tool.Name,
				Description:     tool.Description,
				Parameters:      tool.InputSchema,
				RequireApproval: tool.RequireApproval,
			})
		}
		out = append(out, entry)
	}
	return out
}

// ExecuteWorkflowBuiltinTool 通过现有注册器执行一个受限内置工具。
//
// @param ctx 当前请求上下文。
// @param config 工作流运行时配置。
// @param toolName 已通过工作流白名单校验的工具名。
// @param args 工具 JSON 参数。
// @param sessionID 当前会话 ID。
// @param rerankModel 已解析的 rerank 模型；nil 表示本次调用不做重排。
// @returns 工具结果以及执行错误。
func (s *agentService) ExecuteWorkflowBuiltinTool(
	ctx context.Context,
	config *types.AgentConfig,
	toolName string,
	args json.RawMessage,
	sessionID string,
	rerankModel rerank.Reranker,
) (*types.ToolResult, error) {
	if _, ok := workflowruntime.BuiltinToolAllowlist[toolName]; !ok {
		return &types.ToolResult{Success: false, Error: "workflow builtin tool is not allowed"}, fmt.Errorf("workflow builtin tool %s is not allowed", toolName)
	}
	if config == nil {
		return nil, fmt.Errorf("workflow agent config is required")
	}
	runtimeConfig := *config
	runtimeConfig.AllowedTools = []string{toolName}
	if toolName == agenttools.ToolWebSearch || toolName == agenttools.ToolWebFetch {
		runtimeConfig.WebSearchEnabled = true
	}
	registry := agenttools.NewToolRegistry()
	// rerankModel 由调用方解析后传入：此前这里固定传 nil，knowledge_search 收到
	// nil 会静默跳过重排，导致工作流检索质量低于普通智能体。
	if err := s.registerTools(ctx, registry, &runtimeConfig, rerankModel, nil, sessionID); err != nil {
		return nil, err
	}
	return registry.ExecuteTool(ctx, toolName, args)
}

// ExecuteWorkflowMCPTool 通过现有 MCPTool 执行指定服务中的一个工具。
//
// @param ctx 当前请求上下文。
// @param config 工作流运行时配置。
// @param serviceID MCP 服务 ID。
// @param toolName MCP 工具名。
// @param args 工具 JSON 参数。
// @param sessionID 当前会话 ID。
// @param assistantMessageID 当前助手消息 ID。
// @param toolCallID 工作流合成工具调用 ID。
// @param eventBus 外层事件总线，用于 OAuth 和人工审批。
// @returns MCP 工具结果以及执行错误。
func (s *agentService) ExecuteWorkflowMCPTool(
	ctx context.Context,
	config *types.AgentConfig,
	serviceID, toolName string,
	args json.RawMessage,
	sessionID, assistantMessageID, toolCallID string,
	eventBus *event.EventBus,
) (*types.ToolResult, error) {
	if s.mcpServiceService == nil || s.mcpManager == nil {
		return &types.ToolResult{Success: false, Error: "MCP is unavailable"}, fmt.Errorf("MCP is unavailable")
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return &types.ToolResult{Success: false, Error: "workspace context is required"}, fmt.Errorf("workspace context is required")
	}
	service, err := s.mcpServiceService.GetMCPServiceByID(ctx, tenantID, serviceID)
	if err != nil || service == nil || !service.Enabled {
		if err == nil {
			err = fmt.Errorf("MCP service %s is unavailable", serviceID)
		}
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	mcpTools, err := s.mcpServiceService.GetMCPServiceTools(ctx, tenantID, serviceID)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	var definition *types.MCPTool
	for _, candidate := range mcpTools {
		if candidate != nil && candidate.Name == toolName {
			definition = candidate
			break
		}
	}
	if definition == nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("MCP tool %s is unavailable", toolName)}, fmt.Errorf("MCP tool %s is unavailable", toolName)
	}
	registry := agenttools.NewToolRegistry()
	registry.RegisterTool(agenttools.NewMCPTool(
		service, definition, s.mcpManager, s.toolApprovalGate,
		workflowMCPAuthWaitTimeout(config),
	))
	requestID, _ := types.RequestIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	callCtx = agenttools.WithToolExecContext(callCtx, &agenttools.ToolExecContext{
		SessionID:          sessionID,
		AssistantMessageID: assistantMessageID,
		RequestID:          requestID,
		ToolCallID:         toolCallID,
		UserID:             userID,
		EventBus:           eventBus,
		ApprovalCtx:        ctx,
		ExecTimeout:        60 * time.Second,
	})
	return registry.ExecuteTool(callCtx, registryToolName(service, definition), args)
}

// ExecuteWorkflowSkill 使用只允许一个 Skill、read_file 和 shell_exec 的小智能体。
//
// @param ctx 当前请求上下文。
// @param parentConfig 外层工作流运行时配置。
// @param chatModel 工作流共用的聊天模型。
// @param skillName 已校验的 Skill 名。
// @param task 传给 Skill 小智能体的任务。
// @param sessionID 当前会话 ID。
// @param assistantMessageID 当前助手消息 ID。
// @param eventBus 父级事件总线，用于实时展示调用过程与命令输出。
// @returns Skill 小智能体状态以及执行错误。
func (s *agentService) ExecuteWorkflowSkill(
	ctx context.Context,
	parentConfig *types.AgentConfig,
	chatModel chat.Chat,
	skillName, task, sessionID, assistantMessageID string,
	eventBus *event.EventBus,
) (*types.AgentState, error) {
	if parentConfig == nil {
		return nil, fmt.Errorf("workflow agent config is required")
	}
	child := *parentConfig
	child.AllowedTools = []string{agenttools.ToolReadFile, agenttools.ToolShellExec}
	child.SkillsEnabled = true
	child.AllowedSkills = []string{skillName}
	child.SkillDirs = nil
	child.TenantSkills = filterTenantSkills(parentConfig.TenantSkills, skillName)
	child.MCPSelectionMode = "none"
	child.MCPServices = nil
	child.WebSearchEnabled = false
	child.LocalBrowserEnabled = false
	child.SearchTargets = nil
	child.KnowledgeBases = nil
	child.KnowledgeIDs = nil
	child.PinnedMCPServiceIDs = nil
	child.PinnedSkillNames = nil
	maxIters := parentConfig.MaxIterations
	if maxIters < 25 {
		maxIters = 25
	}
	child.MaxIterations = maxIters
	child.UseCustomSystemPrompt = true
	child.SystemPrompt = "你是工作流中的受限 Skill 执行器。只完成给定任务，只使用当前指定 Skill、read_file 和 shell_exec。若技能需要生成交付文件，请确保执行构建或导出命令将成品文件输出到 /workspace/output 目录。不要调用其他工具，不要修改工作流路由，不要编造未读取到的结果。完成后直接返回简洁结果。"

	subBus := event.NewEventBus()
	if eventBus != nil {
		forwardTypes := []event.EventType{
			event.EventAgentThought,
			event.EventAgentCommandOutput,
			event.EventAgentToolCall,
			event.EventAgentToolResult,
			event.EventAgentStep,
		}
		for _, et := range forwardTypes {
			t := et
			subBus.On(t, func(c context.Context, evt event.Event) error {
				return eventBus.Emit(c, evt)
			})
		}
		eventBus.On(event.EventStop, func(c context.Context, evt event.Event) error {
			return subBus.Emit(c, evt)
		})
	}
	engine, err := s.CreateAgentEngine(
		ctx, &child, chatModel, nil, subBus, sessionID, assistantMessageID,
	)
	if err != nil {
		return nil, err
	}
	state, err := engine.Execute(ctx, sessionID, assistantMessageID, task, nil)
	if err != nil || state == nil {
		return state, err
	}

	// Skill 子 Agent 执行完毕后，尝试收集沙箱 /workspace/output 里的产物文件。
	// 收集是 best-effort：失败只记日志，不阻断工作流正常返回。
	// artifactCollector 字段由 DI 容器注入；未注入时静默跳过（无沙箱后端的部署）。
	if s.artifactCollector != nil {
		tenantID, _ := types.TenantIDFromContext(ctx)
		collectCtx := context.WithoutCancel(ctx)
		artifacts, collectErr := s.artifactCollector.Collect(
			collectCtx,
			sessionID,
			assistantMessageID,
			tenantID,
			skills.ArtifactOutputDir(),
		)
		if collectErr != nil {
			logger.Warnf(ctx, "workflow skill artifact collect failed session=%s skill=%s: %v",
				sessionID, skillName, collectErr)
		} else if len(artifacts) > 0 {
			state.Artifacts = artifacts
			logger.Infof(ctx, "workflow skill artifact collect attached %d file(s) session=%s skill=%s",
				len(artifacts), sessionID, skillName)
		}
	}
	return state, nil
}
func filterTenantSkills(rows []*types.TenantSkillEntity, name string) []*types.TenantSkillEntity {
	filtered := make([]*types.TenantSkillEntity, 0, 1)
	for _, row := range rows {
		if row != nil && row.Name == name && row.Status == types.SkillStatusReady && row.Enabled {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func hasMCPTool(rows []*types.MCPTool, name string) bool {
	for _, row := range rows {
		if row != nil && row.Name == name {
			return true
		}
	}
	return false
}

func workflowMCPAuthWaitTimeout(config *types.AgentConfig) int {
	if config == nil {
		return 0
	}
	return config.MCPAuthWaitTimeout
}

func registryToolName(service *types.MCPService, definition *types.MCPTool) string {
	return agenttools.NewMCPTool(service, definition, nil, nil, 0).Name()
}

var builtinToolDisplayNames = map[string]string{
	"web_search":            "网络搜索",
	"web_fetch":             "网页抓取",
	"database_query":        "数据库查询",
	"data_analysis":         "数据分析",
	"data_schema":           "数据结构元信息",
	"wiki_search":           "Wiki 搜索",
	"wiki_read_page":        "Wiki 页面阅读",
	"wiki_read_source_doc":  "精读源文档",
	"wiki_read_issue":       "查看 Wiki 问题",
	"wiki_flag_issue":       "标记 Wiki 问题",
	"wiki_write_page":       "创建/覆盖 Wiki",
	"wiki_replace_text":     "局部替换 Wiki",
	"wiki_rename_page":      "重命名 Wiki",
	"wiki_delete_page":      "删除 Wiki",
	"wiki_update_issue":     "更新 Wiki 问题",
	"search_conversations":  "搜索历史会话",
	"search_memory":         "检索长期记忆",
	"grep_chunks":           "关键词搜索",
	"knowledge_search":      "知识库检索",
	"list_knowledge_chunks": "查看知识切片",
	"query_knowledge_graph": "查询知识图谱",
	"get_document_info":     "获取文档信息",
	"todo_write":            "计划管理",
	"thinking":              "深度思考",
}
