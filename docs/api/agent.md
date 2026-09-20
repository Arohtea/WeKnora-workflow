# 智能体（Agent）管理 API

[返回目录](./README.md)

## 概述

智能体 API 用于管理自定义智能体（Custom Agent）。系统提供了内置智能体，同时支持用户创建自定义智能体来满足不同的业务场景需求。

> 智能体的共享与跨组织分发（`/agents/:id/shares` 等）属于组织协作能力，文档见 [组织管理 API](./organization.md)。本文件只覆盖智能体自身的 CRUD、复制、占位符、类型预设以及推荐问题接口。

### 内置智能体

系统默认提供以下内置智能体：

| ID | 名称 | 描述 | 模式 |
|----|------|------|------|
| `builtin-quick-answer` | 快速问答 | 基于知识库的 RAG 问答，快速准确地回答问题 | quick-answer |
| `builtin-smart-reasoning` | 智能推理 | ReAct 推理框架，支持多步思考和工具调用 | smart-reasoning |
| `builtin-data-analyst` | 数据分析师 | 专业数据分析智能体，支持 CSV/Excel 文件的 SQL 查询与统计分析 | smart-reasoning |

### 智能体模式

| 模式 | 说明 |
|------|------|
| `quick-answer` | RAG 模式，快速问答，直接基于知识库检索结果生成回答 |
| `smart-reasoning` | ReAct 模式，支持多步推理和工具调用 |

## API 列表

| 方法   | 路径                       | 描述                       |
| ------ | -------------------------- | -------------------------- |
| POST   | `/agents`                  | 创建智能体                 |
| GET    | `/agents`                  | 获取智能体列表             |
| GET    | `/agents/:id`              | 获取智能体详情             |
| PUT    | `/agents/:id`              | 更新智能体                 |
| DELETE | `/agents/:id`              | 删除智能体                 |
| POST   | `/agents/:id/copy`         | 复制智能体                 |
| GET    | `/agents/placeholders`     | 获取占位符定义             |
| GET    | `/agents/:id/workflow/catalog`  | 获取工作流资源目录       |
| POST   | `/agents/:id/workflow/validate` | 校验工作流草稿（结构化问题） |
| POST   | `/agents/:id/workflow/publish`  | 发布工作流新版本         |
| GET    | `/agents/:id/workflow/versions` | 获取工作流发布历史       |
| GET    | `/agents/:id/workflow/versions/:version` | 获取指定发布版本 |
| POST   | `/agents/:id/workflow/versions/:version/restore` | 恢复版本为新草稿 |
| POST   | `/agents/:id/workflow/import/preview` | 预检工作流导入 |
| POST   | `/agents/:id/workflow/debug-runs` | 编辑器内试跑当前草稿 |
| GET    | `/agents/:id/workflow/runs`     | 获取工作流运行记录列表   |
| GET    | `/agents/:id/workflow/runs/:run_id` | 获取工作流运行详情   |
| GET    | `/agents/:id/workflow/runs/:run_id/stream` | 续接运行生命周期事件 |
| POST   | `/agents/:id/workflow/runs/:run_id/cancel` | 取消工作流运行 |
| POST   | `/agents/:id/workflow/runs/:run_id/retry` | 使用原快照整次重跑 |
| POST   | `/agents/:id/workflow/runs/:run_id/nodes/:node_run_id/retry` | 从失败节点继续 |

---

## POST `/agents` - 创建智能体

创建新的自定义智能体。成功返回 HTTP 201。

**请求体参数**:

