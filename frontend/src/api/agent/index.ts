import { get, post, put, del } from "../../utils/request";

// 智能体配置
// 智能推理下的智能体类型预设 ID
// 'rag-qa'       : 经典文档/FAQ 分块 RAG
// 'wiki-qa'      : Wiki 图谱导航问答
// 'hybrid-rag-wiki': Wiki + 分块混合检索
// 'custom'       : 完全自定义（不应用预设）
// 'workflow'     : 画布编排的确定性 DAG
export type AgentType = 'rag-qa' | 'wiki-qa' | 'hybrid-rag-wiki' | 'data-analysis' | 'workflow' | 'custom';

export type WorkflowNodeType =
  | 'start'
  | 'knowledge-retrieval'
  | 'llm'
  | 'llm-decision'
  | 'http-request'
  | 'tool'
  | 'end';

/** 分支模式：first_match 按稳定顺序只执行首个命中的分支；all_match 并行执行全部命中的分支。 */
export type WorkflowBranchMode = 'first_match' | 'all_match';

export interface WorkflowLLMNodeConfig {
  system_prompt?: string;
  prompt: string;
  temperature?: number;
  max_tokens?: number;
}

export type WorkflowToolKind = 'builtin' | 'mcp' | 'skill';

export interface WorkflowPosition {
  x: number;
  y: number;
}

export interface WorkflowViewport {
  x: number;
  y: number;
  zoom: number;
}

export interface WorkflowConditionItem {
  variable: string;
  operator: string;
  value?: unknown;
}

export interface WorkflowCondition {
  mode: 'all' | 'any';
  items: WorkflowConditionItem[];
}

export interface WorkflowNode {
  id: string;
  type: WorkflowNodeType;
  name: string;
  position: WorkflowPosition;
  /** 分支模式：first_match=按顺序只走首个命中分支；all_match=并行执行全部命中分支；新建节点默认 first_match。 */
  branch_mode?: WorkflowBranchMode;
  config: Record<string, unknown>;
}

export interface WorkflowEdge {
  id: string;
  source: string;
  target: string;
  source_handle?: string;
  target_handle?: string;
  order: number;
  is_default: boolean;
  condition?: WorkflowCondition;
}

export interface WorkflowDefinition {
  /** 导入导出与新接口使用的明确格式版本；后端读写该字段，旧数据可能缺省。 */
  schema_version?: number;
  version: number;
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
  viewport: WorkflowViewport;
}

export interface WorkflowCatalogTool {
  name: string;
  display_name?: string;
  description?: string;
  parameters: Record<string, unknown> | unknown[];
  require_approval?: boolean;
}

export interface WorkflowCatalogService {
  id: string;
  name: string;
  description?: string;
  tools: WorkflowCatalogTool[];
}

export interface WorkflowCatalogSkill {
  name: string;
  version?: string;
  description?: string;
}

export interface WorkflowCatalog {
  builtin_tools: WorkflowCatalogTool[];
  mcp_services: WorkflowCatalogService[];
  skills: WorkflowCatalogSkill[];
}

export interface QuestionSuggestionConfig {
  starters: {
    enabled: boolean;
    mode: 'curated' | 'knowledge' | 'hybrid';
    items: string[];
    count: number;
  };
  follow_ups: {
    enabled: boolean;
    mode: 'generated' | 'knowledge' | 'hybrid';
    count: number;
    model_id?: string;
    additional_instruction?: string;
    categories: Array<'clarify' | 'deepen' | 'action'>;
    max_context_turns: number;
    suppress_on_fallback: boolean;
    suppress_when_answer_asks_question: boolean;
    knowledge_fallback: boolean;
    allow_regenerate: boolean;
  };
}

export interface CustomAgentConfig {
  // ===== 基础设置 =====
  agent_mode?: 'quick-answer' | 'smart-reasoning';  // 运行模式：quick-answer=RAG模式, smart-reasoning=ReAct Agent模式
  // 智能推理模式下的类型预设，用于一键应用"系统提示词 + 工具 + KB 兼容性"组合
  // 仅在 agent_mode === 'smart-reasoning' 时生效；quick-answer 模式忽略
  agent_type?: AgentType;
  workflow?: WorkflowDefinition;
  system_prompt?: string;           // 统一系统提示词（使用 {{web_search_status}} 占位符动态控制行为）
  system_prompt_id?: string;        // 引用的 prompt template ID（预设会填入此字段）
  context_template_id?: string;     // Inherit the referenced context template when text is empty
  context_template?: string;        // 上下文模板（普通模式）

