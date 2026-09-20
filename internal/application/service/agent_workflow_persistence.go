package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	workflowruntime "github.com/Tencent/WeKnora/internal/workflow"
)

// PublishWorkflow 校验当前草稿并创建一个不可变发布版本。
//
// @param ctx 当前请求上下文。
// @param agentID 工作流智能体 ID。
// @param expectedRevision 客户端看到的草稿修订号。
// @returns 新发布版本；修订号冲突时返回 repository.ErrWorkflowRevisionConflict。
func (s *agentService) PublishWorkflow(
	ctx context.Context,
	agentID string,
	expectedRevision int64,
) (*types.WorkflowVersionRecord, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	agentRepo := repository.NewCustomAgentRepository(s.db)
	agent, err := agentRepo.GetAgentByID(ctx, agentID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, err
	}
	if agent.Config.AgentType != types.AgentTypeWorkflow {
		return nil, fmt.Errorf("agent is not a workflow agent")
	}
	if agent.DraftRevision != expectedRevision {
		return nil, repository.ErrWorkflowRevisionConflict
	}
	if err := workflowruntime.ValidatePublishedConfig(&agent.Config); err != nil {
		return nil, err
	}
	if issues := workflowruntime.ValidateIssues(agent.Config.Workflow); len(issues) > 0 {
		return nil, fmt.Errorf("workflow validation failed: %s", issues[0].Message)
	}
	if err := s.ValidateWorkflowResources(ctx, &agent.Config); err != nil {
		return nil, err
	}
	var publishedBy string
	if userID, ok := types.UserIDFromContext(ctx); ok && !types.IsSyntheticUserID(userID) {
		publishedBy = userID
	}
	return s.workflowRepo.Publish(
		ctx,
		tenantID,
		agentID,
		publishedBy,
		expectedRevision,
		agent.Config.Workflow,
		agent.Config,
	)
}

// GetPublishedWorkflow 返回正式运行入口应该使用的当前发布快照。
//
// @param ctx 当前请求上下文。
// @param agentID 工作流智能体 ID。
// @returns 不可变发布版本快照。
func (s *agentService) GetPublishedWorkflow(ctx context.Context, agentID string) (*types.WorkflowVersionRecord, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	return s.workflowRepo.GetCurrentVersion(ctx, tenantID, agentID)
}

// ListWorkflowVersions 返回工作流发布历史。
//
// @param ctx 当前请求上下文。
// @param agentID 工作流智能体 ID。
// @returns 从新到旧的发布版本列表。
func (s *agentService) ListWorkflowVersions(ctx context.Context, agentID string) ([]*types.WorkflowVersionRecord, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	return s.workflowRepo.ListVersions(ctx, tenantID, agentID)
}

// GetWorkflowVersion 返回指定不可变版本。
func (s *agentService) GetWorkflowVersion(
	ctx context.Context,
	agentID string,
	version int64,
) (*types.WorkflowVersionRecord, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	return s.workflowRepo.GetVersion(ctx, tenantID, agentID, version)
}

// RestoreWorkflowVersion 将旧版本复制成新的草稿修订，不修改不可变版本。
func (s *agentService) RestoreWorkflowVersion(
	ctx context.Context,
	agentID string,
	version, expectedRevision int64,
) (*types.CustomAgent, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	agentRepo := repository.NewCustomAgentRepository(s.db)
	agent, err := agentRepo.GetAgentByID(ctx, agentID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, err
	}
	if agent.DraftRevision != expectedRevision {
		return nil, repository.ErrWorkflowRevisionConflict
	}
	record, err := s.workflowRepo.GetVersion(ctx, tenantID, agentID, version)
	if err != nil {
		return nil, err
	}
	_, config, err := DecodeWorkflowVersionDefinition(record)
	if err != nil {
		return nil, err
	}
	if err := workflowruntime.NormalizeDraftConfig(config); err != nil {
		return nil, err
	}
	agent.Config = *config
	agent.UpdatedAt = time.Now()
	if err := s.workflowRepo.SaveDraft(ctx, tenantID, agentID, expectedRevision, agent); err != nil {
		return nil, err
	}
	return agent, nil
}