| 参数          | 类型   | 必填 | 说明                                              |
| ------------- | ------ | ---- | ------------------------------------------------- |
| `name`        | string | 是   | 智能体名称                                        |
| `description` | string | 否   | 智能体描述                                        |
| `avatar`      | string | 否   | 头像（emoji 或图标名称）                          |
| `config`      | object | 否   | 智能体配置，详见 [配置参数](#配置参数)            |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "我的智能体",
    "description": "自定义智能体描述",
    "avatar": "🤖",
    "config": {
        "agent_mode": "smart-reasoning",
        "system_prompt": "你是一个专业的助手...",
        "temperature": 0.7,
        "max_iterations": 10,
        "kb_selection_mode": "all",
        "web_search_enabled": true,
        "multi_turn_enabled": true,
        "history_turns": 5
    }
}'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "我的智能体",
        "description": "自定义智能体描述",
        "avatar": "🤖",
        "is_builtin": false,
        "tenant_id": 1,
        "created_by": "user-123",
        "config": {
            "agent_mode": "smart-reasoning",
            "system_prompt": "你是一个专业的助手...",
            "temperature": 0.7,
            "max_iterations": 10
        },
        "created_at": "2025-01-19T10:00:00Z",
        "updated_at": "2025-01-19T10:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误                  | 说明                              |
| ------ | ------ | --------------------- | --------------------------------- |
| 400    | 1000   | Bad Request           | 请求参数错误或智能体名称为空      |
| 500    | 1007   | Internal Server Error | 服务器内部错误                    |

---

## GET `/agents` - 获取智能体列表

获取当前空间的所有智能体，包括内置智能体和自定义智能体。响应中额外返回 `disabled_own_agent_ids`，指示当前空间在前端对话下拉框中主动隐藏的本空间自有智能体 ID 列表（不影响其他空间）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "id": "builtin-quick-answer",
            "name": "快速问答",
            "description": "基于知识库的 RAG 问答，快速准确地回答问题",
            "avatar": "💬",
            "is_builtin": true,
            "tenant_id": 10000,
            "created_by": "",
            "config": {
                "agent_mode": "quick-answer",
                "temperature": 0.3,
                "max_completion_tokens": 2048,
                "kb_selection_mode": "all",
                "web_search_enabled": false,
                "multi_turn_enabled": true,
                "history_turns": 5
            },
            "created_at": "2025-12-29T20:06:01.696308+08:00",
            "updated_at": "2025-12-29T20:06:01.696308+08:00",
            "deleted_at": null
        },
        {
            "id": "550e8400-e29b-41d4-a716-446655440000",
            "name": "我的智能体",
            "is_builtin": false,
            "config": {
                "agent_mode": "smart-reasoning"
            }
        }
    ],
    "disabled_own_agent_ids": []
}
```

**错误响应**:

| 状态码 | 错误码 | 错误                  | 说明               |
| ------ | ------ | --------------------- | ------------------ |
| 401    | 1001   | Unauthorized          | 缺少空间上下文     |
| 500    | 1007   | Internal Server Error | 服务器内部错误     |

---

## GET `/agents/:id` - 获取智能体详情

根据 ID 获取智能体的详细信息。

**路径参数**:

| 参数 | 类型   | 说明     |
| ---- | ------ | -------- |
| `id` | string | 智能体 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/builtin-quick-answer' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "builtin-quick-answer",
        "name": "快速问答",
        "description": "基于知识库的 RAG 问答，快速准确地回答问题",
        "is_builtin": true,
        "tenant_id": 1,
        "config": {
            "agent_mode": "quick-answer",
            "system_prompt": "",
            "context_template": "请根据以下参考资料回答用户问题...",
            "temperature": 0.7,
            "max_completion_tokens": 2048,
            "kb_selection_mode": "all",
            "web_search_enabled": true,
            "multi_turn_enabled": true,
            "history_turns": 5
        },
        "created_at": "2025-01-01T00:00:00Z",
        "updated_at": "2025-01-01T00:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误                  | 说明               |
| ------ | ------ | --------------------- | ------------------ |
| 400    | 1000   | Bad Request           | 智能体 ID 为空     |
| 404    | 1003   | Not Found             | 智能体不存在       |
| 500    | 1007   | Internal Server Error | 服务器内部错误     |

---

## PUT `/agents/:id` - 更新智能体

更新智能体的名称、描述、头像和配置。内置智能体不可修改。

**路径参数**:

| 参数 | 类型   | 说明     |
| ---- | ------ | -------- |
| `id` | string | 智能体 ID |

**请求体参数**:

| 参数          | 类型   | 必填 | 说明         |
| ------------- | ------ | ---- | ------------ |
| `name`        | string | 否   | 智能体名称   |
| `description` | string | 否   | 智能体描述   |
| `avatar`      | string | 否   | 智能体头像   |
| `config`      | object | 否   | 智能体配置   |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/agents/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "更新后的智能体",
    "description": "更新后的描述",
    "config": {
        "agent_mode": "smart-reasoning",
        "temperature": 0.8,
        "max_iterations": 20
    }
}'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "更新后的智能体",
        "description": "更新后的描述",
        "config": {
            "agent_mode": "smart-reasoning",
            "temperature": 0.8,
            "max_iterations": 20
        },
        "updated_at": "2025-01-19T11:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误                  | 说明                              |
| ------ | ------ | --------------------- | --------------------------------- |
| 400    | 1000   | Bad Request           | 请求参数错误或智能体名称为空      |
| 403    | 1002   | Forbidden             | 无法修改内置智能体                |
| 404    | 1003   | Not Found             | 智能体不存在                      |
| 500    | 1007   | Internal Server Error | 服务器内部错误                    |

---

## DELETE `/agents/:id` - 删除智能体

删除指定的自定义智能体。内置智能体不可删除。

**路径参数**:

| 参数 | 类型   | 说明     |
| ---- | ------ | -------- |
| `id` | string | 智能体 ID |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/agents/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "message": "Agent deleted successfully"
}
```