  // ===== 模型设置 =====
  model_id?: string;
  rerank_model_id?: string;         // ReRank 模型 ID
  temperature?: number;
  max_completion_tokens?: number;   // 0 = 跟随系统默认（快速问答 2048；智能推理 4096，绑沙箱可写文件时 24576）。大于 0 为自定义上限
  thinking?: boolean;                      // 是否启用思考模式（支持扩展思考的模型）
  citation_enabled?: boolean;        // 是否在最终回答中输出知识库/网页来源引用（默认开启）

  // ===== Agent模式设置 =====
  max_iterations?: number;          // 最大迭代次数；-1 表示不限制
  llm_call_timeout?: number;        // LLM调用超时时间（秒）
  allowed_tools?: string[];         // 允许的工具
  reflection_enabled?: boolean;     // 是否启用反思
  // MCP服务选择模式：all=全部启用的MCP服务, selected=指定服务, none=不使用MCP
  mcp_selection_mode?: 'all' | 'selected' | 'none';
  mcp_services?: string[];          // 选择的MCP服务ID列表
  // 对话中触发 OAuth 授权时的等待超时（秒）：到点后自动跳过授权提示。
  // <=0 时使用服务端默认超时。仅对使用 OAuth 的 MCP 服务生效。
  mcp_auth_wait_timeout?: number;

  // ===== Skills设置（仅Agent模式）=====
  // Skills选择模式：all=全部预装, selected=指定, none=不使用
  skills_selection_mode?: 'all' | 'selected' | 'none';
  selected_skills?: string[];       // 选择的Skill名称列表

  // ===== 沙箱设置 =====
  // 该智能体的技能脚本运行在哪个沙箱配置上；为空表示不启用沙箱执行。
  // 指向逻辑配置而非某个具体版本，凭据轮换时无需重新指派每个智能体。
  sandbox_config_id?: string;

  // ===== 知识库设置 =====
  // 知识库选择模式：all=全部知识库, selected=指定知识库, none=不使用知识库
  kb_selection_mode?: 'all' | 'selected' | 'none';
  knowledge_bases?: string[];
  // 是否仅在显式 @ 提及时检索知识库（默认: false）
  // true: 只有用户通过 @ 明确提及知识库/文档时才检索
  // false: 根据 kb_selection_mode 自动检索知识库
  retrieve_kb_only_when_mentioned?: boolean;

  // ===== 图片上传/多模态设置 =====
  image_upload_enabled?: boolean;    // 是否启用图片上传（默认: false）
  vlm_model_id?: string;            // VLM模型ID（图片分析用）
  image_storage_provider?: string;   // 图片存储提供商
  audio_upload_enabled?: boolean;    // 是否启用音频上传/ASR转录（默认: false）
  asr_model_id?: string;            // ASR模型ID（音频转录用）
  // 附件图片理解 / 扫描件 OCR 开关（默认: false，开启会增加解析耗时）
  attachment_image_understanding?: boolean;
  // 扫描件 OCR 最大页数（0 = 使用全局默认 WEKNORA_CHAT_ATTACHMENT_OCR_MAX_PAGES）
  attachment_ocr_max_pages?: number;
  // 单轮问答等待附件解析完成的最长时间（秒，0 = 使用全局默认 WEKNORA_CHAT_ATTACHMENT_WAIT_TIMEOUT_SEC）
  attachment_parse_wait_timeout_sec?: number;

  // ===== 聊天附件解析引擎策略 =====
  // 按文件类型选择解析引擎；优先级：请求 parser_engine > 智能体规则 > 租户规则 > auto
  chat_parser_engine_rules?: { file_types: string[]; engine: string }[];

  // ===== 文件类型限制 =====
  // 支持的文件类型（如 ["csv", "xlsx", "xls"]）
  // 为空表示支持所有文件类型
  supported_file_types?: string[];