// ValidateWorkflowDefinition 返回编辑器可定位的结构化静态校验结果。
//
// @param config 待校验的智能体配置。
// @returns 结构化问题列表。
func (s *agentService) ValidateWorkflowDefinition(
	ctx context.Context,
	config *types.CustomAgentConfig,
) ([]types.WorkflowValidationIssue, error) {
	if config == nil || config.AgentType != types.AgentTypeWorkflow {
		return []types.WorkflowValidationIssue{{Code: "NOT_WORKFLOW", Message: "配置不是工作流智能体"}}, nil
	}
	copyConfig := *config
	if err := workflowruntime.NormalizeDraftConfig(&copyConfig); err != nil {
		return workflowruntime.ValidateIssues(copyConfig.Workflow), nil
	}
	issues := workflowruntime.ValidateIssues(copyConfig.Workflow)
	if len(issues) == 0 {
		if err := s.ValidateWorkflowResources(ctx, &copyConfig); err != nil {
			issues = append(issues, types.WorkflowValidationIssue{Code: "RESOURCE_UNAVAILABLE", Message: err.Error()})
		}
	}
	return issues, nil
}

type workflowImportPreviewer interface {
	PreviewWorkflowImport(context.Context, string, types.JSON) (*types.WorkflowImportPreview, error)
}

var workflowImportSensitiveKeyRE = regexp.MustCompile(`(?i)(authorization|cookie|proxy[-_]?authorization|access[-_]?token|api[-_]?key|client[-_]?secret|password|secret|token)`)

// PreviewWorkflowImport 解析并预检一个外部工作流文件，不修改当前草稿。
//
// 预检会迁移 schema、移除敏感字段、检查静态结构，并将知识库、MCP、Skill
// 和内置工具引用映射到当前租户可用资源。返回的 definition 已经脱敏，前端只能
// 用它替换画布，不能把导入文件中的凭据原样写回智能体配置。
func (s *agentService) PreviewWorkflowImport(
	ctx context.Context,
	agentID string,
	payload types.JSON,
) (*types.WorkflowImportPreview, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	agent, err := s.loadWorkflowAgent(ctx, tenantID, agentID)
	if err != nil {
		return nil, err
	}

	definition, sensitiveFields, err := decodeWorkflowImportPayload(payload)
	if err != nil {
		return nil, err
	}
	if err := workflowruntime.MigrateDefinition(definition); err != nil {
		return nil, err
	}
	definition = redactWorkflowDefinition(definition, &sensitiveFields)
	sort.Strings(sensitiveFields)
	sensitiveFields = workflowUniqueStrings(sensitiveFields)

	config := agent.Config
	config.AgentMode = types.AgentModeSmartReasoning
	config.AgentType = types.AgentTypeWorkflow
	config.Workflow = definition
	if err := workflowruntime.NormalizeDraftConfig(&config); err != nil {
		return nil, err
	}
	issues := workflowruntime.ValidateIssues(definition)
	mappings, missing := s.mapWorkflowImportResources(ctx, &config)
	warnings := make([]types.WorkflowValidationIssue, 0, 1)
	if len(sensitiveFields) > 0 {
		warnings = append(warnings, types.WorkflowValidationIssue{
			Code:    "SENSITIVE_FIELDS_REDACTED",
			Message: "导入内容包含敏感字段，预览结果已移除这些字段；请改用工作流连接或资源引用。",
		})
	}
	return &types.WorkflowImportPreview{
		Config:           &config,
		Definition:       definition,
		Issues:           issues,
		Warnings:         warnings,
		MissingResources: missing,
		SensitiveFields:  sensitiveFields,
		ResourceMappings: mappings,
	}, nil
}