**错误响应**:

| 状态码 | 错误码 | 错误                  | 说明                  |
| ------ | ------ | --------------------- | --------------------- |
| 400    | 1000   | Bad Request           | 智能体 ID 为空        |
| 403    | 1002   | Forbidden             | 无法删除内置智能体    |
| 404    | 1003   | Not Found             | 智能体不存在          |
| 500    | 1007   | Internal Server Error | 服务器内部错误        |

---

## POST `/agents/:id/copy` - 复制智能体

复制指定的智能体，创建一个新的副本，副本始终为自定义智能体。支持复制内置智能体。成功返回 HTTP 201。

**路径参数**:

| 参数 | 类型   | 说明           |
| ---- | ------ | -------------- |
| `id` | string | 源智能体 ID    |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/agents/builtin-smart-reasoning/copy' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "660e8400-e29b-41d4-a716-446655440001",
        "name": "智能推理 (副本)",
        "description": "ReAct 推理框架，支持多步思考和工具调用",
        "is_builtin": false,
        "config": {
            "agent_mode": "smart-reasoning",
            "max_iterations": 50
        },
        "created_at": "2025-01-19T12:00:00Z",
        "updated_at": "2025-01-19T12:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误                  | 说明               |
| ------ | ------ | --------------------- | ------------------ |
| 400    | 1000   | Bad Request           | 智能体 ID 为空     |
| 404    | 1003   | Not Found             | 智能体不存在       |
| 500    | 1007   | Internal Server Error | 服务器内部错误     |

---

## GET `/agents/placeholders` - 获取占位符定义