  // ===== 网络搜索设置 =====
  web_search_enabled?: boolean;
  web_search_provider_id?: string;
  web_search_max_results?: number;

  // ===== 多轮对话设置 =====
  multi_turn_enabled?: boolean;     // 是否启用多轮对话
  history_turns?: number;           // 保留历史轮数

  // ===== 长期记忆 =====
  // 该智能体是否可以读取用户的长期记忆。
  // 缺省（旧数据）等同于 true：这是一个只能"关"的开关，空间设置关闭时
  // 这里打开也不会生效。
  memory_enabled?: boolean;

  // ===== 检索策略设置 =====
  embedding_top_k?: number;         // 向量召回TopK
  keyword_threshold?: number;       // 关键词召回阈值
  vector_threshold?: number;        // 向量召回阈值
  rerank_top_k?: number;            // 重排TopK
  rerank_threshold?: number;        // 重排阈值

  // ===== 高级设置（主要用于普通模式）=====
  enable_query_expansion?: boolean; // 是否启用查询扩展
  enable_rewrite?: boolean;         // 是否启用问题改写
  rewrite_prompt_system?: string;   // 改写系统提示词
  rewrite_prompt_user?: string;     // 改写用户提示词模板
  fallback_strategy?: 'fixed' | 'model'; // 兜底策略
  fallback_response?: string;       // 固定兜底回复
  fallback_prompt?: string;         // 兜底提示词（模型生成时）
  // 意图提示词：非检索意图（问候、闲聊等）时覆盖主系统提示词
  intent_prompts?: Record<string, string>;

  // ===== 已废弃字段（保留兼容）=====
  welcome_message?: string;
  question_suggestions?: QuestionSuggestionConfig;
}

// 智能体
export interface CustomAgent {
  id: string;
  name: string;
  description?: string;
  avatar?: string;
  is_builtin: boolean;
  tenant_id?: number;
  /**
   * 草稿修订号：每次保存工作流草稿自增，用于保存时的乐观锁（从 1 开始）。
   * 注意：保存接口的后端 UpdateAgentRequest DTO 尚未声明 expected_revision
   * （见 internal/handler/custom_agent.go 的 UpdateAgentRequest），当前传了也会被忽略。
   */
  draft_revision?: number;
  /** 当前对外生效的不可变发布版本号；0 表示从未发布。 */
  published_version?: number;
  created_by?: string;
  // creator_name 由后端 list 接口批量回填，仅用于列表卡片来源徽章。
  creator_name?: string;
  config: CustomAgentConfig;
  created_at?: string;
  updated_at?: string;
}

// 创建智能体请求
export interface CreateAgentRequest {
  name: string;
  description?: string;
  avatar?: string;
  config?: CustomAgentConfig;
}

// 更新智能体请求
export interface UpdateAgentRequest {
  name: string;
  description?: string;
  avatar?: string;
  config?: CustomAgentConfig;
  /**
   * 保存工作流草稿时回传的乐观锁修订号，字段名与发布接口保持一致。
   * 后端 DTO 支持前该字段会被忽略；一旦后端补上即可直接生效。
   */
  expected_revision?: number;
}

// 内置智能体 ID（常用的保留常量，便于代码引用）
export const BUILTIN_QUICK_ANSWER_ID = 'builtin-quick-answer';
export const BUILTIN_SMART_REASONING_ID = 'builtin-smart-reasoning';

// AgentMode 常量
export const AGENT_MODE_QUICK_ANSWER = 'quick-answer';
export const AGENT_MODE_SMART_REASONING = 'smart-reasoning';

// Deprecated: Use BUILTIN_QUICK_ANSWER_ID instead
export const BUILTIN_AGENT_NORMAL_ID = BUILTIN_QUICK_ANSWER_ID;
// Deprecated: Use BUILTIN_SMART_REASONING_ID instead
export const BUILTIN_AGENT_AGENT_ID = BUILTIN_SMART_REASONING_ID;