// decodeWorkflowImportPayload 兼容标准导出包、Agent 配置包和纯定义 JSON。
func decodeWorkflowImportPayload(payload types.JSON) (*types.WorkflowDefinition, []string, error) {
	if len(payload) == 0 || !json.Valid(payload) {
		return nil, nil, fmt.Errorf("workflow import document must be valid JSON")
	}
	var root interface{}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, fmt.Errorf("decode workflow import document: %w", err)
	}
	object, ok := root.(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("workflow import document must be a JSON object")
	}
	candidate := interface{}(object)
	if nested, ok := object["workflow"].(map[string]interface{}); ok {
		candidate = nested
	} else if config, ok := object["config"].(map[string]interface{}); ok {
		if nested, ok := config["workflow"].(map[string]interface{}); ok {
			candidate = nested
		}
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow import definition: %w", err)
	}
	var definition types.WorkflowDefinition
	if err := json.Unmarshal(raw, &definition); err != nil {
		return nil, nil, fmt.Errorf("decode workflow import definition: %w", err)
	}
	if len(definition.Nodes) == 0 && len(definition.Edges) == 0 {
		return nil, nil, fmt.Errorf("workflow import definition is missing nodes and edges")
	}
	sensitiveFields := make([]string, 0)
	collectWorkflowSensitiveFields(root, "$", &sensitiveFields)
	sort.Strings(sensitiveFields)
	sensitiveFields = workflowUniqueStrings(sensitiveFields)
	return &definition, sensitiveFields, nil
}

func collectWorkflowSensitiveFields(value interface{}, path string, fields *[]string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			childPath := path + "." + key
			if workflowImportSensitiveKeyRE.MatchString(key) {
				*fields = append(*fields, childPath)
				continue
			}
			collectWorkflowSensitiveFields(child, childPath, fields)
		}
	case []interface{}:
		for index, child := range typed {
			collectWorkflowSensitiveFields(child, fmt.Sprintf("%s[%d]", path, index), fields)
		}
	}
}

func redactWorkflowDefinition(definition *types.WorkflowDefinition, sensitiveFields *[]string) *types.WorkflowDefinition {
	if definition == nil {
		return nil
	}
	redacted := *definition
	redacted.Nodes = append([]types.WorkflowNode(nil), definition.Nodes...)
	for index := range redacted.Nodes {
		var value interface{}
		if json.Unmarshal(redacted.Nodes[index].Config, &value) != nil {
			continue
		}
		redactedValue := redactWorkflowJSONValue(value, fmt.Sprintf("$.workflow.nodes[%d].config", index), sensitiveFields)
		encoded, err := json.Marshal(redactedValue)
		if err == nil {
			redacted.Nodes[index].Config = encoded
		}
	}
	return &redacted
}

func redactWorkflowJSONValue(value interface{}, path string, sensitiveFields *[]string) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			childPath := path + "." + key
			if workflowImportSensitiveKeyRE.MatchString(key) {
				*sensitiveFields = append(*sensitiveFields, childPath)
				continue
			}
			out[key] = redactWorkflowJSONValue(child, childPath, sensitiveFields)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = redactWorkflowJSONValue(child, fmt.Sprintf("%s[%d]", path, index), sensitiveFields)
		}
		return out
	default:
		return value
	}
}