获取所有可用的提示词占位符定义，按字段类型分组。这些占位符可用于系统提示词和上下文模板中。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/placeholders' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "all": [...],
        "system_prompt": [...],
        "agent_system_prompt": [...],
        "context_template": [...],
        "rewrite_system_prompt": [...],
        "rewrite_prompt": [...],
        "fallback_prompt": [...]
    }
}
```

---

## 配置参数

智能体的 `config` 对象支持以下配置项：

### 基础设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `agent_mode` | string | - | 智能体模式：`quick-answer`（RAG）或 `smart-reasoning`（ReAct） |
| `system_prompt` | string | - | 系统提示词，支持使用占位符 |
| `system_prompt_id` | string | - | 系统提示词模板 ID（引用 `prompt_templates/` YAML 文件中的模板） |
| `context_template` | string | - | 上下文模板（仅 quick-answer 模式使用） |
| `context_template_id` | string | - | 上下文模板 ID（引用 `prompt_templates/` YAML 文件中的模板） |

### 模型设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `model_id` | string | - | 对话模型 ID |
| `rerank_model_id` | string | - | 重排序模型 ID |
| `temperature` | float | 0.7 | 温度参数（0-1） |
| `max_completion_tokens` | int | 2048 | 最大生成 token 数 |
| `thinking` | *bool | nil | 是否启用思考模式（适用于支持扩展思考的模型） |

### Agent 模式设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `max_iterations` | int | 10 | ReAct 最大迭代次数。`-1` 表示不限制，直到模型自然结束或用户停止；正数上限为 100 |
| `allowed_tools` | []string | - | 允许使用的工具列表 |
| `mcp_selection_mode` | string | - | MCP 服务选择模式：`all`/`selected`/`none` |
| `mcp_services` | []string | - | 选中的 MCP 服务 ID 列表 |
| `skills_selection_mode` | string | - | Skills 选择模式：`all`/`selected`/`none` |
| `selected_skills` | []string | - | 选中的 Skill 名称列表（mode 为 `selected` 时） |

### 知识库设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `kb_selection_mode` | string | - | 知识库选择模式：`all`/`selected`/`none` |
| `knowledge_bases` | []string | - | 关联的知识库 ID 列表 |
| `retrieve_kb_only_when_mentioned` | bool | false | 仅在用户通过 @ 显式提及时才检索知识库 |
| `supported_file_types` | []string | - | 支持的文件类型（如 `["csv", "xlsx"]`） |

### 图片上传 / 多模态设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `image_upload_enabled` | bool | false | 是否允许上传图片 |
| `vlm_model_id` | string | - | 图片分析所用的 VLM 模型 ID |
| `image_storage_provider` | string | - | 图片存储提供者：`local`/`minio`/`cos`/`tos`/`oss`，为空使用全局默认 |

### FAQ 策略设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `faq_priority_enabled` | bool | true | FAQ 优先策略开关 |
| `faq_direct_answer_threshold` | float | 0.9 | FAQ 直接回答阈值 |
| `faq_score_boost` | float | 1.2 | FAQ 分数加成系数 |

### 网络搜索设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `web_search_enabled` | bool | true | 是否启用网络搜索 |
| `web_search_max_results` | int | 5 | 网络搜索最大结果数 |
| `web_search_provider_id` | string | - | 网络搜索提供者 ID，为空使用空间默认提供者 |
| `web_fetch_enabled` | bool | false | 是否自动获取重排后的搜索结果页面全文 |
| `web_fetch_top_n` | int | 3 | 重排后获取全文的最大页面数 |

### 多轮对话设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `multi_turn_enabled` | bool | true | 是否启用多轮对话 |
| `history_turns` | int | 5 | 保留的历史轮次数 |

### 检索策略设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `embedding_top_k` | int | 10 | 向量检索 TopK |
| `keyword_threshold` | float | 0.3 | 关键词检索阈值 |
| `vector_threshold` | float | 0.5 | 向量检索阈值 |
| `rerank_top_k` | int | 5 | 重排序 TopK |
| `rerank_threshold` | float | 0.5 | 重排序阈值 |

### 推荐问题设置

`question_suggestions` 是智能体拥有的统一策略。网页嵌入等渠道只能关闭展示，不能覆盖内容或生成规则。

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `question_suggestions.starters.enabled` | bool | true | 是否在首次提问前展示开场问题 |
| `question_suggestions.starters.mode` | string | `hybrid` | `curated`、`knowledge` 或 `hybrid` |
| `question_suggestions.starters.items` | []string | `[]` | 运营配置的开场问题 |
| `question_suggestions.starters.count` | int | 6 | 展示数量，范围 1-8 |
| `question_suggestions.follow_ups.enabled` | bool | false | 是否在每次完整回答后异步生成追问 |
| `question_suggestions.follow_ups.mode` | string | `hybrid` | `generated`、`knowledge` 或 `hybrid` |
| `question_suggestions.follow_ups.count` | int | 3 | 生成数量，范围 1-5 |
| `question_suggestions.follow_ups.model_id` | string | - | 独立生成模型；为空使用本轮对话模型 |
| `question_suggestions.follow_ups.categories` | []string | `clarify,deepen,action` | 允许的问题类型 |
| `question_suggestions.follow_ups.max_context_turns` | int | 2 | 生成时使用的最近对话轮数，范围 1-5 |
| `question_suggestions.follow_ups.additional_instruction` | string | - | 智能体作者的附加生成要求 |
| `question_suggestions.follow_ups.suppress_on_fallback` | bool | true | 兜底回答后不展示 |
| `question_suggestions.follow_ups.suppress_when_answer_asks_question` | bool | true | 回答本身以问题结尾时不展示 |
| `question_suggestions.follow_ups.knowledge_fallback` | bool | true | 模型失败时使用知识库候选补位 |
| `question_suggestions.follow_ups.allow_regenerate` | bool | false | 是否允许用户换一批 |

旧 `suggested_prompts` 会在数据库迁移时一次性写入 `starters.items`，API 不再接受该字段。

### 高级设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enable_query_expansion` | bool | true | 是否启用查询扩展 |
| `enable_rewrite` | bool | true | 是否启用多轮对话查询改写 |
| `rewrite_prompt_system` | string | - | 改写系统提示词 |
| `rewrite_prompt_user` | string | - | 改写用户提示词模板 |
| `fallback_strategy` | string | `model` | 回退策略：`fixed`（固定回复）或 `model`（模型生成）；未设置时在服务端默认为 `model` |
| `fallback_response` | string | - | 固定回退回复（`fallback_strategy` 为 `fixed` 时使用） |
| `fallback_prompt` | string | - | 回退提示词（`fallback_strategy` 为 `model` 时使用） |