// 获取智能体列表（包括内置智能体）
// disabled_own_agent_ids: 当前空间在对话下拉中停用的「我的」智能体 ID，仅影响本空间
export function listAgents(params?: {
  /**
   * Optional creator filter; mirrors listKnowledgeBases. Built-in agents
   * (is_builtin=true) are always returned regardless of this filter so
   * the conversation dropdown never silently loses quick-answer /
   * smart-reasoning when a user picks "Created by me".
   */
  creator?: 'all' | 'mine' | 'others';
}) {
  const qs = params?.creator && params.creator !== 'all' ? `?creator=${params.creator}` : '';
  return get<{ data: CustomAgent[]; disabled_own_agent_ids?: string[] }>(`/api/v1/agents${qs}`);
}

// 获取智能体详情
export function getAgentById(id: string) {
  return get<{ data: CustomAgent }>(`/api/v1/agents/${id}`);
}

// 获取当前工作流智能体有权使用的资源目录
export function getWorkflowCatalog(id: string) {
  return get<{ data: WorkflowCatalog }>(`/api/v1/agents/${id}/workflow/catalog`);
}

// 创建智能体
export function createAgent(data: CreateAgentRequest) {
  return post<{ data: CustomAgent }>('/api/v1/agents', data);
}

// 更新智能体
export function updateAgent(id: string, data: UpdateAgentRequest) {
  return put<{ data: CustomAgent }>(`/api/v1/agents/${id}`, data);
}

// 删除智能体
export function deleteAgent(id: string) {
  return del<{ success: boolean }>(`/api/v1/agents/${id}`);
}

// 复制智能体
export function copyAgent(id: string) {
  return post<{ data: CustomAgent }>(`/api/v1/agents/${id}/copy`);
}

// 判断是否为内置智能体（通过 agent.is_builtin 字段或 ID 前缀判断）
export function isBuiltinAgent(agentId: string): boolean {
  return agentId.startsWith('builtin-');
}

// 占位符定义
export interface PlaceholderDefinition {
  name: string;
  label: string;
  description: string;
}

// 占位符响应
export interface PlaceholdersResponse {
  all: PlaceholderDefinition[];
  system_prompt: PlaceholderDefinition[];
  agent_system_prompt: PlaceholderDefinition[];
  context_template: PlaceholderDefinition[];
  rewrite_system_prompt: PlaceholderDefinition[];
  rewrite_prompt: PlaceholderDefinition[];
  fallback_prompt: PlaceholderDefinition[];
}

// 获取占位符定义
export function getPlaceholders() {
  return get<{ data: PlaceholdersResponse }>('/api/v1/agents/placeholders');
}

// ===== 智能体类型预设 =====

// 后端 kb_filter 结构（见 internal/types/agent_type_preset.go）
export interface AgentTypeKBFilter {
  any_of?: string[];   // KB 至少拥有其一
  all_of?: string[];   // KB 必须全部拥有
  none_of?: string[];  // KB 必须全部不拥有
}

// KB 能力标签（后端 types.KBCapabilities 的 JSON）
export interface KBCapabilities {
  vector: boolean;
  keyword: boolean;
  wiki: boolean;
  graph: boolean;
  faq: boolean;
}

// 预设的"自动填充"配置载荷：仅包含被预设覆盖的字段；其他字段不动
export interface AgentTypePresetConfig {
  system_prompt_id?: string;
  temperature?: number;
  max_iterations?: number;
  allowed_tools?: string[];
  retain_retrieval_history?: boolean;
  faq_priority_enabled?: boolean;
  web_search_enabled?: boolean;
  supported_file_types?: string[];
  kb_selection_mode?: 'all' | 'selected' | 'none';
}

export interface AgentTypePresetI18n {
  label: string;
  description: string;
}

export interface AgentTypePreset {
  id: AgentType;
  i18n: Record<string, AgentTypePresetI18n>;
  config?: AgentTypePresetConfig;     // 为空表示"自定义"类型（无预设）
  kb_filter?: AgentTypeKBFilter;      // 为空表示所有 KB 可选
}

// 拉取类型预设列表（编辑器用）
export function getAgentTypePresets() {
  return get<{ data: AgentTypePreset[] }>('/api/v1/agents/type-presets');
}

// ===== IM渠道 =====