func (s *agentService) mapWorkflowImportResources(
	ctx context.Context,
	config *types.CustomAgentConfig,
) ([]types.WorkflowImportResourceMapping, []string) {
	refs, err := workflowruntime.CollectResources(config.Workflow)
	if err != nil {
		return []types.WorkflowImportResourceMapping{}, []string{}
	}
	mappings := make([]types.WorkflowImportResourceMapping, 0)
	missing := make([]string, 0)
	catalog, _ := s.WorkflowCatalog(ctx, &types.CustomAgent{Config: *config})
	builtin := make(map[string]struct{}, len(workflowruntime.BuiltinToolAllowlist))
	for name := range workflowruntime.BuiltinToolAllowlist {
		builtin[name] = struct{}{}
	}
	for _, name := range refs.BuiltinTools {
		status := "available"
		if _, ok := builtin[name]; !ok {
			status = "missing"
			missing = append(missing, "builtin:"+name)
		}
		mappings = append(mappings, types.WorkflowImportResourceMapping{Kind: "builtin", Reference: name, ResolvedID: name, Status: status})
	}
	permissions := kbReadPermissions(ctx, s.kbShareService)
	for _, id := range refs.KnowledgeBaseIDs {
		status := "missing"
		if s.knowledgeBaseService != nil {
			kb, kbErr := s.knowledgeBaseService.GetKnowledgeBaseByIDOnly(ctx, id)
			if kbErr == nil && kb != nil {
				allowed, permissionErr := permissions.Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
				if permissionErr == nil && allowed {
					status = "available"
				}
			}
		}
		if status == "missing" {
			missing = append(missing, "knowledge-base:"+id)
		}
		mappings = append(mappings, types.WorkflowImportResourceMapping{Kind: "knowledge-base", Reference: id, ResolvedID: id, Status: status})
	}
	for _, id := range refs.MCPServiceIDs {
		status := "missing"
		if catalog != nil {
			for _, service := range catalog.MCPServices {
				if service.ID == id {
					status = "available"
					break
				}
			}
		}
		if status == "missing" {
			missing = append(missing, "mcp:"+id)
		}
		mappings = append(mappings, types.WorkflowImportResourceMapping{Kind: "mcp", Reference: id, ResolvedID: id, Status: status})
	}
	for _, name := range refs.SkillNames {
		status := "missing"
		if catalog != nil {
			for _, skill := range catalog.Skills {
				if skill.Name == name {
					status = "available"
					break
				}
			}
		}
		if status == "missing" {
			missing = append(missing, "skill:"+name)
		}
		mappings = append(mappings, types.WorkflowImportResourceMapping{Kind: "skill", Reference: name, ResolvedID: name, Status: status})
	}
	return mappings, workflowUniqueStrings(missing)
}

func workflowUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// ListWorkflowRuns 返回工作流运行列表及是否还有下一页。
//
// @param ctx 当前请求上下文。
// @param agentID 工作流智能体 ID。
// @param query 游标和筛选条件。
// @returns 运行记录、下一页标记及错误。
func (s *agentService) ListWorkflowRuns(ctx context.Context, agentID string, query types.WorkflowRunQuery) ([]*types.WorkflowRun, bool, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, false, ErrInvalidTenantID
	}
	return s.workflowRepo.ListRuns(ctx, tenantID, agentID, query)
}

// GetWorkflowRun 返回单次运行及其节点轨迹。
//
// @param ctx 当前请求上下文。
// @param agentID 工作流智能体 ID。
// @param runID 运行记录 ID。
// @returns 运行详情。
func (s *agentService) GetWorkflowRun(ctx context.Context, agentID, runID string) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	return s.workflowRepo.GetRun(ctx, tenantID, agentID, runID)
}

// ListWorkflowRunEvents 返回指定 sequence 之后的持久化生命周期事件。
func (s *agentService) ListWorkflowRunEvents(
	ctx context.Context,
	agentID, runID string,
	afterSequence int64,
	limit int,
) ([]*types.WorkflowRunEvent, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	return s.workflowRepo.ListRunEventsAfter(ctx, tenantID, agentID, runID, afterSequence, limit)
}

// RequestWorkflowRunCancel 幂等记录整次运行取消请求。
func (s *agentService) RequestWorkflowRunCancel(
	ctx context.Context,
	agentID, runID string,
) (*types.WorkflowRun, bool, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, false, ErrInvalidTenantID
	}
	return s.workflowRepo.RequestRunCancel(ctx, tenantID, agentID, runID, time.Now())
}