---

## 使用 Agent 进行问答

创建或获取智能体后，可以通过 `/agent-chat/:session_id` 接口使用智能体进行问答。详情请参考 [聊天功能 API](./chat.md)。

在问答请求中使用 `agent_id` 参数指定要使用的智能体：

```curl
curl --location 'http://localhost:8080/api/v1/agent-chat/session-123' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query": "帮我分析一下这份数据",
    "agent_enabled": true,
    "agent_id": "builtin-data-analyst"
}'
```

## 工作流（Workflow）接口

工作流智能体（`agent_type: workflow`）的编排能力。版本模型、分支语义、状态归并和运行记录的设计说明见 [工作流生产可用 V1](../workflow-production-v1.md)。

### 权限

| 操作 | 权限 |
| --- | --- |
| 读取目录 / 发布历史 / 运行记录 / 脱敏事件 | Viewer 及以上 |
| 校验草稿 | Viewer 及以上（纯校验，不落库） |
| 保存、试跑、发布、恢复版本、取消、重试 | 智能体创建者或 Admin 及以上 |
| 查看完整节点输入/输出载荷 | 智能体创建者或 Admin 及以上；默认只返回脱敏摘要 |

### GET `/agents/:id/workflow/catalog`

返回该智能体有权使用的内置工具、MCP 工具和已安装 Skill，供编辑器资源选择。编辑器不再依赖前端硬编码目录。

```json
{"success": true, "data": {"builtin_tools": [...], "mcp_services": [...], "skills": [...]}}
```

### POST `/agents/:id/workflow/validate`

对**未保存的草稿**做结构化校验，返回可定位到编辑器的错误码、节点 ID、连线 ID 与字段路径。不落库、不影响草稿。