export interface IMChannel {
  id: string;
  tenant_id?: number;
  agent_id: string;
  // 'lark' is Feishu's international edition; it shares Feishu's credentials and modes.
  platform: 'wecom' | 'feishu' | 'lark' | 'slack' | 'telegram' | 'dingtalk' | 'mattermost' | 'wechat' | 'qqbot' | 'yunzhijia';
  name: string;
  enabled: boolean;
  mode: 'webhook' | 'websocket' | 'longpoll';
  output_mode: 'stream' | 'full';
  session_mode?: 'user' | 'thread';
  knowledge_base_id?: string;
  credentials: Record<string, any>;
  created_at?: string;
  updated_at?: string;
}

export function listIMChannels(agentId: string) {
  return get<{ data: IMChannel[] }>(`/api/v1/agents/${agentId}/im-channels`);
}

// Tenant-wide overview row. Credentials are intentionally omitted — use
// listIMChannels(agentId) when you need to edit a specific channel.
export interface IMChannelOverview {
  id: string;
  tenant_id: number;
  agent_id: string;
  agent_name: string; // localized built-in name when the agent is built-in
  platform: IMChannel['platform'];
  name: string;
  enabled: boolean;
  mode: IMChannel['mode'];
  output_mode: IMChannel['output_mode'];
  session_mode?: IMChannel['session_mode'];
  bot_identity: string;
  created_at: string;
  updated_at: string;
}

export function listAllIMChannels() {
  return get<{ data: IMChannelOverview[] }>('/api/v1/im-channels');
}

export function createIMChannel(agentId: string, data: Partial<IMChannel>) {
  return post<{ data: IMChannel }>(`/api/v1/agents/${agentId}/im-channels`, data);
}

export function updateIMChannel(id: string, data: Partial<IMChannel>) {
  return put<{ data: IMChannel }>(`/api/v1/im-channels/${id}`, data);
}

export function deleteIMChannel(id: string) {
  return del<{ success: boolean }>(`/api/v1/im-channels/${id}`);
}

export function toggleIMChannel(id: string) {
  return post<{ data: IMChannel }>(`/api/v1/im-channels/${id}/toggle`);
}

// ===== 推荐问题 =====

// 推荐问题
export interface SuggestedQuestion {
  question: string;
  source: 'faq' | 'document' | 'agent_config' | 'wiki';
  knowledge_base_id?: string;
}

// 获取智能体推荐问题
// 根据智能体关联的知识库范围返回推荐问题，用于前端对话面板快捷提问
export function getSuggestedQuestions(
  agentId: string,
  params?: {
    knowledge_base_ids?: string[];
    knowledge_ids?: string[];
    tag_scopes?: Array<{ knowledge_base_id: string; tag_ids: string[] }>;
    limit?: number;
  }
) {
  const query = new URLSearchParams();
  if (params?.knowledge_base_ids?.length) query.set('knowledge_base_ids', params.knowledge_base_ids.join(','));
  if (params?.knowledge_ids?.length) query.set('knowledge_ids', params.knowledge_ids.join(','));
  if (params?.tag_scopes?.length) query.set('tag_scopes', JSON.stringify(params.tag_scopes));
  if (params?.limit) query.set('limit', String(params.limit));
  const qs = query.toString();
  return get<{ data: { questions: SuggestedQuestion[] } }>(`/api/v1/agents/${agentId}/suggested-questions${qs ? '?' + qs : ''}`);
}
// ===== WeChat QR Code Login =====

export interface WeChatQRCodeResult {
  qrcode_url: string;
  qrcode: string;
}

export interface WeChatQRCodeStatus {
  status: 'wait' | 'scaned' | 'confirmed' | 'expired';
  credentials?: {
    bot_token: string;
    ilink_bot_id: string;
    ilink_user_id: string;
  };
  baseurl?: string;
}

export function getWeChatQRCode() {
  return post<{ data: WeChatQRCodeResult }>('/api/v1/wechat/qrcode');
}

export function pollWeChatQRCodeStatus(qrcode: string) {
  return post<{ data: WeChatQRCodeStatus }>('/api/v1/wechat/qrcode/status', { qrcode });
}

// ===== 工作流发布 / 运行记录 =====

/** 运行状态：与后端 types.WorkflowRunStatus* 一一对应。 */
export type WorkflowRunStatus = 'running' | 'succeeded' | 'partial' | 'failed' | 'canceled';

/** 触发来源：决定这条运行是从哪个入口触发的。 */
export type WorkflowRunTriggerSource = 'chat' | 'share' | 'im' | 'embed' | 'debug';