// DecodeWorkflowVersionDefinition 将数据库快照解码并迁移到当前格式。
//
// @param record 发布版本记录。
// @returns 可执行的工作流定义和配置快照。
func DecodeWorkflowVersionDefinition(record *types.WorkflowVersionRecord) (*types.WorkflowDefinition, *types.CustomAgentConfig, error) {
	if record == nil {
		return nil, nil, repository.ErrWorkflowVersionNotFound
	}
	var definition types.WorkflowDefinition
	if err := json.Unmarshal(record.Definition, &definition); err != nil {
		return nil, nil, fmt.Errorf("decode workflow definition snapshot: %w", err)
	}
	if err := workflowruntime.MigrateDefinition(&definition); err != nil {
		return nil, nil, err
	}
	var config types.CustomAgentConfig
	if len(record.ConfigSnapshot) > 0 {
		if err := json.Unmarshal(record.ConfigSnapshot, &config); err != nil {
			return nil, nil, fmt.Errorf("decode workflow config snapshot: %w", err)
		}
	}
	config.Workflow = &definition
	config.AgentType = types.AgentTypeWorkflow
	return &definition, &config, nil
}

// DeleteWorkflowRunsOlderThan 删除超过保留期限的工作流运行记录。
//
// @param ctx 后台清理上下文。
// @param cutoff 保留时间边界。
// @returns 删除的运行数量。
func (s *agentService) DeleteWorkflowRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.workflowRepo.DeleteRunsOlderThan(ctx, cutoff)
}

// DeleteWorkflowRunsOlderThanMode 按运行模式删除过期记录。
func (s *agentService) DeleteWorkflowRunsOlderThanMode(
	ctx context.Context,
	cutoff time.Time,
	runMode string,
) (int64, error) {
	return s.workflowRepo.DeleteRunsOlderThanMode(ctx, cutoff, runMode)
}

// CreateWorkflowRun 写入一条工作流运行记录。
//
// 暴露给执行器（而非让 sessionService 直接持有仓储）是因为运行记录的租户与
// 智能体归属由 agentService 统一判定，执行器只负责把观测数据交出来。
//
// @param ctx 当前请求上下文。
// @param run 待写入的运行记录。
// @returns 写入错误。
func (s *agentService) CreateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error {
	if s.workflowRepo == nil {
		return repository.ErrWorkflowRunNotFound
	}
	return s.workflowRepo.CreateRun(ctx, run)
}

// UpdateWorkflowRun 更新工作流运行记录的状态、摘要与耗时。
//
// @param ctx 当前请求上下文。
// @param run 已带 ID 的运行记录。
// @returns 更新错误。
func (s *agentService) UpdateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error {
	if s.workflowRepo == nil {
		return repository.ErrWorkflowRunNotFound
	}
	return s.workflowRepo.UpdateRun(ctx, run)
}

// CreateWorkflowRunNode 写入一条节点运行记录，并回填自增主键供后续更新使用。
//
// @param ctx 当前请求上下文。
// @param node 待写入的节点记录。
// @returns 写入错误。
func (s *agentService) CreateWorkflowRunNode(ctx context.Context, node *types.WorkflowRunNode) error {
	if s.workflowRepo == nil {
		return repository.ErrWorkflowRunNotFound
	}
	return s.workflowRepo.CreateRunNode(ctx, node)
}

// UpdateWorkflowRunNode 更新节点运行记录的终态、耗时与摘要。
//
// @param ctx 当前请求上下文。
// @param node 已带 ID 的节点记录。
// @returns 更新错误。
func (s *agentService) UpdateWorkflowRunNode(ctx context.Context, node *types.WorkflowRunNode) error {
	if s.workflowRepo == nil {
		return repository.ErrWorkflowRunNotFound
	}
	return s.workflowRepo.UpdateRunNode(ctx, node)
}

// CreateWorkflowRunEvent 持久化一条生命周期事件。
func (s *agentService) CreateWorkflowRunEvent(ctx context.Context, runEvent *types.WorkflowRunEvent) error {
	if s.workflowRepo == nil {
		return repository.ErrWorkflowRunNotFound
	}
	return s.workflowRepo.CreateRunEvent(ctx, runEvent)
}