**请求体**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `config` | object | 否 | 待校验的智能体配置；缺省时按"非工作流"返回 `NOT_WORKFLOW` |

**响应**：`data` 为问题数组，无问题是空数组。

```json
{"success": true, "data": [
  {"code": "NO_MATCHING_BRANCH", "message": "...", "node_id": "n1", "edge_id": "e2", "field_path": "nodes.1.config"}
]}
```

### POST `/agents/:id/workflow/publish`

校验当前草稿并创建**不可变**发布版本。校验失败返回 `400`；草稿修订号不匹配返回 `409`。

**请求体**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `expected_revision` | integer | 是 | 客户端读取草稿时看到的 `draft_revision`（从 1 起，传 0 会被拒绝） |

**成功 200**

```json
{"success": true, "data": {
  "tenant_id": 1, "agent_id": "...", "version": 3, "draft_revision": 12,
  "definition": {"schema_version": 2, "nodes": [], "edges": [], "viewport": {}},
  "config_snapshot": {}, "published_by": "...", "published_at": "2026-01-01T00:00:00Z"
}}
```

### GET `/agents/:id/workflow/versions/:version`

获取指定不可变发布版本。版本内容只读，响应中的 `definition` 与 `config_snapshot` 是该版本的完整快照；它们不随当前草稿变化。

### POST `/agents/:id/workflow/versions/:version/restore`

把指定版本复制成新的草稿修订，不会修改旧版本，也不会自动发布。

```json
{"expected_revision": 12}
```

返回值为更新后的智能体草稿。`expected_revision` 不匹配时返回 `409`；恢复后必须再次通过严格校验并调用发布接口。

### POST `/agents/:id/workflow/import/preview`

服务端预检导入内容，不修改草稿。服务端会迁移 schema、脱敏敏感字段、检查资源引用，并返回 `issues`、`warnings`、`missing_resources`、`sensitive_fields` 和 `resource_mappings`。

```json
{"document": {"schema_version": 2, "nodes": [], "edges": [], "viewport": {}}}
```

预检通过后，前端才可由用户确认替换画布；导入结果仍是未发布草稿，定义中不携带凭据值。

### POST `/agents/:id/workflow/debug-runs`

基于指定 `expected_revision` 的当前草稿快照创建编辑器内 Debug Run。Debug Run 需要严格校验工作流，但不会推进 `published_version`，也不会改变正式聊天入口的执行版本。

**请求体**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `expected_revision` | integer | 是 | 当前编辑器看到的草稿修订号 |
| `input.query` | string | 否 | 试跑问题 |
| `input.attachments_text` | string | 否 | 已提取的附件文本 |
| `idempotency_key` | string | 否 | 幂等键，也可使用 `Idempotency-Key` 请求头 |

返回 `202` 和 `WorkflowRun`。重复幂等请求返回数据库中已有的同一 `run_id`，不会重复执行。

### GET `/agents/:id/workflow/runs/:run_id/stream`

以 SSE 续接持久化生命周期事件。可用 `after_sequence` 查询参数或 `Last-Event-ID` 请求头指定已收到的 sequence；服务端只返回更大的 sequence，并以 `id` 字段复用该 sequence。

```text
GET /agents/a/workflow/runs/r1/stream?after_sequence=7

id: 8
event: workflow_node.completed
data: {"sequence":8,"run_id":"r1","status":"succeeded"}
```

客户端应按 `run_id + sequence` 去重，断线后用最后一个 sequence 续接。运行进入终态且没有更多事件时，服务端发送 `event: end` 后关闭连接。

### POST `/agents/:id/workflow/runs/:run_id/cancel`

幂等请求取消运行。服务端先写入数据库 `cancel_requested_at`，再尽力删除排队唤醒任务；活动节点通过执行上下文响应取消，最终由状态机写入 `canceled`。

### POST `/agents/:id/workflow/runs/:run_id/retry`