/** 不可变的工作流发布版本快照（对应后端 WorkflowVersionRecord）。 */
export interface WorkflowVersionRecord {
  tenant_id: number;
  agent_id: string;
  version: number;
  /** 发布时的草稿修订号；与当前草稿 revision 比较即可判定是否有未发布修改。 */
  draft_revision: number;
  definition: WorkflowDefinition;
  /** 发布时的智能体配置快照；旧版本可能为空。 */
  config_snapshot?: CustomAgentConfig;
  published_by?: string;
  published_at: string;
}

/** 结构化校验问题；node_id/edge_id/field_path 用于把问题定位回画布。 */
export interface WorkflowValidationIssue {
  code: string;
  message: string;
  node_id?: string;
  edge_id?: string;
  field_path?: string;
}

export interface WorkflowDebugInput {
  query: string;
  attachments_text?: string;
}

export interface WorkflowImportResourceMapping {
  kind: string;
  reference: string;
  resolved_id?: string;
  status: 'available' | 'missing' | string;
}

export interface WorkflowImportPreview {
  config?: CustomAgentConfig;
  definition?: WorkflowDefinition;
  issues: WorkflowValidationIssue[];
  warnings: WorkflowValidationIssue[];
  missing_resources: string[];
  sensitive_fields: string[];
  resource_mappings: WorkflowImportResourceMapping[];
}

/** 单次运行中的节点执行记录（对应后端 WorkflowRunNode）。 */
export interface WorkflowRunNode {
  id: number;
  run_id: string;
  node_id: string;
  node_name: string;
  node_type: string;
  /** 分支路径：并行分支下用路径区分同一节点在不同分支中的执行。 */
  branch_path: string;
  sequence: number;
  attempt: number;
  retry_of?: number;
  task_id?: string;
  retryable: boolean;
  status: string;
  input_summary?: string;
  input_truncated?: boolean;
  output_summary?: string;
  output_truncated?: boolean;
  error_summary?: string;
  error_truncated?: boolean;
  usage?: Record<string, unknown>;
  started_at?: string;
  finished_at?: string;
  duration_ms: number;
}

/** 一次绑定到不可变版本的工作流执行（对应后端 WorkflowRun）。 */
export interface WorkflowRun {
  id: string;
  tenant_id: number;
  agent_id: string;
  workflow_version: number;
  draft_revision: number;
  /** 当次运行的定义快照：详情必须用它渲染，不能加载当前草稿。 */
  definition_snapshot: WorkflowDefinition;
  run_mode: 'production' | 'debug' | string;
  trigger_source: WorkflowRunTriggerSource;
  status: WorkflowRunStatus;
  requested_by?: string;
  idempotency_key?: string;
  cancel_requested_at?: string;
  last_heartbeat_at?: string;
  session_id?: string;
  message_id?: string;
  request_id?: string;
  input_summary?: string;
  input_truncated?: boolean;
  output_summary?: string;
  output_truncated?: boolean;
  error_code?: string;
  error_summary?: string;
  error_truncated?: boolean;
  usage?: Record<string, unknown>;
  started_at: string;
  finished_at?: string;
  duration_ms: number;
  created_at: string;
  nodes?: WorkflowRunNode[];
}

/** 运行列表的游标：指向上一页最后一条记录，两个字段必须成对回传。 */
export interface WorkflowRunCursor {
  started_at: string;
  id: string;
}

export interface WorkflowRunListResponse {
  items: WorkflowRun[];
  has_more: boolean;
  next_cursor: WorkflowRunCursor | null;
}

export interface ListWorkflowRunsParams {
  /** 每页数量，默认 20、上限 100。 */
  limit?: number;
  status?: WorkflowRunStatus;
  /** RFC3339，筛选起始时间（含）。 */
  started_after?: string;
  /** RFC3339，筛选结束时间（不含）。 */
  started_before?: string;
  /** 游标：上一页最后一条的 started_at（RFC3339）。 */
  before_started_at?: string;
  /** 游标：上一页最后一条的 id。必须与 before_started_at 同时提供。 */
  before_id?: string;
}

/**
 * 发布工作流草稿为新的不可变版本。
 * expected_revision 是客户端最后看到的草稿修订号，用于乐观锁；与当前草稿不一致时后端返回 409。
 */