使用原运行的定义快照、配置快照和输入创建新的运行。草稿或后续发布不会改变这次重跑使用的内容；返回 `202` 和新的 `WorkflowRun`。

### POST `/agents/:id/workflow/runs/:run_id/nodes/:node_run_id/retry`

从指定失败节点及其下游继续。服务端沿用该节点所属分支的成功上游变量检查点，创建新的运行和 attempt；仅允许重试终态为 `failed` 且符合权限范围的节点。

**修订冲突 409**：`error.details.current_revision` 为服务端当前修订号。前端应提示刷新编辑器，**不要**覆盖重试。

```json
{"success": false, "error": {
  "code": 1005,
  "message": "Workflow draft was modified by another update; refresh and retry",
  "details": {"current_revision": 15}
}}
```

### GET `/agents/:id/workflow/versions`

返回发布历史（版本号从新到旧）。已发布版本不可修改或覆盖。

```json
{"success": true, "data": [
  {"version": 3, "draft_revision": 12, "definition": {}, "config_snapshot": {},
   "published_by": "...", "published_at": "..."}
]}
```

### GET `/agents/:id/workflow/runs`

运行记录列表，游标分页。

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `limit` | integer | 每页条数，默认 20，上限 100；越界自动归一化 |
| `status` | string | 按运行状态筛选：`running` / `succeeded` / `partial` / `failed` / `canceled` |
| `started_after` | string | RFC3339 时间下界（含） |
| `started_before` | string | RFC3339 时间上界（不含） |
| `before_started_at` | string | 游标：上一页最后一条的 `started_at`（RFC3339） |
| `before_id` | string | 游标：上一页最后一条的 `id`。**必须与 `before_started_at` 成对出现** |

```json
{"success": true, "data": {
  "items": [{"id": "...", "started_at": "...", "status": "succeeded", "workflow_version": 3}],
  "has_more": true,
  "next_cursor": {"started_at": "...", "id": "..."}
}}
```

`has_more` 为 `false` 时 `next_cursor` 为 `null`。

### GET `/agents/:id/workflow/runs/:run_id`

运行详情。`definition_snapshot` 是**当次执行的不可变快照**——渲染历史必须使用它，而不是当前的草稿或当前发布定义。`nodes` 为按 `sequence` 升序的节点记录数组。

```json
{"success": true, "data": {
  "id": "...", "run_mode": "production", "requested_by": "user-1", "request_id": "req-1",
  "idempotency_key": "chat:message-1", "status": "partial", "workflow_version": 3,
  "trigger_source": "chat", "started_at": "...", "finished_at": "...", "duration_ms": 1234,
  "input_summary": "...", "output_summary": "...", "error_code": "", "usage": {},
  "definition_snapshot": {"schema_version": 2, "nodes": [], "edges": [], "viewport": {}},
  "nodes": [
    {"node_id": "llm-1", "node_name": "...", "node_type": "llm", "branch_path": "start/llm-1",
     "sequence": 2, "attempt": 1, "retry_of": 0, "task_id": "...", "retryable": false,
     "status": "succeeded", "duration_ms": 800, "output_summary": "..."}
  ]
}}
```

运行详情中的输入、输出、HTTP headers、工具参数和错误默认已经脱敏并按摘要上限截断；完整载荷只对所有者/Admin 可见。生命周期事件始终使用同一运行内单调递增的 `sequence`，并携带 `attempt`、`branch_path` 和结构化 `error_code`。

记录不存在或不属于当前租户时返回 `404`。

## 相关文档

- 工作流设计与语义：[工作流生产可用 V1](../workflow-production-v1.md)

- 智能体的组织共享、跨空间分发与禁用（`/agents/:id/shares`、`/shared-agents` 等）：见 [组织管理 API](./organization.md)
- 智能体绑定 IM 渠道（`/agents/:id/im-channels`）：见组织/IM 渠道相关文档
- 网络搜索提供者配置（被 `web_search_provider_id` 引用）：见 [Web Search API](./web-search.md)