export function publishWorkflow(id: string, expectedRevision: number) {
  return post<{ data: WorkflowVersionRecord }>(`/api/v1/agents/${id}/workflow/publish`, {
    expected_revision: expectedRevision,
  });
}

// 获取工作流发布历史，按版本号从新到旧
export function listWorkflowVersions(id: string) {
  return get<{ data: WorkflowVersionRecord[] }>(`/api/v1/agents/${id}/workflow/versions`);
}

/** 获取指定不可变版本，详情不受当前草稿影响。 */
export function getWorkflowVersion(id: string, version: number) {
  return get<{ data: WorkflowVersionRecord }>(`/api/v1/agents/${id}/workflow/versions/${version}`);
}

/** 将不可变版本复制为新的草稿修订，不会自动发布。 */
export function restoreWorkflowVersion(id: string, version: number, expectedRevision: number) {
  return post<{ data: CustomAgent }>(`/api/v1/agents/${id}/workflow/versions/${version}/restore`, {
    expected_revision: expectedRevision,
  });
}

// 结构化校验工作流定义，不落库；用于发布前把问题定位回编辑器
export function validateWorkflowDefinition(id: string, config: CustomAgentConfig) {
  return post<{ data: WorkflowValidationIssue[] }>(`/api/v1/agents/${id}/workflow/validate`, { config });
}

/** 服务端预检导入文件，返回脱敏后的归一化定义和资源映射。 */
export function previewWorkflowImport(id: string, document: unknown) {
  return post<{ data: WorkflowImportPreview }>(`/api/v1/agents/${id}/workflow/import/preview`, { document });
}

/** 在编辑器内基于当前草稿快照发起一次 debug run。 */
export function startWorkflowDebugRun(
  id: string,
  expectedRevision: number,
  input: WorkflowDebugInput,
  idempotencyKey?: string,
) {
  return post<{ data: WorkflowRun }>(`/api/v1/agents/${id}/workflow/debug-runs`, {
    expected_revision: expectedRevision,
    input,
    ...(idempotencyKey ? { idempotency_key: idempotencyKey } : {}),
  });
}

/** 请求取消一条工作流运行。 */
export function cancelWorkflowRun(id: string, runId: string) {
  return post<{ data: { run: WorkflowRun; cancel_requested: boolean } }>(
    `/api/v1/agents/${id}/workflow/runs/${runId}/cancel`,
    {},
  );
}

/** 使用原运行快照进行整次重跑。 */
export function retryWorkflowRun(id: string, runId: string) {
  return post<{ data: WorkflowRun }>(`/api/v1/agents/${id}/workflow/runs/${runId}/retry`, {});
}

/** 从指定失败节点的检查点继续运行。 */
export function retryWorkflowNode(id: string, runId: string, nodeRunId: number) {
  return post<{ data: WorkflowRun }>(
    `/api/v1/agents/${id}/workflow/runs/${runId}/nodes/${nodeRunId}/retry`,
    {},
  );
}

// 获取工作流运行列表（游标分页）
export function listWorkflowRuns(id: string, params?: ListWorkflowRunsParams) {
  const query = new URLSearchParams();
  if (params?.limit) query.set('limit', String(params.limit));
  if (params?.status) query.set('status', params.status);
  if (params?.started_after) query.set('started_after', params.started_after);
  if (params?.started_before) query.set('started_before', params.started_before);
  if (params?.before_started_at) query.set('before_started_at', params.before_started_at);
  if (params?.before_id) query.set('before_id', params.before_id);
  const qs = query.toString();
  return get<{ data: WorkflowRunListResponse }>(
    `/api/v1/agents/${id}/workflow/runs${qs ? '?' + qs : ''}`,
  );
}

// 获取工作流运行详情，含当次定义快照与节点执行记录
export function getWorkflowRun(id: string, runId: string) {
  return get<{ data: WorkflowRun }>(`/api/v1/agents/${id}/workflow/runs/${runId}`);
}

/** 后端 409 修订冲突时 error.details 的结构；请求层已把 error 对象原样抛出。 */
export interface WorkflowRevisionConflictDetails {
  current_revision?: number;
}
