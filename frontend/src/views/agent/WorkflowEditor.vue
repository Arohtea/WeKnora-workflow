<template>
  <div class="workflow-editor">
    <div class="workflow-toolbar">
      <div class="workflow-toolbar-left">
        <div class="workflow-toolbar-title-wrap">
          <h3 class="workflow-title">流程编排</h3>
          <span class="workflow-stats-badge">{{ flowNodes.length }} 节点 · {{ flowEdges.length }} 连线</span>
        </div>
        <p class="workflow-subtitle">按照预定流程序列处理提问，结合知识库检索、模型推理与外部工具输出精准回答。</p>
      </div>
      <div class="workflow-toolbar-actions">
        <button
          type="button"
          class="workflow-toolbar-btn workflow-toolbar-btn--icon"
          title="撤回 (Ctrl+Z / ⌘Z)"
          :disabled="disabled || !canUndo"
          @click="undo"
        >
          <t-icon name="rollback" />
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn workflow-toolbar-btn--icon"
          title="重做 (Ctrl+Shift+Z / ⌘Shift+Z / Ctrl+Y)"
          :disabled="disabled || !canRedo"
          @click="redo"
        >
          <t-icon name="rollback" style="transform: scaleX(-1);" />
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn workflow-toolbar-btn--icon"
          title="适应画布视野"
          :disabled="disabled"
          @click="fitCanvas"
        >
          <t-icon name="fullscreen-1" />
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn"
          :class="{ 'is-active': showTemplateGallery }"
          :disabled="disabled"
          :title="showTemplateGallery ? '返回画布' : '用现成模板替换当前流程'"
          @click="toggleTemplateGallery"
        >
          <t-icon name="view-module" />
          <span>{{ showTemplateGallery ? '返回画布' : '模板库' }}</span>
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn"
          :class="{ 'is-active': showOnboardingGuide }"
          title="切换编排指南"
          @click="showOnboardingGuide = !showOnboardingGuide"
        >
          <t-icon name="help-circle" />
          <span>指南</span>
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn"
          :disabled="disabled"
          @click="validateDefinition"
        >
          <t-icon name="check-circle" />
          <span>校验</span>
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn workflow-toolbar-btn--primary"
          :disabled="disabled"
          @click="emit('run')"
        >
          <t-icon name="play-circle" />
          <span>保存并试用</span>
        </button>
      </div>
    </div>

    <transition name="fade">
      <div v-if="showOnboardingGuide" class="workflow-onboarding" aria-label="工作流编排步骤">
        <div class="workflow-onboarding-grid">
          <div v-for="(step, index) in onboardingSteps" :key="step.title" class="workflow-onboarding-step">
            <span class="workflow-onboarding-index">{{ index + 1 }}</span>
            <div class="workflow-onboarding-content">
              <strong>{{ step.title }}</strong>
              <small>{{ step.description }}</small>
            </div>
          </div>
        </div>
        <button type="button" class="workflow-onboarding-close" title="收起指南" @click="showOnboardingGuide = false">
          <t-icon name="close" size="14px" />
        </button>
      </div>
    </transition>

    <div v-if="showTemplateGallery" class="workflow-templates" data-guide="workflow-templates">
      <div class="workflow-templates-header">
        <div>
          <h4 class="workflow-templates-title">从模板开始</h4>
          <p class="workflow-templates-subtitle">
            模板是一张已经连好线的流程草图。选一个最接近你需求的，再把标了待配置的地方换成你自己的资源，比从空白画布开始快得多。
          </p>
        </div>
        <button type="button" class="workflow-link-button" @click="templateGalleryOpen = false">
          跳过，我自己画
        </button>
      </div>

      <div class="workflow-template-grid">
        <button
          v-for="template in templates"
          :key="template.id"
          type="button"
          class="workflow-template-card"
          :class="{ 'workflow-template-card--active': appliedTemplateId === template.id }"
          :disabled="disabled"
          @click="selectTemplate(template)"
        >
          <div class="workflow-template-card-head">
            <span class="workflow-template-icon"><t-icon :name="template.icon" /></span>
            <span class="workflow-template-heading">
              <strong>{{ template.name }}</strong>
              <small>{{ template.summary }}</small>
            </span>
          </div>

          <ol class="workflow-template-flow">
            <li v-for="step in template.steps" :key="step">{{ step }}</li>
          </ol>

          <p class="workflow-template-detail">{{ template.detail }}</p>

          <div class="workflow-template-needs">
            <span class="workflow-template-needs-label">
              <t-icon name="edit-1" />
              选中后还需补齐 {{ template.requirements.length }} 项
            </span>
            <span v-for="requirement in template.requirements" :key="requirement.nodeId" class="workflow-template-need">
              {{ requirement.label }}
            </span>
          </div>

          <span class="workflow-template-action">
            {{ appliedTemplateId === template.id ? '已应用，可在画布上继续调整' : '使用这个模板' }}
          </span>
        </button>
      </div>
    </div>

    <div v-else class="workflow-layout">
      <aside class="workflow-palette" aria-label="节点面板" data-guide="workflow-palette">
        <div class="workflow-panel-header">
          <div class="workflow-panel-heading">节点组件</div>
          <p class="workflow-panel-hint">点击或拖拽节点至右侧画布连线</p>
        </div>
        <div class="workflow-palette-list">
          <button
            v-for="item in nodePalette"
            :key="item.type"
            type="button"
            class="workflow-palette-item"
            :class="`workflow-palette-item--${item.type}`"
            :disabled="disabled || (item.type === 'start' && hasStartNode)"
            draggable="true"
            @click="addNode(item.type)"
            @dragstart="onPaletteDragStart($event, item.type)"
          >
            <div class="workflow-palette-icon">
              <t-icon :name="item.icon" />
            </div>
            <div class="workflow-palette-info">
              <strong>{{ item.label }}</strong>
              <small>{{ item.description }}</small>
            </div>
          </button>
        </div>

        <div class="workflow-legend">
          <span class="workflow-legend-item"><span class="workflow-legend-dot workflow-legend-dot--start" /> 入口节点</span>
          <span class="workflow-legend-item"><span class="workflow-legend-dot workflow-legend-dot--end" /> 回复节点</span>
        </div>
      </aside>

      <div
        class="workflow-canvas"
        :class="{ 'workflow-canvas--disabled': disabled }"
        data-guide="workflow-canvas"
        @dragover.prevent
        @drop="onCanvasDrop"
      >
        <VueFlow
          v-model:nodes="flowNodes"
          v-model:edges="flowEdges"
          :connection-mode="ConnectionMode.Loose"
          :connection-radius="30"
          :is-valid-connection="isValidConnection"
          :nodes-draggable="!disabled"
          :nodes-connectable="!disabled"
          :elements-selectable="true"
          :default-viewport="viewport"
          :fit-view-on-init="false"
          :default-edge-options="{ type: 'smoothstep', animated: false, markerEnd: MarkerType.ArrowClosed }"
          @connect="onConnect"
          @node-click="onNodeClick"
          @node-drag-stop="onNodeDragStop"
          @edge-click="onEdgeClick"
          @pane-click="clearSelection"
          @move-end="onMoveEnd"
        >
          <template #node-default="{ id, data, selected }">
            <div
              class="workflow-node-card"
              :class="[
                `workflow-node-card--${data.workflowType}`,
                { 'is-selected': selected }
              ]"
            >
              <!-- 上、右、下、左 四向连接桩 Handle -->
              <Handle
                id="top"
                :type="data.workflowType === 'end' ? 'target' : 'source'"
                :position="Position.Top"
                :connectable-start="!disabled && data.workflowType !== 'end'"
                :connectable-end="!disabled && data.workflowType !== 'start'"
                class="workflow-node-handle workflow-node-handle--top"
              />
              <Handle
                id="right"
                :type="data.workflowType === 'end' ? 'target' : 'source'"
                :position="Position.Right"
                :connectable-start="!disabled && data.workflowType !== 'end'"
                :connectable-end="!disabled && data.workflowType !== 'start'"
                class="workflow-node-handle workflow-node-handle--right"
              />
              <Handle
                id="bottom"
                :type="data.workflowType === 'end' ? 'target' : 'source'"
                :position="Position.Bottom"
                :connectable-start="!disabled && data.workflowType !== 'end'"
                :connectable-end="!disabled && data.workflowType !== 'start'"
                class="workflow-node-handle workflow-node-handle--bottom"
              />
              <Handle
                id="left"
                :type="data.workflowType === 'end' ? 'target' : 'source'"
                :position="Position.Left"
                :connectable-start="!disabled && data.workflowType !== 'end'"
                :connectable-end="!disabled && data.workflowType !== 'start'"
                class="workflow-node-handle workflow-node-handle--left"
              />
              <div class="workflow-node-stripe" />
              <div class="workflow-node-body">
                <div class="workflow-node-icon-badge">
                  <t-icon :name="nodeTypeIcon(data.workflowType)" />
                </div>
                <div class="workflow-node-text-wrap">
                  <div class="workflow-node-name" :title="data.name">{{ data.name }}</div>
                  <div class="workflow-node-snippet">{{ getNodeSnippet(data) }}</div>
                </div>
              </div>
            </div>
          </template>
          <Background pattern-color="#cbd5e1" :gap="20" />
          <Controls position="bottom-left" />
          <MiniMap position="bottom-right" />
        </VueFlow>
        <div v-if="isPlaceholderFlow" class="workflow-placeholder-note">
          <t-icon name="info-circle" />
          <span>“开始 → 结束”是可直接运行的占位流程；添加处理节点后，请调整连线。</span>
        </div>
        <div v-if="flowNodes.length === 0" class="workflow-canvas-empty">
          <t-icon name="share" size="28px" />
          <strong>画布为空</strong>
          <span>从左侧添加开始节点和处理节点。</span>
        </div>
      </div>

      <aside class="workflow-inspector" data-guide="workflow-inspector">
        <template v-if="selectedNode">
          <div class="workflow-inspector-header">
            <div class="workflow-inspector-header-left">
              <div class="workflow-node-type-pill" :class="`workflow-node-type-pill--${selectedNode.data.workflowType}`">
                <t-icon :name="nodeTypeIcon(selectedNode.data.workflowType)" size="14px" />
                <span>{{ nodeTypeLabel(selectedNode.data.workflowType) }}</span>
              </div>
              <h3 class="workflow-inspector-title">{{ selectedNode.data.name }}</h3>
              <div class="workflow-node-id-row">
                <button
                  type="button"
                  class="workflow-node-id-chip"
                  title="点击复制下游引用变量"
                  @click="copyNodeVariable(selectedNode.id)"
                >
                  <t-icon name="code" size="12px" />
                  <code>nodes.{{ selectedNode.id }}</code>
                  <t-icon name="copy" size="12px" class="workflow-copy-icon" />
                </button>
              </div>
            </div>
            <button
              v-if="!disabled && selectedNode.data.workflowType !== 'start'"
              type="button"
              class="workflow-toolbar-btn workflow-toolbar-btn--icon workflow-toolbar-btn--danger"
              title="删除此节点"
              @click="removeSelectedNode"
            >
              <t-icon name="delete" />
            </button>
          </div>

          <!-- 基础信息 -->
          <div class="workflow-inspector-section">
            <label class="workflow-field">
              <span class="workflow-field-label">节点显示名称</span>
              <input
                :value="selectedNode.data.name"
                :disabled="disabled"
                class="workflow-input"
                placeholder="给步骤起一个清晰的名称..."
                @input="updateNodeName(inputValue($event))"
              />
              <small class="workflow-field-help">用于在画布中直观区分步骤，便于理解与协作。</small>
            </label>
          </div>

          <!-- 知识库检索节点 -->
          <template v-if="selectedNode.data.workflowType === 'knowledge-retrieval'">
            <div class="workflow-inspector-section">
              <div class="workflow-section-title">检索配置</div>
              <div v-if="knowledgeBaseOptions.length === 0" class="workflow-resource-empty">
                <t-icon name="folder" size="20px" />
                <strong>暂无可用知识库</strong>
                <span>当前空间没有可选知识库，请先创建或获取知识库权限。</span>
                <button type="button" class="workflow-link-button" @click="emit('manage-knowledge-bases')">
                  配置知识库
                </button>
              </div>
              <div v-else class="workflow-field">
                <span class="workflow-field-label">关联知识库 <em class="workflow-required">*</em></span>
                <t-select
                  :value="retrievalKnowledgeBaseIDs"
                  multiple
                  filterable
                  placeholder="选择要检索的知识库..."
                  :disabled="disabled"
                  :min-collapsed-num="3"
                  @change="onKnowledgeBasesChange"
                >
                  <t-option
                    v-for="kb in knowledgeBaseOptions"
                    :key="kb.value"
                    :value="kb.value"
                    :label="kb.label"
                  />
                </t-select>
                <small class="workflow-field-help">支持多选，模型将综合检索命中内容作为上下文依据。</small>
                <div v-if="missingRetrievalKnowledgeBaseIDs.length" class="workflow-field-alert workflow-field-alert--error">
                  <t-icon name="error-circle" />
                  <span>以下知识库已不可用：{{ missingRetrievalKnowledgeBaseIDs.join(', ') }}</span>
                </div>
              </div>

              <div class="workflow-field">
                <span class="workflow-field-label">查询模板 <em class="workflow-required">*</em></span>
                <textarea
                  :value="configString('query_template')"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="3"
                  placeholder="{{input.query}}"
                  @input="updateConfig('query_template', inputValue($event))"
                />
                <small class="workflow-field-help">默认使用提问变量 <code>&#123;&#123;input.query&#125;&#125;</code>，也可拼接上下文。</small>
              </div>

              <div class="workflow-field workflow-field--inline">
                <span class="workflow-field-label">召回数量 (Top-K) <em class="workflow-required">*</em></span>
                <input
                  type="number"
                  min="1"
                  max="50"
                  class="workflow-input"
                  style="width: 110px;"
                  :value="configNumber('top_k', 5)"
                  :disabled="disabled"
                  @input="updateConfig('top_k', numberValue($event, 5))"
                />
              </div>
            </div>
          </template>

          <!-- 大模型处理（LLM）节点 -->
          <template v-else-if="selectedNode.data.workflowType === 'llm'">
            <div class="workflow-inspector-section">
              <div class="workflow-section-title">模型推理设置</div>
              <div class="workflow-field">
                <span class="workflow-field-label">系统提示词 (System Prompt)</span>
                <textarea
                  :value="configString('system_prompt')"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="3"
                  placeholder="设定模型的角色与行为准则，例如：你是一个严谨高效的文本处理专家..."
                  @input="updateConfig('system_prompt', inputValue($event))"
                />
                <small class="workflow-field-help">可选，用于定义大模型的全局角色、专业视角与输出规范。</small>
              </div>

              <div class="workflow-field">
                <span class="workflow-field-label">用户提示词 (User Prompt) <em class="workflow-required">*</em></span>
                <textarea
                  :value="configString('prompt')"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="6"
                  placeholder="请根据 {{input.query}} 进行改写或总结..."
                  @input="updateConfig('prompt', inputValue($event))"
                />
                <small class="workflow-field-help">
                  支持使用 <code>&#123;&#123;input.query&#125;&#125;</code> 或 <code>&#123;&#123;nodes.节点ID.xxx&#125;&#125;</code>；生成文本可通过 <code>nodes.{{ selectedNode.id }}.text</code> 供下游引用。
                </small>
              </div>

              <div class="workflow-field-grid">
                <div class="workflow-field">
                  <span class="workflow-field-label">采样温度 (0-2)</span>
                  <input
                    type="number"
                    step="0.1"
                    min="0"
                    max="2"
                    class="workflow-input"
                    :value="configNumber('temperature', 0.7)"
                    :disabled="disabled"
                    @input="updateConfig('temperature', numberValue($event, 0.7))"
                  />
                  <small class="workflow-field-help">数值越低越确定，越高越具发散性。</small>
                </div>
                <div class="workflow-field">
                  <span class="workflow-field-label">最大 Token</span>
                  <input
                    type="number"
                    min="1"
                    class="workflow-input"
                    placeholder="默认不限"
                    :value="configNullableNumber('max_tokens')"
                    :disabled="disabled"
                    @input="updateConfig('max_tokens', nullableNumberValue($event))"
                  />
                  <small class="workflow-field-help">留空表示使用模型默认限制。</small>
                </div>
              </div>
            </div>
          </template>

          <!-- 大模型决策节点 -->
          <template v-else-if="selectedNode.data.workflowType === 'llm-decision'">
            <div class="workflow-inspector-section">
              <div class="workflow-section-title">分支判断设置</div>
              <div class="workflow-field">
                <span class="workflow-field-label">判断提示词 <em class="workflow-required">*</em></span>
                <textarea
                  :value="configString('prompt')"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="6"
                  placeholder="请根据 {{input.query}} 判断意图并返回以下候选之一..."
                  @input="updateConfig('prompt', inputValue($event))"
                />
              </div>
              <div class="workflow-field">
                <span class="workflow-field-label">候选分支标签 <em class="workflow-required">*</em></span>
                <textarea
                  :value="decisionChoicesText"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="4"
                  placeholder="通过&#10;拒绝"
                  @input="updateDecisionChoices(inputValue($event))"
                />
                <small class="workflow-field-help">每行一个候选分支标签；决策输出可通过 <code>nodes.{{ selectedNode.id }}.data.choice</code> 配合连线条件实现分支路由。</small>
              </div>
            </div>
          </template>

          <!-- HTTP 请求节点 -->
          <template v-else-if="selectedNode.data.workflowType === 'http-request'">
            <div class="workflow-inspector-section">
              <div class="workflow-section-title">HTTP 接口请求</div>
              <div class="workflow-field-grid">
                <div class="workflow-field" style="max-width: 120px;">
                  <span class="workflow-field-label">请求方法 <em class="workflow-required">*</em></span>
                  <t-select
                    :value="configString('method', 'GET')"
                    :disabled="disabled"
                    @change="updateConfig('method', String($event || 'GET'))"
                  >
                    <t-option v-for="method in httpMethods" :key="method" :value="method" :label="method" />
                  </t-select>
                </div>
                <div class="workflow-field" style="flex: 1;">
                  <span class="workflow-field-label">接口 URL <em class="workflow-required">*</em></span>
                  <input
                    :value="configString('url')"
                    :disabled="disabled"
                    class="workflow-input"
                    placeholder="https://api.example.com/endpoint"
                    @input="updateConfig('url', inputValue($event))"
                  />
                </div>
              </div>
              <small class="workflow-field-help" style="margin-top: -6px; margin-bottom: 12px; display: block;">{{ urlFieldHint }}</small>

              <div class="workflow-field">
                <span class="workflow-field-label">请求头 JSON (Headers)</span>
                <textarea
                  :value="httpHeadersText"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="4"
                  placeholder='{"Content-Type":"application/json"}'
                  @input="updateHttpHeaders(inputValue($event))"
                />
                <div v-if="jsonFieldErrors.headers" class="workflow-field-alert workflow-field-alert--error">
                  <t-icon name="error-circle" />
                  <span>{{ jsonFieldErrors.headers }}</span>
                </div>
                <div v-else class="workflow-field-alert workflow-field-alert--info">
                  <t-icon name="info-circle" />
                  <span>出于安全考虑，不可直接配置 Authorization/Cookie 头。若需鉴权调用，请使用 MCP 服务接入。</span>
                </div>
              </div>

              <div class="workflow-field">
                <span class="workflow-field-label">请求体模板 (Body)</span>
                <textarea
                  :value="configString('body_template')"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="4"
                  placeholder='{"query":"{{input.query}}"}'
                  @input="updateConfig('body_template', inputValue($event))"
                />
              </div>
            </div>
          </template>

          <!-- 工具调用节点 -->
          <template v-else-if="selectedNode.data.workflowType === 'tool'">
            <div class="workflow-inspector-section">
              <div class="workflow-section-title">外部工具接入</div>
              <div class="workflow-field">
                <span class="workflow-field-label">工具类型 <em class="workflow-required">*</em></span>
                <t-radio-group
                  :value="configString('kind', 'builtin')"
                  :disabled="disabled"
                  variant="default-filled"
                  size="small"
                  @change="changeToolKind(String($event))"
                >
                  <t-radio-button value="builtin">内置工具</t-radio-button>
                  <t-radio-button value="mcp">MCP 服务</t-radio-button>
                  <t-radio-button value="skill">沙箱技能</t-radio-button>
                </t-radio-group>
              </div>

              <div v-if="toolKind === 'builtin' && builtinTools.length === 0" class="workflow-resource-empty">
                <t-icon name="tools" size="20px" />
                <strong>暂无可用内置工具</strong>
                <span>当前环境未发现可用于工作流的内置工具。</span>
              </div>
              <div v-else-if="toolKind === 'builtin'" class="workflow-field">
                <span class="workflow-field-label">选择内置工具 <em class="workflow-required">*</em></span>
                <t-select
                  :value="configString('tool_name')"
                  :disabled="disabled"
                  placeholder="请选择内置工具..."
                  filterable
                  @change="updateConfig('tool_name', String($event || ''))"
                >
                  <t-option
                    v-for="tool in builtinTools"
                    :key="tool.name"
                    :value="tool.name"
                    :label="tool.display_name || tool.name"
                  />
                </t-select>
                <small v-if="selectedBuiltinTool?.description" class="workflow-field-help">{{ selectedBuiltinTool.description }}</small>
              </div>

              <template v-else-if="toolKind === 'mcp'">
                <div v-if="mcpServices.length === 0" class="workflow-resource-empty">
                  <t-icon name="server" size="20px" />
                  <strong>暂无可用 MCP 服务</strong>
                  <span>当前空间没有已启用的 MCP 服务，请先配置连接。</span>
                  <button type="button" class="workflow-link-button" @click="emit('manage-mcp')">
                    管理 MCP
                  </button>
                </div>
                <template v-else>
                  <div class="workflow-field">
                    <span class="workflow-field-label">MCP 服务 <em class="workflow-required">*</em></span>
                    <t-select
                      :value="configString('service_id')"
                      :disabled="disabled"
                      placeholder="请选择 MCP 服务..."
                      filterable
                      @change="changeMCPService(String($event || ''))"
                    >
                      <t-option
                        v-for="service in mcpServices"
                        :key="service.id"
                        :value="service.id"
                        :label="service.name"
                      />
                    </t-select>
                  </div>
                  <div v-if="selectedMCPService" class="workflow-field">
                    <span class="workflow-field-label">MCP 工具 <em class="workflow-required">*</em></span>
                    <t-select
                      :value="configString('tool_name')"
                      :disabled="disabled"
                      placeholder="请选择 MCP 工具..."
                      filterable
                      @change="updateConfig('tool_name', String($event || ''))"
                    >
                      <t-option
                        v-for="tool in selectedMCPService?.tools || []"
                        :key="tool.name"
                        :value="tool.name"
                        :label="tool.display_name || tool.name"
                      />
                    </t-select>
                    <small v-if="selectedMCPTool?.description" class="workflow-field-help">{{ selectedMCPTool.description }}</small>
                    <div v-else-if="selectedMCPService.tools.length === 0" class="workflow-field-alert workflow-field-alert--error">
                      <t-icon name="error-circle" />
                      <span>该 MCP 服务当前暂无可调用的工具接口。</span>
                    </div>
                  </div>
                </template>
              </template>

              <template v-else>
                <div class="workflow-field">
                  <span class="workflow-field-label">沙箱技能 (Skill) <em class="workflow-required">*</em></span>
                  <t-select
                    :value="configString('skill_name')"
                    :disabled="disabled || !sandboxConfigId || skills.length === 0"
                    placeholder="请选择沙箱技能..."
                    filterable
                    @change="updateConfig('skill_name', String($event || ''))"
                  >
                    <t-option
                      v-for="skill in skills"
                      :key="skill.name"
                      :value="skill.name"
                      :label="skillOptionLabel(skill)"
                    />
                  </t-select>
                  <small v-if="selectedSkill?.description" class="workflow-field-help">{{ selectedSkill.description }}</small>
                </div>

                <div v-if="!sandboxConfigId" class="workflow-resource-empty">
                  <t-icon name="server" size="20px" />
                  <strong>未关联运行沙箱</strong>
                  <span>Skill 依赖沙箱执行环境，请先关联沙箱。</span>
                  <button type="button" class="workflow-link-button" @click="emit('select-sandbox')">
                    关联沙箱
                  </button>
                </div>
                <div v-else-if="skills.length === 0" class="workflow-resource-empty">
                  <t-icon name="tools" size="20px" />
                  <strong>当前沙箱未安装可用技能</strong>
                  <span>请先安装并等待技能就绪。</span>
                  <button type="button" class="workflow-link-button" @click="emit('manage-skills')">
                    管理技能
                  </button>
                </div>
              </template>

              <div v-if="toolKind !== 'skill'" class="workflow-field">
                <span class="workflow-field-label">入参 JSON (Arguments)</span>
                <textarea
                  :value="toolArgumentsText"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="6"
                  :placeholder="toolArgumentPlaceholder"
                  @input="updateToolArguments(inputValue($event))"
                />
                <div v-if="jsonFieldErrors.arguments" class="workflow-field-alert workflow-field-alert--error">
                  <t-icon name="error-circle" />
                  <span>{{ jsonFieldErrors.arguments }}</span>
                </div>
                <div v-else-if="toolArgumentFields.length" class="workflow-field-help">
                  参数规范：<code v-for="field in toolArgumentFields" :key="field.name" class="workflow-code-tag">{{ field.label }}</code>
                </div>
                <small v-else class="workflow-field-help">{{ toolArgumentHint }}</small>
              </div>
              <div v-else class="workflow-field">
                <span class="workflow-field-label">任务执行模板 <em class="workflow-required">*</em></span>
                <textarea
                  :value="configString('task_template')"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="6"
                  placeholder="请根据 {{input.query}} 完成具体任务并返回可交付成果。"
                  @input="updateConfig('task_template', inputValue($event))"
                />
              </div>
            </div>
          </template>

          <!-- 最终输出节点 -->
          <template v-else-if="selectedNode.data.workflowType === 'end'">
            <div class="workflow-inspector-section">
              <div class="workflow-section-title">最终回复编排</div>
              <div class="workflow-field">
                <span class="workflow-field-label">回复输出模板</span>
                <textarea
                  :value="configString('text_template')"
                  :disabled="disabled"
                  class="workflow-textarea"
                  rows="6"
                  :placeholder="endTemplatePlaceholder"
                  @input="updateConfig('text_template', inputValue($event))"
                />
                <small class="workflow-field-help">用户最终收到的完整文本内容；留空则默认转发上一节点的输出结果。</small>
              </div>
              <div v-if="upstreamNodeOptions.length" class="workflow-variable-picker">
                <span class="workflow-variable-picker-title">快捷插入上游节点变量：</span>
                <div class="workflow-variable-chips">
                  <button
                    v-for="option in upstreamNodeOptions"
                    :key="option.value"
                    type="button"
                    class="workflow-variable-chip"
                    :disabled="disabled"
                    :title="option.value"
                    @click="appendTemplateVariable('text_template', option.value)"
                  >
                    + {{ option.label }}
                  </button>
                </div>
              </div>
            </div>
          </template>

          <!-- 开始节点说明 -->
          <div v-if="selectedNode.data.workflowType === 'start'" class="workflow-inspector-section">
            <div class="workflow-field-alert workflow-field-alert--info">
              <t-icon name="info-circle" />
              <span>流程入口节点。下游节点可通过 <code>input.query</code> 获取用户提问，通过 <code>input.attachments_text</code> 读取附件提取文本。</span>
            </div>
          </div>

          <!-- 输出与变量参考 -->
          <div v-if="selectedNode.data.workflowType !== 'start'" class="workflow-inspector-section workflow-inspector-section--help">
            <div class="workflow-section-title">输出与变量参考</div>
            <div class="workflow-help-grid">
              <div class="workflow-help-item">
                <span class="workflow-help-label">下游引用变量：</span>
                <code>{{ selectedNodeHelp.variables }}</code>
              </div>
              <div class="workflow-help-item">
                <span class="workflow-help-label">输出属性说明：</span>
                <span>{{ selectedNodeHelp.output }}</span>
              </div>
            </div>
          </div>
        </template>

        <!-- 连线属性检视器 -->
        <template v-else-if="selectedEdge">
          <div class="workflow-inspector-header">
            <div class="workflow-inspector-header-left">
              <div class="workflow-node-type-pill workflow-node-type-pill--edge">
                <t-icon name="fork" size="14px" />
                <span>分支连线</span>
              </div>
              <h3 class="workflow-inspector-title">{{ selectedEdge.source }} → {{ selectedEdge.target }}</h3>
            </div>
            <button
              v-if="!disabled"
              type="button"
              class="workflow-toolbar-btn workflow-toolbar-btn--icon workflow-toolbar-btn--danger"
              title="删除连线"
              @click="removeSelectedEdge"
            >
              <t-icon name="delete" />
            </button>
          </div>

          <div class="workflow-inspector-section">
            <div class="workflow-switch-card">
              <div class="workflow-switch-meta">
                <strong>默认后备分支</strong>
                <small>当其他所有分支条件都不满足时，将沿此路径继续执行（同一源节点最多 1 条默认分支）。</small>
              </div>
              <t-switch
                :value="Boolean(selectedEdge.data?.is_default)"
                :disabled="disabled"
                @change="updateEdgeDefault(Boolean($event))"
              />
            </div>

            <template v-if="!selectedEdge.data?.is_default">
              <div class="workflow-field" style="margin-top: 14px;">
                <span class="workflow-field-label">多条件满足规则</span>
                <t-radio-group
                  :value="edgeConditionMode"
                  :disabled="disabled"
                  variant="default-filled"
                  size="small"
                  @change="updateEdgeConditionMode(String($event))"
                >
                  <t-radio-button value="all">满足全部条件 (AND)</t-radio-button>
                  <t-radio-button value="any">满足任一条件 (OR)</t-radio-button>
                </t-radio-group>
              </div>

              <div class="workflow-condition-list">
                <div v-for="(item, index) in edgeConditionItems" :key="index" class="workflow-condition-card">
                  <div class="workflow-condition-card-header">
                    <span class="workflow-condition-index-badge">规则 {{ index + 1 }}</span>
                    <button
                      v-if="!disabled"
                      type="button"
                      class="workflow-icon-btn-subtle"
                      title="删除此规则"
                      @click="removeConditionItem(index)"
                    >
                      <t-icon name="close" size="14px" />
                    </button>
                  </div>
                  <div class="workflow-condition-row-grid">
                    <t-select
                      :value="item.variable"
                      :disabled="disabled"
                      size="small"
                      placeholder="判断变量"
                      @change="updateConditionItem(index, 'variable', String($event))"
                    >
                      <t-option v-for="variable in variableOptions" :key="variable" :value="variable" :label="variable" />
                    </t-select>
                    <t-select
                      :value="item.operator"
                      :disabled="disabled"
                      size="small"
                      placeholder="操作符"
                      @change="updateConditionItem(index, 'operator', String($event))"
                    >
                      <t-option v-for="operator in conditionOperators" :key="operator.value" :value="operator.value" :label="operator.label" />
                    </t-select>
                  </div>
                  <input
                    :value="conditionValue(item.value)"
                    :disabled="disabled"
                    class="workflow-input workflow-input--small"
                    placeholder="目标比较值..."
                    @input="updateConditionItem(index, 'value', inputValue($event))"
                  />
                </div>
              </div>

              <button
                v-if="!disabled"
                type="button"
                class="workflow-toolbar-btn workflow-toolbar-btn--add-condition"
                @click="addConditionItem"
              >
                <t-icon name="add" />
                <span>添加判断条件</span>
              </button>
            </template>
          </div>
        </template>

        <!-- 空选中态 -->
        <div v-else class="workflow-inspector-empty">
          <div class="workflow-empty-icon-ring">
            <t-icon name="cursor" size="24px" />
          </div>
          <strong>选中节点或连线以配置</strong>
          <p>在画布上点击任意节点以修改入参、知识库或模型提示词；点击连线可设置分支路由条件。</p>
        </div>
      </aside>
    </div>

    <div v-if="validationMessage" class="workflow-validation" :class="`workflow-validation--${validationStatus}`" :role="validationStatus === 'error' ? 'alert' : 'status'">
      <t-icon :name="validationIcon" />
      <span>{{ validationMessage }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Background } from '@vue-flow/background';
import { Controls } from '@vue-flow/controls';
import { MiniMap } from '@vue-flow/minimap';
import { ConnectionMode, Handle, MarkerType, Position, VueFlow, useVueFlow, type Connection } from '@vue-flow/core';
import { MessagePlugin } from 'tdesign-vue-next';
import { copyToClipboard } from '@/utils/clipboard';
import type {
  WorkflowCatalog,
  WorkflowCatalogService,
  WorkflowCatalogSkill,
  WorkflowCatalogTool,
  WorkflowCondition,
  WorkflowConditionItem,
  WorkflowDefinition,
  WorkflowEdge,
  WorkflowNode,
  WorkflowNodeType,
} from '@/api/agent';
import '@vue-flow/core/dist/style.css';
import '@vue-flow/core/dist/theme-default.css';
import {
  WORKFLOW_TEMPLATES,
  isUntouchedDefinition,
  type WorkflowTemplate,
} from './workflowTemplates';

interface KnowledgeBaseOption {
  label: string;
  value: string;
}

interface WorkflowNodeData {
  workflowType: WorkflowNodeType;
  name: string;
  config: Record<string, any>;
}

interface WorkflowEdgeData {
  order: number;
  is_default: boolean;
  condition?: WorkflowCondition;
}

type EditorNode = any;
type EditorEdge = any;

const props = withDefaults(defineProps<{
  modelValue?: WorkflowDefinition | null;
  catalog?: WorkflowCatalog | null;
  knowledgeBaseOptions?: KnowledgeBaseOption[];
  sandboxConfigId?: string;
  disabled?: boolean;
}>(), {
  modelValue: null,
  catalog: null,
  knowledgeBaseOptions: () => [],
  sandboxConfigId: '',
  disabled: false,
});

const emit = defineEmits<{
  (event: 'update:modelValue', value: WorkflowDefinition): void;
  (event: 'validation-error', message: string): void;
  (event: 'select-sandbox'): void;
  (event: 'manage-skills'): void;
  (event: 'manage-knowledge-bases'): void;
  (event: 'manage-mcp'): void;
  (event: 'run'): void;
}>();

const onboardingSteps = [
  { title: '选一个模板', description: '最接近你需求的流程，会自动连线。' },
  { title: '补齐待配置项', description: '按右侧提示换成自己的知识库或接口。' },
  { title: '改节点文字', description: '把名称改成同事看得懂的说法。' },
  { title: '校验并去试用', description: '校验通过后保存，会自动打开对话让你问一句。' },
];

const nodePalette: Array<{ type: WorkflowNodeType; label: string; description: string; icon: string }> = [
  { type: 'start', label: '开始', description: '流程入口：接收用户的提问和附件', icon: 'play-circle' },
  { type: 'llm', label: '大模型处理', description: '输入提示词让大模型改写、总结或处理文本', icon: 'chat' },
  { type: 'knowledge-retrieval', label: '查知识库', description: '在指定知识库里找相关内容', icon: 'search' },
  { type: 'llm-decision', label: '让模型判断', description: '在几个候选结论里选一个', icon: 'control-platform' },
  { type: 'http-request', label: '调用接口', description: '把内容发给外部系统（如工单、审批）', icon: 'link' },
  { type: 'tool', label: '用工具', description: '联网搜索、查图谱，或运行一个技能', icon: 'tools' },
  { type: 'end', label: '输出回答', description: '整理成给用户看的最终回复', icon: 'check-circle' },
];

const httpMethods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];
const conditionOperatorValues = [
  'eq', 'neq', 'contains', 'not_contains', 'starts_with', 'ends_with',
  'gt', 'gte', 'lt', 'lte', 'in', 'not_in', 'is_empty', 'is_not_empty',
] as const;
const conditionOperatorLabels: Record<(typeof conditionOperatorValues)[number], string> = {
  eq: '等于',
  neq: '不等于',
  contains: '包含',
  not_contains: '不包含',
  starts_with: '开始于',
  ends_with: '结束于',
  gt: '大于',
  gte: '大于等于',
  lt: '小于',
  lte: '小于等于',
  in: '属于',
  not_in: '不属于',
  is_empty: '为空',
  is_not_empty: '不为空',
};
const conditionOperators = conditionOperatorValues.map((value) => ({ value, label: conditionOperatorLabels[value] }));

const flowNodes = ref<EditorNode[]>([]);
const flowEdges = ref<EditorEdge[]>([]);
const selectedNodeId = ref('');
const selectedEdgeId = ref('');
const validationMessage = ref('');
const validationStatus = ref<'idle' | 'hint' | 'error' | 'success'>('idle');
/** 临时提示的自动消失定时器。 */
let hintTimer: ReturnType<typeof setTimeout> | null = null;
const viewport = ref({ x: 0, y: 0, zoom: 1 });
const applyingModel = ref(false);

/** 撤回/重做历史快照 */
const undoStack = ref<string[]>([]);
const redoStack = ref<string[]>([]);
const isHistoryApplying = ref(false);
const MAX_HISTORY = 50;

const canUndo = computed(() => undoStack.value.length > 0 && !props.disabled);
const canRedo = computed(() => redoStack.value.length > 0 && !props.disabled);
/**
 * JSON 文本框的原始输入。非法 JSON 也要能显示在输入框里，
 * 因此原始文本与已解析的 config 分开保存；只有解析成功才写回 config。
 */
const httpHeadersRaw = ref<string | null>(null);
const toolArgumentsRaw = ref<string | null>(null);
/** 字段名 → JSON 语法错误，用于输入框下方就地提示。 */
const jsonFieldErrors = ref<Record<string, string>>({});
/** 用户手动开关模板面板；null 表示跟随"画布是否还是空白默认流程"自动判断。 */
const templateGalleryOpen = ref<boolean | null>(null);
/** 已应用的模板，用于在卡片上回显"已应用"。 */
const appliedTemplateId = ref('');
let lastEmitted = '';

const { fitView } = useVueFlow();

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));

const templates = WORKFLOW_TEMPLATES;

/** 画布仍是"开始 → 输出回答"空白流程时为 true。 */
const isUntouchedFlow = computed(() => isUntouchedDefinition(toDefinition()));

/**
 * 空白流程默认打开模板面板，让用户的第一眼就是"可以选一个现成的"，
 * 而不是一张需要自己从零连线的画布。
 */
const showTemplateGallery = computed(() =>
  templateGalleryOpen.value === null ? isUntouchedFlow.value : templateGalleryOpen.value,
);

function defaultConfig(type: WorkflowNodeType): Record<string, any> {
  switch (type) {
    case 'knowledge-retrieval':
      return { knowledge_base_ids: [], query_template: '{{input.query}}', top_k: 5 };
    case 'llm':
      return { prompt: '{{input.query}}', system_prompt: '你是一个严谨高效的文本处理助手。', temperature: 0.7 };
    case 'llm-decision':
      return { prompt: '{{input.query}}', choices: ['通过', '拒绝'] };
    case 'http-request':
      return { method: 'GET', url: '', headers: {}, body_template: '' };
    case 'tool':
      return { kind: 'builtin', tool_name: '', arguments: {} };
    case 'end':
      return { text_template: '{{input.query}}' };
    default:
      return {};
  }
}

function defaultDefinition(): WorkflowDefinition {
  return {
    version: 1,
    nodes: [
      { id: 'start', type: 'start', name: '开始', position: { x: 80, y: 160 }, config: {} },
      { id: 'end', type: 'end', name: '结束', position: { x: 420, y: 160 }, config: { text_template: '{{input.query}}' } },
    ],
    edges: [{ id: 'start-end', source: 'start', target: 'end', order: 0, is_default: false }],
    viewport: { x: 0, y: 0, zoom: 1 },
  };
}

function normalizeDefinition(value?: WorkflowDefinition | null): WorkflowDefinition {
  const definition = value && value.nodes?.length ? clone(value) : defaultDefinition();
  definition.version ||= 1;
  definition.nodes = (definition.nodes || []).map((node) => ({
    ...node,
    name: node.name || node.id,
    position: { x: Number(node.position?.x) || 0, y: Number(node.position?.y) || 0 },
    config: node.config && typeof node.config === 'object' ? node.config : defaultConfig(node.type),
  }));
  definition.edges = (definition.edges || []).map((edge, index) => ({
    ...edge,
    id: edge.id || `edge-${index + 1}`,
    order: Number.isFinite(edge.order) ? edge.order : index,
    is_default: !!edge.is_default,
  }));
  definition.viewport = {
    x: Number(definition.viewport?.x) || 0,
    y: Number(definition.viewport?.y) || 0,
    zoom: Number(definition.viewport?.zoom) > 0 ? Number(definition.viewport.zoom) : 1,
  };
  return definition;
}

function nodeClass(type: WorkflowNodeType): string {
  return `workflow-flow-node workflow-flow-node--${type}`;
}

function loadDefinition(value?: WorkflowDefinition | null) {
  const definition = normalizeDefinition(value);
  applyingModel.value = true;
  viewport.value = clone(definition.viewport);
  flowNodes.value = definition.nodes.map((node): EditorNode => ({
    id: node.id,
    type: 'default',
    label: node.name,
    position: { x: node.position.x, y: node.position.y },
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    class: nodeClass(node.type),
    data: { workflowType: node.type, name: node.name, config: clone(node.config || defaultConfig(node.type)) },
    draggable: !props.disabled,
    deletable: !props.disabled,
  }));
  flowEdges.value = definition.edges.map((edge): EditorEdge => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    sourceHandle: edge.source_handle || 'right',
    targetHandle: edge.target_handle || 'left',
    type: 'smoothstep',
    markerEnd: MarkerType.ArrowClosed,
    label: edge.is_default ? '默认' : '',
    data: {
      order: edge.order,
      is_default: !!edge.is_default,
      condition: edge.condition ? clone(edge.condition) : undefined,
    },
    deletable: !props.disabled,
  }));
  selectedNodeId.value = '';
  selectedEdgeId.value = '';
  undoStack.value = [];
  redoStack.value = [];
  applyingModel.value = false;
}

function toDefinition(): WorkflowDefinition {
  return {
    version: 1,
    nodes: flowNodes.value.map((node): WorkflowNode => ({
      id: node.id,
      type: node.data.workflowType,
      name: node.data.name,
      position: { x: Number(node.position.x) || 0, y: Number(node.position.y) || 0 },
      config: clone(node.data.config || {}),
    })),
    edges: flowEdges.value.map((edge): WorkflowEdge => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      source_handle: edge.sourceHandle || undefined,
      target_handle: edge.targetHandle || undefined,
      order: edge.data?.order ?? 0,
      is_default: !!edge.data?.is_default,
      ...(edge.data?.condition ? { condition: clone(edge.data.condition) } : {}),
    })),
    viewport: clone(viewport.value),
  };
}

function emitDefinition() {
  if (applyingModel.value) return;
  // 临时提示由定时器自行消失，不因为一次数据变更就被清掉。
  if (validationStatus.value === 'error' || validationStatus.value === 'success') {
    validationStatus.value = 'idle';
    validationMessage.value = '';
  }
  const next = toDefinition();
  lastEmitted = JSON.stringify(next);
  emit('update:modelValue', next);
  if (!isHistoryApplying.value) {
    pushSnapshotDebounced();
  }
}

/** 捕获当前画布的序列化快照 */
function captureCurrentSnapshot(): string {
  return JSON.stringify({
    nodes: flowNodes.value.map((node) => ({
      id: node.id,
      type: node.data.workflowType,
      name: node.data.name,
      position: { x: Number(node.position.x) || 0, y: Number(node.position.y) || 0 },
      config: clone(node.data.config || {}),
    })),
    edges: flowEdges.value.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      source_handle: edge.sourceHandle || undefined,
      target_handle: edge.targetHandle || undefined,
      order: edge.data?.order ?? 0,
      is_default: !!edge.data?.is_default,
      condition: edge.data?.condition ? clone(edge.data.condition) : undefined,
    })),
  });
}

/** 记录一个立即可撤回的快照 */
function pushSnapshot() {
  if (isHistoryApplying.value || applyingModel.value || props.disabled) return;
  const current = captureCurrentSnapshot();
  const last = undoStack.value[undoStack.value.length - 1];
  if (last === current) return;

  undoStack.value.push(current);
  if (undoStack.value.length > MAX_HISTORY) {
    undoStack.value.shift();
  }
  // 用户产生新操作时清空重做栈
  redoStack.value = [];
}

let snapshotDebounceTimer: ReturnType<typeof setTimeout> | null = null;
function pushSnapshotDebounced(delay = 500) {
  if (isHistoryApplying.value || applyingModel.value || props.disabled) return;
  if (snapshotDebounceTimer) {
    clearTimeout(snapshotDebounceTimer);
  }
  snapshotDebounceTimer = setTimeout(() => {
    pushSnapshot();
  }, delay);
}

/** 从历史快照精准恢复节点与连线 */
function restoreSnapshot(snapshotStr: string) {
  try {
    const snapshot = JSON.parse(snapshotStr);
    isHistoryApplying.value = true;
    flowNodes.value = (snapshot.nodes || []).map((node: any): EditorNode => ({
      id: node.id,
      type: 'default',
      label: node.name,
      position: { x: node.position.x, y: node.position.y },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      class: nodeClass(node.type),
      data: { workflowType: node.type, name: node.name, config: clone(node.config || defaultConfig(node.type)) },
      draggable: !props.disabled,
      deletable: !props.disabled,
    }));
    flowEdges.value = (snapshot.edges || []).map((edge: any): EditorEdge => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      sourceHandle: edge.source_handle || 'right',
      targetHandle: edge.target_handle || 'left',
      type: 'smoothstep',
      markerEnd: MarkerType.ArrowClosed,
      label: edge.is_default ? '默认' : '',
      data: {
        order: edge.order,
        is_default: !!edge.is_default,
        condition: edge.condition ? clone(edge.condition) : undefined,
      },
      deletable: !props.disabled,
    }));
    clearSelection();
  } finally {
    nextTick(() => {
      isHistoryApplying.value = false;
      emitDefinition();
    });
  }
}

/** 执行撤回 */
function undo() {
  if (!canUndo.value || props.disabled) return;
  const current = captureCurrentSnapshot();
  const previous = undoStack.value.pop();
  if (previous) {
    redoStack.value.push(current);
    restoreSnapshot(previous);
    showHint('已撤回上一操作');
  }
}

/** 执行重做 */
function redo() {
  if (!canRedo.value || props.disabled) return;
  const current = captureCurrentSnapshot();
  const next = redoStack.value.pop();
  if (next) {
    undoStack.value.push(current);
    restoreSnapshot(next);
    showHint('已重做操作');
  }
}

/** 节点拖拽移动停止时记录快照 */
function onNodeDragStop() {
  pushSnapshot();
  emitDefinition();
}

/** 监听全局撤回与重做快捷键 */
function handleKeyDown(event: KeyboardEvent) {
  if (props.disabled) return;
  // 文本框打字中不劫持输入框原生撤回
  const target = event.target as HTMLElement | null;
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
    return;
  }

  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform);
  const isModifier = isMac ? event.metaKey : event.ctrlKey;
  if (!isModifier) return;

  const key = event.key.toLowerCase();
  // 重做: Cmd/Ctrl + Shift + Z 或者 Cmd/Ctrl + Y
  if ((key === 'z' && event.shiftKey) || (key === 'y' && !event.shiftKey)) {
    if (canRedo.value) {
      event.preventDefault();
      redo();
    }
    return;
  }

  // 撤回: Cmd/Ctrl + Z (无 Shift)
  if (key === 'z' && !event.shiftKey) {
    if (canUndo.value) {
      event.preventDefault();
      undo();
    }
    return;
  }
}

onMounted(() => {
  window.addEventListener('keydown', handleKeyDown);
});

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeyDown);
  if (snapshotDebounceTimer) {
    clearTimeout(snapshotDebounceTimer);
  }
});

watch(
  () => props.modelValue,
  (value) => {
    const incoming = JSON.stringify(normalizeDefinition(value));
    if (incoming === lastEmitted) return;
    loadDefinition(value);
  },
  { deep: true, immediate: true },
);

watch([flowNodes, flowEdges], emitDefinition, { deep: true });

const selectedNode = computed(() => flowNodes.value.find((node) => node.id === selectedNodeId.value));
const selectedEdge = computed(() => flowEdges.value.find((edge) => edge.id === selectedEdgeId.value));
const hasStartNode = computed(() => flowNodes.value.some((node) => node.data.workflowType === 'start'));
const knowledgeBaseOptions = computed(() => props.knowledgeBaseOptions || []);
const catalog = computed(() => props.catalog || { builtin_tools: [], mcp_services: [], skills: [] });
const builtinTools = computed(() => catalog.value.builtin_tools || []);
const mcpServices = computed(() => catalog.value.mcp_services || []);
const skills = computed(() => catalog.value.skills || []);

const selectedNodeConfig = computed<Record<string, any>>(() => selectedNode.value?.data.config || {});
const configString = (key: string, fallback = '') => {
  const value = selectedNodeConfig.value[key];
  return value == null ? fallback : String(value);
};
const configNumber = (key: string, fallback: number) => {
  const value = Number(selectedNodeConfig.value[key]);
  return Number.isFinite(value) ? value : fallback;
};
const configNullableNumber = (key: string): number | string => {
  const value = selectedNodeConfig.value[key];
  if (value === undefined || value === null || value === '') return '';
  const num = Number(value);
  return Number.isFinite(num) ? num : '';
};
const toolKind = computed(() => configString('kind', 'builtin'));
const retrievalKnowledgeBaseIDs = computed(() => {
  const value = selectedNodeConfig.value.knowledge_base_ids;
  return Array.isArray(value) ? value.map(String) : [];
});
const decisionChoicesText = computed(() => {
  const choices = selectedNodeConfig.value.choices;
  return Array.isArray(choices) ? choices.join('\n') : '';
});
// 有未解析成功的原始输入时优先回显它，否则回显已保存的配置，
// 这样用户不会在打字过程中看到内容被悄悄回滚。
const httpHeadersText = computed(
  () => httpHeadersRaw.value ?? JSON.stringify(selectedNodeConfig.value.headers || {}, null, 2),
);
const toolArgumentsText = computed(
  () => toolArgumentsRaw.value ?? JSON.stringify(selectedNodeConfig.value.arguments || {}, null, 2),
);
const selectedBuiltinTool = computed<WorkflowCatalogTool | undefined>(() =>
  builtinTools.value.find((tool) => tool.name === configString('tool_name')),
);
const selectedMCPService = computed<WorkflowCatalogService | undefined>(() =>
  mcpServices.value.find((service) => service.id === configString('service_id')),
);
const selectedMCPTool = computed<WorkflowCatalogTool | undefined>(() =>
  selectedMCPService.value?.tools.find((tool) => tool.name === configString('tool_name')),
);
const selectedSkill = computed<WorkflowCatalogSkill | undefined>(() =>
  skills.value.find((skill) => skill.name === configString('skill_name')),
);
const missingRetrievalKnowledgeBaseIDs = computed(() => {
  const available = new Set(knowledgeBaseOptions.value.map((item) => item.value));
  return retrievalKnowledgeBaseIDs.value.filter((id) => !available.has(id));
});

/** 当前工具节点选中的工具，用于把它的入参 schema 展示成可读提示。 */
const selectedTool = computed<WorkflowCatalogTool | undefined>(() =>
  toolKind.value === 'mcp' ? selectedMCPTool.value : selectedBuiltinTool.value,
);

/** 工具入参 schema 的字段清单：参数 JSON 一直是空白文本域，用户无从得知要写什么。 */
const toolArgumentFields = computed(() => {
  const schema = selectedTool.value?.parameters;
  if (!schema || Array.isArray(schema)) return [];
  const properties = (schema as Record<string, any>).properties;
  if (!properties || typeof properties !== 'object') return [];
  const required: string[] = Array.isArray((schema as Record<string, any>).required)
    ? (schema as Record<string, any>).required
    : [];
  return Object.entries(properties as Record<string, any>).map(([name, definition]) => {
    const type = Array.isArray(definition?.type) ? definition.type[0] : definition?.type;
    const suffix = required.includes(name) ? '必填' : '选填';
    return { name, label: `${name}（${type || '任意'}·${suffix}）` };
  });
});

/**
 * 依据工具 schema 生成一份可直接用的参数示例。
 *
 * 字符串参数默认填用户问题，数组参数包一层，避免用户面对 `{}` 反复试错。
 */
const toolArgumentPlaceholder = computed(() => {
  const schema = selectedTool.value?.parameters;
  const properties = (!schema || Array.isArray(schema))
    ? undefined
    : (schema as Record<string, any>).properties;
  if (!properties || typeof properties !== 'object') return '{}';
  const example: Record<string, unknown> = {};
  for (const [name, definition] of Object.entries(properties as Record<string, any>)) {
    const type = Array.isArray(definition?.type) ? definition.type[0] : definition?.type;
    if (type === 'array') {
      example[name] = ['{{input.query}}'];
    } else if (type === 'number' || type === 'integer') {
      example[name] = 5;
    } else if (type === 'boolean') {
      example[name] = true;
    } else {
      example[name] = '{{input.query}}';
    }
  }
  return JSON.stringify(example, null, 2);
});
const isPlaceholderFlow = computed(() => {
  if (flowNodes.value.length !== 2 || flowEdges.value.length !== 1) return false;
  const start = flowNodes.value.find((node) => node.data.workflowType === 'start');
  const end = flowNodes.value.find((node) => node.data.workflowType === 'end');
  const edge = flowEdges.value[0];
  return !!start && !!end && edge.source === start.id && edge.target === end.id;
});

const selectedNodeHelp = computed(() => {
  const type = selectedNode.value?.data.workflowType as WorkflowNodeType | undefined;
  const keyByType: Record<WorkflowNodeType, string> = {
    start: 'start',
    llm: 'llm',
    'knowledge-retrieval': 'retrieval',
    'llm-decision': 'decision',
    'http-request': 'http',
    tool: 'tool',
    end: 'end',
  };
  const key = type ? keyByType[type] : 'start';
  const help: Record<string, { required: string; variables: string; output: string }> = {
    start: {
      required: '无需配置',
      variables: 'input.query / input.attachments_text',
      output: '把用户输入传给后续节点。',
    },
    llm: {
      required: '提示词',
      variables: '{{input.query}}',
      output: 'text 是大模型生成的完整文本，供下游节点或输出节点引用。',
    },
    retrieval: {
      required: '知识库、查询模板、召回数量',
      variables: '{{input.query}}',
      output: 'text 是检索到的内容，会作为回答依据；data.count 是命中条数。',
    },
    decision: {
      required: '判断提示词、至少两个候选标签',
      variables: '{{input.query}}',
      output: 'data.choice 是模型选中的那个标签，可用来决定走哪个分支。',
    },
    http: {
      required: '请求方法、HTTP/HTTPS URL',
      variables: '{{input.query}}',
      output: 'data.status_code、data.body 和 data.json 是接口返回的内容。',
    },
    tool: {
      required: '工具类型及对应资源；Skill 还需任务模板',
      variables: '{{input.query}}',
      output: 'text 是工具返回的结果，可直接作为后续步骤的输入。',
    },
    end: {
      required: '最终输出模板可留空',
      variables: '{{input.query}}',
      output: '作为工作流的最终回复返回给用户。',
    },
  };
  return help[key];
});

function skillOptionLabel(skill: WorkflowCatalogSkill): string {
  const versionedName = skill.version ? `${skill.name} · v${skill.version}` : skill.name;
  return skill.description ? `${versionedName} — ${skill.description}` : versionedName;
}

const variableOptions = computed(() => {
  const values = ['input.query', 'input.attachments_text'];
  for (const node of flowNodes.value) {
    if (node.data.workflowType === 'start') continue;
    values.push(`nodes.${node.id}.text`, `nodes.${node.id}.status`);
    if (node.data.workflowType === 'llm') values.push(`nodes.${node.id}.data.reasoning_content`);
    if (node.data.workflowType === 'llm-decision') values.push(`nodes.${node.id}.data.choice`);
    if (node.data.workflowType === 'knowledge-retrieval') values.push(`nodes.${node.id}.data.count`);
    if (node.data.workflowType === 'http-request') {
      values.push(`nodes.${node.id}.data.status_code`, `nodes.${node.id}.data.body`, `nodes.${node.id}.data.json`);
    }
  }
  return values;
});

/**
 * 沿入边向上回溯，得到某个节点的全部上游节点。
 *
 * 后端只允许当前分支上游的变量参与渲染，所以这里必须按路径真实回溯，
 * 否则用户从下拉里选到的变量会在运行时被判定为不可用。
 *
 * @param nodeId 起始节点 ID。
 * @returns 从近到远排列的上游节点。
 */
function upstreamNodesFor(nodeId: string): EditorNode[] {
  const incomingByTarget = new Map<string, string>();
  for (const edge of flowEdges.value) {
    // 每个节点最多一条入边，因此无需处理汇聚场景。
    if (!incomingByTarget.has(edge.target)) incomingByTarget.set(edge.target, edge.source);
  }
  const result: EditorNode[] = [];
  const seen = new Set<string>([nodeId]);
  let current = incomingByTarget.get(nodeId);
  while (current && !seen.has(current)) {
    seen.add(current);
    const node = flowNodes.value.find((item) => item.id === current);
    if (!node) break;
    if (node.data.workflowType !== 'start') result.push(node);
    current = incomingByTarget.get(current);
  }
  return result;
}

/** 结束节点可引用的上游输出，用节点名称而不是 ID 展示。 */
const upstreamNodeOptions = computed(() => {
  if (selectedNode.value?.data.workflowType !== 'end') return [];
  return upstreamNodesFor(selectedNode.value.id).map((node) => ({
    label: node.data.name || node.id,
    value: `{{nodes.${node.id}.text}}`,
  }));
});

/** 结束节点默认取紧邻的上一步输出，而不是让用户面对空占位符。 */
const endTemplatePlaceholder = computed(() => upstreamNodeOptions.value[0]?.value || '{{input.query}}');

/**
 * 字段旁的说明文案里需要出现 {{...}} 字面量，而模板属性里的双大括号会被
 * Vue 编译器当成插值表达式，因此在脚本里拼好再渲染。
 */
const urlFieldHint = '必须带 http:// 或 https://；变量只能写在路径或参数里，例如 https://example.com/api/{{nodes.retrieval-1.text}}';
const toolArgumentHint = '按工具的入参写一个 JSON 对象；用 {{input.query}} 可以引用用户的问题。';

/**
 * 把变量占位符追加到某个文本模板字段末尾。
 *
 * @param key 节点配置字段名。
 * @param variable 形如 {{nodes.x.text}} 的占位符。
 */
function appendTemplateVariable(key: string, variable: string) {
  if (props.disabled) return;
  const current = configString(key);
  updateConfig(key, current ? `${current}\n${variable}` : variable);
}

/**
 * 复制节点在模板里被引用时使用的变量前缀。
 *
 * 条件变量和输出模板都要求写 nodes.<节点 ID>，而节点 ID 是自动生成的，
 * 所以这里把它显式呈现出来，避免用户去猜。
 *
 * @param nodeId 节点 ID。
 */
async function copyNodeVariable(nodeId: string) {
  const variable = `nodes.${nodeId}`;
  const ok = await copyToClipboard(variable);
  if (ok) MessagePlugin.success(`已复制 ${variable}`);
}

const edgeCondition = computed<WorkflowCondition>(() => selectedEdge.value?.data?.condition || {
  mode: 'all',
  items: [{ variable: variableOptions.value[0] || 'input.query', operator: 'eq', value: '' }],
});
const edgeConditionMode = computed(() => edgeCondition.value.mode);
const edgeConditionItems = computed(() => edgeCondition.value.items || []);

function inputValue(event: Event): string {
  return (event.target as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement).value;
}

// 切换节点时丢弃上一条原始输入与报错，避免把 A 节点的文本带到 B 节点。
watch(selectedNodeId, () => {
  httpHeadersRaw.value = null;
  toolArgumentsRaw.value = null;
  jsonFieldErrors.value = {};
});

function selectedValues(event: Event): string[] {
  return Array.from((event.target as HTMLSelectElement).selectedOptions).map((option) => option.value);
}

function numberValue(event: Event, fallback: number): number {
  const value = Number(inputValue(event));
  return Number.isFinite(value) ? value : fallback;
}

function nullableNumberValue(event: Event): number | undefined {
  const raw = inputValue(event).trim();
  if (!raw) return undefined;
  const val = Number(raw);
  return Number.isFinite(val) ? val : undefined;
}

function updateSelectedNode(mutator: (node: EditorNode) => void) {
  if (props.disabled || !selectedNode.value) return;
  const node = selectedNode.value;
  mutator(node);
  node.label = node.data.name;
  node.class = nodeClass(node.data.workflowType);
  emitDefinition();
}

function updateNodeName(name: string) {
  updateSelectedNode((node) => { node.data.name = name.trim() || node.id; });
}

function updateConfig(key: string, value: unknown) {
  updateSelectedNode((node) => { node.data.config[key] = value; });
}

function updateRetrievalKnowledgeBases(ids: string[]) {
  updateConfig('knowledge_base_ids', ids);
}

function updateDecisionChoices(value: string) {
  updateConfig('choices', value.split('\n').map((item) => item.trim()).filter(Boolean));
}

/**
 * 解析 JSON 文本框的内容。
 *
 * 非法 JSON 必须原样回显给用户，否则下一次 setConfig 会把输入框重置回旧值，
 * 用户会遇到"打字打不进去"且毫无提示的情况。
 *
 * @param value 用户输入的原始文本。
 * @param fallback 空输入时使用的字面量。
 * @returns 解析结果与错误原因，二者只会有一个存在。
 */
function parseJSONField(value: string, fallback: string): { value?: Record<string, unknown>; error?: string } {
  const trimmed = value.trim();
  if (!trimmed) {
    return { value: {} };
  }
  try {
    const parsed = JSON.parse(trimmed === '' ? fallback : trimmed);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return { error: '需要是一个 JSON 对象，例如 {"key":"value"}' };
    }
    return { value: parsed as Record<string, unknown> };
  } catch (error) {
    return { error: `JSON 格式有误：${(error as Error).message}` };
  }
}

function applyJSONField(key: string, raw: string, fallback: string): boolean {
  const { value, error } = parseJSONField(raw, fallback);
  if (error) {
    jsonFieldErrors.value = { ...jsonFieldErrors.value, [key]: error };
    return false;
  }
  const next = { ...jsonFieldErrors.value };
  delete next[key];
  jsonFieldErrors.value = next;
  updateConfig(key, value);
  return true;
}

function updateHttpHeaders(value: string) {
  httpHeadersRaw.value = value;
  applyJSONField('headers', value, '{}');
}

function updateToolArguments(value: string) {
  toolArgumentsRaw.value = value;
  applyJSONField('arguments', value, '{}');
}

/** 列出仍然存在 JSON 语法错误的字段，供校验与保存前拦截。 */
function pendingJSONErrors(): string[] {
  return Object.keys(jsonFieldErrors.value);
}

function changeToolKind(kind: string) {
  updateSelectedNode((node) => {
    node.data.config = {
      ...defaultConfig('tool'),
      ...node.data.config,
      kind,
      ...(kind === 'builtin' ? { tool_name: '' } : {}),
      ...(kind === 'mcp' ? { service_id: '', tool_name: '' } : {}),
      ...(kind === 'skill' ? { skill_name: '', task_template: '' } : {}),
    };
  });
}

function changeMCPService(serviceID: string) {
  updateSelectedNode((node) => {
    node.data.config.service_id = serviceID;
    node.data.config.tool_name = '';
  });
}

function updateSelectedEdge(mutator: (edge: EditorEdge) => void) {
  if (props.disabled || !selectedEdge.value) return;
  const edge = selectedEdge.value;
  mutator(edge);
  edge.label = edge.data?.is_default ? '默认' : '';
  emitDefinition();
}

function updateEdgeDefault(value: boolean) {
  updateSelectedEdge((edge) => {
    edge.data = { ...(edge.data || { order: 0 }), is_default: value };
    if (value) edge.data.condition = undefined;
  });
}

function updateEdgeConditionMode(mode: string) {
  updateSelectedEdge((edge) => {
    edge.data = {
      ...(edge.data || { order: 0, is_default: false }),
      condition: { mode: mode === 'any' ? 'any' : 'all', items: clone(edge.data?.condition?.items || [{ variable: variableOptions.value[0] || 'input.query', operator: 'eq', value: '' }]) },
    };
  });
}

function updateConditionItem(index: number, key: keyof WorkflowConditionItem, value: unknown) {
  updateSelectedEdge((edge) => {
    const condition = edge.data?.condition || { mode: 'all', items: [] };
    const items = [...condition.items];
    const nextValue = key === 'value' && typeof value === 'string' ? parseConditionValue(value) : value;
    items[index] = { ...items[index], [key]: nextValue };
    edge.data = { ...(edge.data || { order: 0, is_default: false }), condition: { ...condition, items } };
  });
}

function addConditionItem() {
  updateSelectedEdge((edge) => {
    const condition = edge.data?.condition || { mode: 'all', items: [] };
    edge.data = {
      ...(edge.data || { order: 0, is_default: false }),
      condition: {
        ...condition,
        items: [...condition.items, { variable: variableOptions.value[0] || 'input.query', operator: 'eq', value: '' }],
      },
    };
  });
}

function removeConditionItem(index: number) {
  updateSelectedEdge((edge) => {
    const condition = edge.data?.condition;
    if (!condition) return;
    const items = condition.items.filter((_item: WorkflowConditionItem, itemIndex: number) => itemIndex !== index);
    edge.data = { ...(edge.data || { order: 0, is_default: false }), condition: items.length ? { ...condition, items } : undefined };
  });
}

function conditionValue(value: unknown): string {
  if (value == null) return '';
  return typeof value === 'string' ? value : JSON.stringify(value);
}

function parseConditionValue(value: string): unknown {
  const trimmed = value.trim();
  if (!trimmed) return '';
  try {
    return JSON.parse(trimmed);
  } catch {
    return value;
  }
}

const showOnboardingGuide = ref(false);

function nodeTypeLabel(type: WorkflowNodeType): string {
  return nodePalette.find((item) => item.type === type)?.label || type;
}

function nodeTypeIcon(type: WorkflowNodeType): string {
  return nodePalette.find((item) => item.type === type)?.icon || 'view-module';
}

function getNodeSnippet(data: WorkflowNodeData): string {
  if (!data?.config) return '';
  switch (data.workflowType) {
    case 'start':
      return '接收提问与附件输入';
    case 'llm': {
      const prompt = (data.config.prompt as string) || '';
      return prompt ? (prompt.length > 20 ? prompt.slice(0, 20) + '...' : prompt) : '待设置模型提示词';
    }
    case 'knowledge-retrieval': {
      const kbIds = (data.config.knowledge_base_ids as string[]) || [];
      return kbIds.length ? `已选 ${kbIds.length} 个知识库` : '未选择知识库';
    }
    case 'llm-decision': {
      const choices = (data.config.choices as string[]) || [];
      return choices.length ? `${choices.length} 个候选分支` : '待设置候选分支';
    }
    case 'http-request': {
      const method = data.config.method || 'GET';
      const url = data.config.url || '';
      return `${method} ${url ? (url.length > 16 ? url.slice(0, 16) + '...' : url) : '未配置接口'}`;
    }
    case 'tool': {
      const kind = data.config.kind || 'builtin';
      if (kind === 'builtin') return `内置: ${data.config.tool_name || '未选'}`;
      if (kind === 'mcp') return `MCP: ${data.config.tool_name || '未选'}`;
      return `Skill: ${data.config.skill_name || '未选'}`;
    }
    case 'end':
      return data.config.text_template ? '自定义回复输出' : '输出最终回答';
    default:
      return '';
  }
}

function onKnowledgeBasesChange(val: unknown) {
  const ids = Array.isArray(val) ? val.map(String) : [];
  updateRetrievalKnowledgeBases(ids);
}

/** 临时提示（黄色），用于解释"为什么刚才那步没生效"。 */
function showHint(message: string) {
  if (hintTimer) clearTimeout(hintTimer);
  validationStatus.value = 'hint';
  validationMessage.value = message;
  hintTimer = setTimeout(() => {
    if (validationStatus.value !== 'hint') return;
    validationStatus.value = 'idle';
    validationMessage.value = '';
  }, 4000);
}

function toggleTemplateGallery() {
  templateGalleryOpen.value = !showTemplateGallery.value;
}

/**
 * 应用模板并切换到画布。
 *
 * 模板会整张替换当前流程，因此覆盖已有内容前先让用户确认一次；
 * 应用后自动定位到第一个"待配置"节点，把用户直接送到需要动手的地方。
 *
 * @param template 用户选中的模板。
 */
function selectTemplate(template: WorkflowTemplate) {
  if (props.disabled) return;
  if (!isUntouchedFlow.value && appliedTemplateId.value !== template.id) {
    const confirmed = window.confirm(
      `“${template.name}”会替换画布上现有的节点和连线。已配置的内容不会保留，确定继续吗？`,
    );
    if (!confirmed) return;
  }

  pushSnapshot();
  const definition = template.create();
  loadDefinition(definition);
  appliedTemplateId.value = template.id;
  templateGalleryOpen.value = false;

  // 直接选中第一个还需要用户补齐资源的节点，省掉"该点哪里"的摸索。
  const pending = template.requirements[0]?.nodeId;
  if (pending && flowNodes.value.some((node) => node.id === pending)) {
    selectedNodeId.value = pending;
    selectedEdgeId.value = '';
  }

  emitDefinition();
  nextTick(() => fitCanvas());
}

function newNodeID(type: WorkflowNodeType): string {
  const base = type === 'knowledge-retrieval' ? 'retrieval' : type === 'llm-decision' ? 'decision' : type === 'http-request' ? 'http' : type;
  let index = 1;
  while (flowNodes.value.some((node) => node.id === `${base}-${index}`)) index += 1;
  return `${base}-${index}`;
}

function nextNodePosition(): { x: number; y: number } {
  const index = flowNodes.value.length;
  return { x: 100 + (index % 4) * 260, y: 80 + Math.floor(index / 4) * 160 };
}

function addNode(type: WorkflowNodeType, position?: { x: number; y: number }) {
  if (props.disabled) return;
  if (type === 'start' && hasStartNode.value) {
    showHint('每个工作流只能有一个开始节点，画布上已经有一个了。');
    return;
  }
  pushSnapshot();
  const id = newNodeID(type);
  const name = nodeTypeLabel(type);
  flowNodes.value.push({
    id,
    type: 'default',
    label: name,
    position: position || nextNodePosition(),
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    class: nodeClass(type),
    data: { workflowType: type, name, config: defaultConfig(type) },
    draggable: true,
    deletable: true,
  });
  selectedNodeId.value = id;
  selectedEdgeId.value = '';
  emitDefinition();
}

function onPaletteDragStart(event: DragEvent, type: WorkflowNodeType) {
  event.dataTransfer?.setData('application/x-workflow-node', type);
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'copy';
}

function onCanvasDrop(event: DragEvent) {
  const type = event.dataTransfer?.getData('application/x-workflow-node') as WorkflowNodeType;
  if (!type) return;
  addNode(type, nextNodePosition());
}

/**
 * 校验节点间连线的合法性
 * 1. 阻止自连
 * 2. 阻止连入开始节点
 * 3. 阻止从结束节点连出
 * 4. 正在拖拽中的新连接：避免重复连接相同桩位
 * 注：VueFlow 内部解析已有 edge 时也会调用本函数，已有边必定带 id，此时绝不能误当成重复边丢弃！
 */
function isValidConnection(connection: Connection | EditorEdge): boolean {
  if (!connection.source || !connection.target) return false;
  if (connection.source === connection.target) return false;

  const targetNode = flowNodes.value.find((n) => n.id === connection.target);
  if (targetNode?.data?.workflowType === 'start') return false;

  const sourceNode = flowNodes.value.find((n) => n.id === connection.source);
  if (sourceNode?.data?.workflowType === 'end') return false;

  // VueFlow 内部在解析已有的 edge 时带有 id 属性，满足拓扑规则即可通过
  const edgeId = (connection as EditorEdge).id;
  if (edgeId) {
    return true;
  }

  // 正在交互拖拽的新连线：阻止相同方位重复吸附
  const sHandle = connection.sourceHandle || '';
  const tHandle = connection.targetHandle || '';
  const isDuplicate = flowEdges.value.some(
    (edge) =>
      edge.source === connection.source &&
      edge.target === connection.target &&
      (edge.sourceHandle || '') === sHandle &&
      (edge.targetHandle || '') === tHandle,
  );
  if (isDuplicate) return false;

  return true;
}

function onConnect(connection: Connection) {
  if (props.disabled || !connection.source || !connection.target) return;
  if (!isValidConnection(connection)) {
    if (connection.source === connection.target) {
      showHint('一个步骤不能连到自己，请连到后面的步骤。');
    } else {
      const sourceNode = flowNodes.value.find((n) => n.id === connection.source);
      const targetNode = flowNodes.value.find((n) => n.id === connection.target);
      if (targetNode?.data?.workflowType === 'start') {
        showHint('开始节点不能作为连入目标。');
      } else if (sourceNode?.data?.workflowType === 'end') {
        showHint('输出回答节点不能向外连出。');
      } else {
        showHint('该方位已经连好了，不用再连一次。');
      }
    }
    return;
  }
  pushSnapshot();
  const sHandle = connection.sourceHandle || undefined;
  const tHandle = connection.targetHandle || undefined;
  const order = flowEdges.value.filter((edge) => edge.source === connection.source).length;
  const edge: EditorEdge = {
    id: `edge-${Date.now()}-${order}`,
    source: connection.source,
    target: connection.target,
    sourceHandle: sHandle,
    targetHandle: tHandle,
    type: 'smoothstep',
    markerEnd: MarkerType.ArrowClosed,
    data: { order, is_default: order === 0 },
    label: order === 0 ? '默认' : '',
    deletable: true,
  };
  flowEdges.value.push(edge);
  selectedNodeId.value = '';
  selectedEdgeId.value = edge.id;
  emitDefinition();
}

function onNodeClick(event: any) {
  const id = event?.node?.id || event?.id;
  if (!id) return;
  selectedNodeId.value = id;
  selectedEdgeId.value = '';
}

function onEdgeClick(event: any) {
  const id = event?.edge?.id || event?.id;
  if (!id) return;
  selectedNodeId.value = '';
  selectedEdgeId.value = id;
}

function clearSelection() {
  selectedNodeId.value = '';
  selectedEdgeId.value = '';
}

function removeSelectedNode() {
  if (props.disabled || !selectedNode.value || selectedNode.value.data.workflowType === 'start') return;
  pushSnapshot();
  const id = selectedNode.value.id;
  flowNodes.value = flowNodes.value.filter((node) => node.id !== id);
  flowEdges.value = flowEdges.value.filter((edge) => edge.source !== id && edge.target !== id);
  clearSelection();
  emitDefinition();
}

function removeSelectedEdge() {
  if (props.disabled || !selectedEdge.value) return;
  pushSnapshot();
  flowEdges.value = flowEdges.value.filter((edge) => edge.id !== selectedEdge.value?.id);
  clearSelection();
  emitDefinition();
}

function fitCanvas() {
  void fitView({ padding: 0.2 });
}

function onMoveEnd(event: any) {
  const transform = event?.flowTransform;
  if (!transform || applyingModel.value) return;
  viewport.value = {
    x: Number(transform.x) || 0,
    y: Number(transform.y) || 0,
    zoom: Number(transform.zoom) > 0 ? Number(transform.zoom) : 1,
  };
  emitDefinition();
}

type ValidationIssue = {
  message: string;
  nodeId?: string;
  edgeId?: string;
};

const workflowIDPattern = /^[A-Za-z0-9_-]{1,80}$/;
const inputVariablePattern = /^input\.(query|attachments_text)$/;
const nodeVariablePattern = /^nodes\.([A-Za-z0-9_-]{1,80})\.(text|status|data(?:\.[A-Za-z0-9_-]+)*)$/;
const conditionOperatorSet = new Set<string>(conditionOperatorValues);

function failValidation(issue: ValidationIssue): false {
  validationStatus.value = 'error';
  validationMessage.value = issue.message;
  if (issue.edgeId && flowEdges.value.some((edge) => edge.id === issue.edgeId)) {
    selectedNodeId.value = '';
    selectedEdgeId.value = issue.edgeId;
  } else if (issue.nodeId && flowNodes.value.some((node) => node.id === issue.nodeId)) {
    selectedNodeId.value = issue.nodeId;
    selectedEdgeId.value = '';
  }
  emit('validation-error', issue.message);
  return false;
}

function passValidation(): true {
  validationStatus.value = 'success';
  validationMessage.value = '校验通过，可以保存并去试用了。';
  emit('validation-error', '');
  return true;
}

const validationIcon = computed(() => {
  if (validationStatus.value === 'error') return 'error-circle';
  if (validationStatus.value === 'hint') return 'info-circle';
  return 'check-circle';
});

function isValidHTTPTemplateURL(raw: string): boolean {
  const value = raw.trim();
  if (!/^https?:\/\//i.test(value)) return false;
  const authority = value.match(/^https?:\/\/([^/?#]*)/i)?.[1] || '';
  if (authority.includes('{{') || authority.includes('}}')) return false;
  try {
    const parsed = new URL(value.replace(/\{\{[^{}]+\}\}/g, 'value'));
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && !!parsed.hostname;
  } catch {
    return false;
  }
}

/**
 * 找出模板里引用了不存在节点的变量。
 *
 * 前后端正则都只校验变量写法，不校验节点是否存在，所以写错的变量会一路保存成功，
 * 直到运行时才报 "unavailable on this branch"。这里提前把问题挡在保存之前。
 *
 * @param text 待检查的模板文本。
 * @param knownNodeIDs 当前画布上所有节点 ID。
 * @returns 不存在节点对应的变量清单。
 */
function unknownTemplateNodes(text: string, knownNodeIDs: Set<string>): string[] {
  const missing = new Set<string>();
  for (const match of text.matchAll(/\{\{\s*nodes\.([A-Za-z0-9_-]+)\./g)) {
    if (!knownNodeIDs.has(match[1])) missing.add(`nodes.${match[1]}`);
  }
  return [...missing];
}

/** 后端禁止在 HTTP 请求头里携带认证信息，这里提前给出可执行的提示。 */
const blockedHeaderNames = new Set([
  'authorization', 'cookie', 'proxy-authorization', 'x-api-key', 'api-key',
  'x-auth-token', 'x-access-token', 'access-token',
]);

function findBlockedHeader(headers: Record<string, unknown>): string {
  return Object.keys(headers).find((name) => blockedHeaderNames.has(name.trim().toLowerCase())) || '';
}

function validateNodeConfiguration(
  node: WorkflowNode,
  availableKnowledgeBases: Set<string>,
  knownNodeIDs: Set<string> = new Set<string>(),
): ValidationIssue | null {
  const config = node.config || {};

  /**
   * 模板占位符检查：写错的变量在保存阶段完全合法，只会在运行时失败，
   * 所以这里对所有会渲染模板的字段统一做一次存在性校验。
   */
  const checkTemplates = (fields: Array<[string, string]>): ValidationIssue | null => {
    for (const [label, text] of fields) {
      if (!text) continue;
      const unknown = unknownTemplateNodes(text, knownNodeIDs);
      if (unknown.length) {
        return {
          message: `节点“${node.name}”的${label}引用了不存在的步骤：${unknown.join('、')}。请改成画布上真实存在的节点，或点击字段下方的变量按钮插入。`,
          nodeId: node.id,
        };
      }
      for (const match of text.matchAll(/\{\{\s*nodes\.([A-Za-z0-9_-]+)\./g)) {
        if (match[1] === node.id) {
          return {
            message: `节点“${node.name}”的${label}不能引用节点自身。当前步骤的输出在执行时尚未产生。`,
            nodeId: node.id,
          };
        }
      }
    }
    return null;
  };

  if (!workflowIDPattern.test(node.id)) {
    return { message: `节点 ID“${node.id}”格式无效。`, nodeId: node.id };
  }
  if (!node.name.trim()) {
    return { message: `节点“${node.id}”需要填写名称。`, nodeId: node.id };
  }
  if (node.type === 'knowledge-retrieval') {
    const ids = Array.isArray(config.knowledge_base_ids) ? config.knowledge_base_ids.map(String) : [];
    if (ids.length === 0) {
      return { message: `节点“${node.name}”至少需要选择一个知识库。`, nodeId: node.id };
    }
    const missing = ids.filter((id) => !availableKnowledgeBases.has(id));
    if (missing.length > 0) {
      return {
        message: `节点“${node.name}”引用了不可用的知识库：${missing.join(', ')}。`,
        nodeId: node.id,
      };
    }
    if (!String(config.query_template || '').trim()) {
      return { message: `节点“${node.name}”需要填写查询模板。`, nodeId: node.id };
    }
    const topK = Number(config.top_k);
    if (!Number.isInteger(topK) || topK < 1 || topK > 50) {
      return { message: `节点“${node.name}”的召回数量必须是 1 到 50 的整数。`, nodeId: node.id };
    }
    return checkTemplates([['查询模板', String(config.query_template || '')]]);
  } else if (node.type === 'llm') {
    if (!String(config.prompt || '').trim()) {
      return { message: `节点“${node.name}”需要填写用户提示词。`, nodeId: node.id };
    }
    const temp = config.temperature !== undefined && config.temperature !== null ? Number(config.temperature) : 0.7;
    if (Number.isNaN(temp) || temp < 0 || temp > 2) {
      return { message: `节点“${node.name}”的温度必须在 0 到 2 之间。`, nodeId: node.id };
    }
    if (config.max_tokens !== undefined && config.max_tokens !== null && config.max_tokens !== '') {
      const maxTokens = Number(config.max_tokens);
      if (!Number.isInteger(maxTokens) || maxTokens <= 0) {
        return { message: `节点“${node.name}”的最大 Token 必须是大于 0 的整数。`, nodeId: node.id };
      }
    }
    const templates: Array<[string, string]> = [['用户提示词', String(config.prompt || '')]];
    if (config.system_prompt) {
      templates.push(['系统提示词', String(config.system_prompt)]);
    }
    return checkTemplates(templates);
  } else if (node.type === 'llm-decision') {
    if (!String(config.prompt || '').trim()) {
      return { message: `节点“${node.name}”需要填写判断提示词。`, nodeId: node.id };
    }
    const choices = Array.isArray(config.choices)
      ? config.choices.map((choice) => String(choice).trim()).filter(Boolean)
      : [];
    if (choices.length < 2 || choices.length > 20) {
      return { message: `节点“${node.name}”需要配置 2 到 20 个候选标签。`, nodeId: node.id };
    }
    if (new Set(choices).size !== choices.length) {
      return { message: `节点“${node.name}”存在重复的候选标签。`, nodeId: node.id };
    }
    return checkTemplates([['判断提示词', String(config.prompt || '')]]);
  } else if (node.type === 'http-request') {
    const method = String(config.method || '').toUpperCase();
    if (!httpMethods.includes(method)) {
      return { message: `节点“${node.name}”使用了不支持的请求方法。`, nodeId: node.id };
    }
    const url = String(config.url || '').trim();
    if (!url) {
      return { message: `节点“${node.name}”需要填写 URL。`, nodeId: node.id };
    }
    if (!isValidHTTPTemplateURL(url)) {
      return {
        message: `节点“${node.name}”的 URL 必须是 http:// 或 https:// 开头的完整地址，且变量只能写在路径或参数里，不能写在域名部分。`,
        nodeId: node.id,
      };
    }
    const headers = (config.headers && typeof config.headers === 'object' && !Array.isArray(config.headers))
      ? config.headers as Record<string, unknown>
      : {};
    const blocked = findBlockedHeader(headers);
    if (blocked) {
      return {
        message: `节点“${node.name}”的请求头里有 ${blocked}。出于安全考虑，工作流不能直接携带认证信息；如果这个接口需要密钥，请改用 MCP 工具接入。`,
        nodeId: node.id,
      };
    }
    return checkTemplates([
      ['URL', url],
      ['请求体模板', String(config.body_template || '')],
      ...Object.entries(headers).map(([name, value]): [string, string] => [`请求头 ${name}`, String(value)]),
    ]);
  } else if (node.type === 'tool') {
    const kind = String(config.kind || 'builtin');
    if (kind === 'builtin') {
      const toolName = String(config.tool_name || '').trim();
      if (!toolName) {
        return { message: `节点“${node.name}”需要选择内置工具。`, nodeId: node.id };
      }
      if (!builtinTools.value.some((tool) => tool.name === toolName)) {
        return { message: `节点“${node.name}”选择的内置工具“${toolName}”已不可用。`, nodeId: node.id };
      }
    } else if (kind === 'mcp') {
      const serviceID = String(config.service_id || '').trim();
      const toolName = String(config.tool_name || '').trim();
      if (!serviceID) {
        return { message: `节点“${node.name}”需要选择 MCP 服务。`, nodeId: node.id };
      }
      const service = mcpServices.value.find((item) => item.id === serviceID);
      if (!service) {
        return { message: `节点“${node.name}”选择的 MCP 服务“${serviceID}”已不可用。`, nodeId: node.id };
      }
      if (!toolName) {
        return { message: `节点“${node.name}”需要选择 MCP 工具。`, nodeId: node.id };
      }
      if (!service.tools.some((tool) => tool.name === toolName)) {
        return { message: `节点“${node.name}”选择的 MCP 工具“${toolName}”已不可用。`, nodeId: node.id };
      }
    } else if (kind === 'skill') {
      if (!props.sandboxConfigId) {
        return { message: `节点“${node.name}”使用 Skill 前需要先选择运行沙箱。`, nodeId: node.id };
      }
      const skillName = String(config.skill_name || '').trim();
      if (!skillName) {
        return { message: `节点“${node.name}”需要选择 Skill。`, nodeId: node.id };
      }
      if (!skills.value.some((skill) => skill.name === skillName)) {
        return { message: `节点“${node.name}”选择的 Skill“${skillName}”不属于当前沙箱或已不可用。`, nodeId: node.id };
      }
      if (!String(config.task_template || '').trim()) {
        return { message: `节点“${node.name}”需要填写 Skill 任务模板。`, nodeId: node.id };
      }
    } else {
      return { message: `节点“${node.name}”使用了不支持的工具类型。`, nodeId: node.id };
    }
    return checkTemplates([
      ['任务模板', String(config.task_template || '')],
      // 工具参数既可能是纯文本，也可能是整段变量引用，统一按文本扫描。
      ['参数', JSON.stringify(config.arguments || {})],
    ]);
  } else if (node.type === 'end') {
    return checkTemplates([['最终输出模板', String(config.text_template || '')]]);
  }
  return null;
}

function validateDefinition(): boolean {
  const definition = toDefinition();
  if (definition.nodes.length === 0) {
    return failValidation({ message: '工作流至少需要一个节点。' });
  }

  // 有 JSON 还没解析成功时，config 里仍是旧值，直接保存会悄悄丢掉用户的最新输入。
  const brokenJSON = pendingJSONErrors();
  if (brokenJSON.length) {
    const labels: Record<string, string> = { headers: '请求头 JSON', arguments: '参数 JSON' };
    return failValidation({
      message: `${brokenJSON.map((key) => labels[key] || key).join('、')} 还不是合法的 JSON，请先修正：${jsonFieldErrors.value[brokenJSON[0]]}。`,
      nodeId: selectedNodeId.value || undefined,
    });
  }

  const nodes = new Map<string, WorkflowNode>();
  const availableKnowledgeBases = new Set(knowledgeBaseOptions.value.map((item) => item.value));
  const knownNodeIDs = new Set(definition.nodes.map((node) => node.id));
  for (const node of definition.nodes) {
    if (nodes.has(node.id)) {
      return failValidation({ message: `存在重复的节点 ID“${node.id}”。`, nodeId: node.id });
    }
    nodes.set(node.id, node);
    const issue = validateNodeConfiguration(node, availableKnowledgeBases, knownNodeIDs);
    if (issue) return failValidation(issue);
  }

  const startNodes = definition.nodes.filter((node) => node.type === 'start');
  if (startNodes.length !== 1) {
    return failValidation({ message: '工作流必须且只能有一个开始节点。', nodeId: startNodes[0]?.id });
  }
  if (!definition.nodes.some((node) => node.type === 'end')) {
    return failValidation({ message: '工作流至少需要一个结束节点。' });
  }

  const incoming = new Map<string, WorkflowEdge[]>();
  const outgoing = new Map<string, WorkflowEdge[]>();
  const edgeIDs = new Set<string>();
  for (const node of definition.nodes) {
    incoming.set(node.id, []);
    outgoing.set(node.id, []);
  }
  for (const edge of definition.edges) {
    if (!workflowIDPattern.test(edge.id) || edgeIDs.has(edge.id)) {
      return failValidation({ message: `连线 ID“${edge.id}”格式无效或重复。`, edgeId: edge.id });
    }
    edgeIDs.add(edge.id);
    if (!nodes.has(edge.source) || !nodes.has(edge.target) || edge.source === edge.target) {
      return failValidation({ message: '存在引用缺失节点或连接自身的无效连线。', edgeId: edge.id });
    }
    if (edge.is_default && edge.condition?.items?.length) {
      return failValidation({ message: '默认分支不能同时设置条件。', edgeId: edge.id });
    }
    if (edge.condition) {
      if (!['all', 'any'].includes(edge.condition.mode) || edge.condition.items.length > 20) {
        return failValidation({ message: '连线条件模式无效，或条件数量超过 20 个。', edgeId: edge.id });
      }
      for (const item of edge.condition.items) {
        if (!conditionOperatorSet.has(item.operator)) {
          return failValidation({ message: '连线包含不支持的条件操作符。', edgeId: edge.id });
        }
        if (!inputVariablePattern.test(item.variable) && !nodeVariablePattern.test(item.variable)) {
          return failValidation({ message: `条件变量“${item.variable}”格式无效。`, edgeId: edge.id });
        }
      }
    }
    incoming.get(edge.target)!.push(edge);
    outgoing.get(edge.source)!.push(edge);
  }

  const start = startNodes[0];
  const startIncoming = incoming.get(start.id) || [];
  if (startIncoming.length > 0) {
    return failValidation({ message: '开始节点不能有入边。', edgeId: startIncoming[0].id });
  }
  for (const node of definition.nodes) {
    const incomingEdges = incoming.get(node.id) || [];
    const outgoingEdges = outgoing.get(node.id) || [];
    if (incomingEdges.length > 1) {
      return failValidation({ message: `节点“${node.name}”有多条入边，当前版本不支持分支汇聚。`, edgeId: incomingEdges[1].id });
    }
    if (node.type === 'end' && outgoingEdges.length > 0) {
      return failValidation({ message: `结束节点“${node.name}”不能有出边。`, edgeId: outgoingEdges[0].id });
    }
    if (node.type !== 'end' && outgoingEdges.length === 0) {
      return failValidation({ message: `节点“${node.name}”没有出边，请连接后续节点。`, nodeId: node.id });
    }
    const defaultEdges = outgoingEdges.filter((edge) => edge.is_default);
    if (defaultEdges.length > 1) {
      return failValidation({ message: `节点“${node.name}”最多只能有一条默认分支。`, edgeId: defaultEdges[1].id });
    }
    if (outgoingEdges.length > 1) {
      const missingCondition = outgoingEdges.find((edge) => !edge.is_default && !edge.condition?.items?.length);
      if (missingCondition) {
        return failValidation({ message: `节点“${node.name}”有多个分支，非默认分支必须设置条件。`, edgeId: missingCondition.id });
      }
    }
  }

  const seen = new Set<string>([start.id]);
  const queue = [start.id];
  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const edge of outgoing.get(current) || []) {
      if (!seen.has(edge.target)) {
        seen.add(edge.target);
        queue.push(edge.target);
      }
    }
  }
  const unreachable = definition.nodes.find((node) => !seen.has(node.id));
  if (unreachable) {
    return failValidation({ message: `节点“${unreachable.name}”无法从开始节点到达。`, nodeId: unreachable.id });
  }

  const visiting = new Set<string>();
  const visited = new Set<string>();
  let cycleEdgeID = '';
  const visit = (id: string): boolean => {
    visiting.add(id);
    for (const edge of outgoing.get(id) || []) {
      if (visiting.has(edge.target)) {
        cycleEdgeID = edge.id;
        return false;
      }
      if (!visited.has(edge.target) && !visit(edge.target)) return false;
    }
    visiting.delete(id);
    visited.add(id);
    return true;
  };
  if (!visit(start.id)) {
    return failValidation({ message: '工作流不能包含环形连线。', edgeId: cycleEdgeID });
  }

  for (const edge of definition.edges) {
    if (!edge.condition) continue;
    const allowed = new Set<string>();
    let current = edge.source;
    while (current && !allowed.has(current)) {
      allowed.add(current);
      current = incoming.get(current)?.[0]?.source || '';
    }
    for (const item of edge.condition.items) {
      if (inputVariablePattern.test(item.variable)) continue;
      const nodeMatch = item.variable.match(nodeVariablePattern);
      if (nodeMatch && !allowed.has(nodeMatch[1])) {
        return failValidation({
          message: `条件变量“${item.variable}”不在当前连线的上游路径中。`,
          edgeId: edge.id,
        });
      }
    }
  }
  return passValidation();
}

// 父组件需要知道画布是否还是空白流程，用来决定是否展示首次引导。
defineExpose({
  validate: validateDefinition,
  isUntouched: () => isUntouchedFlow.value,
  isTemplateGalleryOpen: () => showTemplateGallery.value,
});
</script>

<style scoped lang="less">
.workflow-editor {
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
  color: var(--td-text-color-primary);
  font-family: inherit;
}

/* --------------------------------------------------------------------------
   顶栏工具区：轻盈现代 Studio 风格，释放垂直视口空间
   -------------------------------------------------------------------------- */
.workflow-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 0 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.workflow-toolbar-left {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.workflow-toolbar-title-wrap {
  display: flex;
  align-items: center;
  gap: 10px;
}

.workflow-title {
  margin: 0;
  font-size: 17px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.workflow-stats-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  color: var(--td-brand-color);
  font-size: 12px;
  font-weight: 500;
}

.workflow-subtitle {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.workflow-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.workflow-toolbar-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 32px;
  padding: 0 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 7px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.18s cubic-bezier(0.4, 0, 0.2, 1);
  user-select: none;

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 4%, var(--td-bg-color-container));
  }

  &.is-active {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 10%, var(--td-bg-color-container));
    color: var(--td-brand-color);
  }

  &--icon {
    width: 32px;
    padding: 0;
  }

  &--primary {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color);
    color: #fff;
    box-shadow: 0 2px 8px rgba(0, 82, 217, 0.22);

    &:hover:not(:disabled) {
      background: var(--td-brand-color-hover);
      border-color: var(--td-brand-color-hover);
      color: #fff;
      box-shadow: 0 4px 12px rgba(0, 82, 217, 0.32);
    }
  }

  &--danger {
    color: var(--td-text-color-secondary);

    &:hover:not(:disabled) {
      border-color: var(--td-error-color);
      color: var(--td-error-color);
      background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
    }
  }

  &--add-condition {
    width: 100%;
    margin-top: 8px;
    border-style: dashed;
    color: var(--td-brand-color);

    &:hover:not(:disabled) {
      border-style: solid;
      background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    }
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }
}

/* --------------------------------------------------------------------------
   新手向导折叠横幅
   -------------------------------------------------------------------------- */
.workflow-onboarding {
  position: relative;
  margin-top: 10px;
  padding: 10px 36px 10px 12px;
  border: 1px solid color-mix(in srgb, var(--td-brand-color) 20%, var(--td-component-stroke));
  border-radius: 8px;
  background: color-mix(in srgb, var(--td-brand-color) 4%, var(--td-bg-color-secondarycontainer));
  flex-shrink: 0;
}

.workflow-onboarding-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.workflow-onboarding-step {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
}

.workflow-onboarding-index {
  display: inline-flex;
  width: 20px;
  height: 20px;
  align-items: center;
  justify-content: center;
  flex: 0 0 20px;
  border-radius: 50%;
  background: var(--td-brand-color);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
  margin-top: 1px;
}

.workflow-onboarding-content {
  min-width: 0;

  strong {
    display: block;
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  small {
    display: block;
    margin-top: 2px;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    line-height: 1.4;
  }
}

.workflow-onboarding-close {
  position: absolute;
  top: 8px;
  right: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  border-radius: 4px;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

/* --------------------------------------------------------------------------
   工作流主布局：Palette (220px) | Canvas (自适应) | Inspector (380px)
   -------------------------------------------------------------------------- */
.workflow-layout {
  display: grid;
  grid-template-columns: 220px minmax(460px, 1fr) 380px;
  flex: 1;
  min-height: 0;
  margin-top: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  overflow: hidden;
  box-shadow: 0 2px 12px rgba(15, 23, 42, 0.04);
}

/* --------------------------------------------------------------------------
   左侧节点组件库 (Palette)
   -------------------------------------------------------------------------- */
.workflow-palette {
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow-y: auto;
  padding: 14px 12px;
  border-right: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}

.workflow-panel-header {
  margin-bottom: 6px;
}

.workflow-panel-heading {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.workflow-panel-hint {
  margin: 3px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
}

.workflow-palette-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
}

.workflow-palette-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  text-align: left;
  cursor: grab;
  transition: all 0.18s cubic-bezier(0.4, 0, 0.2, 1);

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color);
    box-shadow: 0 3px 10px rgba(15, 23, 42, 0.06);
    transform: translateY(-1px);
  }

  &:active:not(:disabled) {
    cursor: grabbing;
    transform: scale(0.99);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }
}

.workflow-palette-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  border-radius: 7px;
  font-size: 16px;
}

.workflow-palette-info {
  min-width: 0;
  flex: 1;

  strong {
    display: block;
    font-size: 13px;
    font-weight: 600;
    line-height: 1.35;
  }

  small {
    display: block;
    margin-top: 2px;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    line-height: 1.35;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

/* 节点组件专属色调 */
.workflow-palette-item--start .workflow-palette-icon { background: #ecfdf5; color: #059669; }
.workflow-palette-item--llm .workflow-palette-icon { background: #eff6ff; color: #0052d9; }
.workflow-palette-item--knowledge-retrieval .workflow-palette-icon { background: #e0f2fe; color: #0284c7; }
.workflow-palette-item--llm-decision .workflow-palette-icon { background: #f5f3ff; color: #7c3aed; }
.workflow-palette-item--http-request .workflow-palette-icon { background: #fff1f2; color: #e11d48; }
.workflow-palette-item--tool .workflow-palette-icon { background: #fffbeb; color: #d97706; }
.workflow-palette-item--end .workflow-palette-icon { background: #f0fdfa; color: #0d9488; }

.workflow-legend {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-top: auto;
  padding-top: 14px;
  border-top: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.workflow-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.workflow-legend-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;

  &--start { background: #059669; }
  &--end { background: #0d9488; }
}

/* --------------------------------------------------------------------------
   中央画布 (Canvas) 与 VueFlow 自定义节点
   -------------------------------------------------------------------------- */
.workflow-canvas {
  position: relative;
  min-width: 0;
  min-height: 480px;
  background-color: #f8fafc;
  background-image: radial-gradient(#cbd5e1 1px, transparent 1px);
  background-size: 20px 20px;

  &--disabled {
    background-color: var(--td-bg-color-container);
  }

  :deep(.vue-flow) {
    width: 100%;
    height: 100%;
  }

  :deep(.vue-flow__node) {
    cursor: default;
    border: none;
    background: transparent;
    padding: 0;
  }

  :deep(.vue-flow__edge-path) {
    stroke: #94a3b8;
    stroke-width: 2px;
    transition: stroke 0.2s cubic-bezier(0.4, 0, 0.2, 1), stroke-width 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  }

  :deep(.vue-flow__arrowhead polyline) {
    stroke: #94a3b8 !important;
    fill: #94a3b8 !important;
    transition: stroke 0.2s cubic-bezier(0.4, 0, 0.2, 1), fill 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  }

  :deep(.vue-flow__edge:hover .vue-flow__edge-path) {
    stroke: #475569;
    stroke-width: 2.5px;
  }

  :deep(.vue-flow__edge:hover .vue-flow__arrowhead polyline) {
    stroke: #475569 !important;
    fill: #475569 !important;
  }

  :deep(.vue-flow__edge.selected .vue-flow__edge-path) {
    stroke: var(--td-brand-color, #0052d9);
    stroke-width: 2.5px;
  }

  :deep(.vue-flow__edge.selected .vue-flow__arrowhead polyline) {
    stroke: var(--td-brand-color, #0052d9) !important;
    fill: var(--td-brand-color, #0052d9) !important;
  }

  :deep(.vue-flow__connection-path) {
    stroke: var(--td-brand-color, #0052d9);
    stroke-width: 2px;
    stroke-dasharray: 5;
    animation: dashdraw 0.5s linear infinite;
  }
}

/* 自定义节点卡片 */
.workflow-node-card {
  position: relative;
  width: 210px;
  border-radius: 10px;
  background: var(--td-bg-color-container, #ffffff);
  border: 1px solid #cbd5e1;
  box-shadow: 0 3px 12px rgba(15, 23, 42, 0.06);
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  overflow: visible;

  &:hover {
    border-color: #94a3b8;
    box-shadow: 0 6px 18px rgba(15, 23, 42, 0.1);
  }

  &.is-selected {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px var(--td-brand-color), 0 8px 24px rgba(0, 82, 217, 0.18);
    transform: translateY(-1px);
  }
}

.workflow-node-stripe {
  height: 3.5px;
  border-radius: 10px 10px 0 0;
  background: #94a3b8;
}

.workflow-node-card--start .workflow-node-stripe { background: #059669; }
.workflow-node-card--llm .workflow-node-stripe { background: #0052d9; }
.workflow-node-card--knowledge-retrieval .workflow-node-stripe { background: #0284c7; }
.workflow-node-card--llm-decision .workflow-node-stripe { background: #7c3aed; }
.workflow-node-card--http-request .workflow-node-stripe { background: #e11d48; }
.workflow-node-card--tool .workflow-node-stripe { background: #d97706; }
.workflow-node-card--end .workflow-node-stripe { background: #0d9488; }

.workflow-node-body {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px 11px;
}

.workflow-node-icon-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  border-radius: 6px;
  font-size: 15px;
  margin-top: 1px;
}

.workflow-node-card--start .workflow-node-icon-badge { background: #ecfdf5; color: #059669; }
.workflow-node-card--llm .workflow-node-icon-badge { background: #eff6ff; color: #0052d9; }
.workflow-node-card--knowledge-retrieval .workflow-node-icon-badge { background: #e0f2fe; color: #0284c7; }
.workflow-node-card--llm-decision .workflow-node-icon-badge { background: #f5f3ff; color: #7c3aed; }
.workflow-node-card--http-request .workflow-node-icon-badge { background: #fff1f2; color: #e11d48; }
.workflow-node-card--tool .workflow-node-icon-badge { background: #fffbeb; color: #d97706; }
.workflow-node-card--end .workflow-node-icon-badge { background: #f0fdfa; color: #0d9488; }

.workflow-node-text-wrap {
  min-width: 0;
  flex: 1;
}

.workflow-node-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  line-height: 1.35;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.workflow-node-snippet {
  margin-top: 3px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.35;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 四向连接桩 Handle 优化 */
.workflow-node-handle {
  width: 10px !important;
  height: 10px !important;
  border-radius: 50% !important;
  border: 2px solid #ffffff !important;
  background: var(--td-brand-color, #0052d9) !important;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.18) !important;
  opacity: 0.65;
  pointer-events: all !important;
  cursor: crosshair !important;
  transition: opacity 0.2s cubic-bezier(0.4, 0, 0.2, 1), box-shadow 0.2s cubic-bezier(0.4, 0, 0.2, 1), transform 0.2s cubic-bezier(0.4, 0, 0.2, 1) !important;
  z-index: 10;

  /* 扩大鼠标命中热区至 26px，方便用户极其顺手地抓取连线与吸附 */
  &::after {
    content: '';
    position: absolute;
    top: -8px;
    left: -8px;
    right: -8px;
    bottom: -8px;
    border-radius: 50%;
    cursor: crosshair;
    pointer-events: all;
  }

  &:hover {
    opacity: 1 !important;
  }
}

/* 节点悬停或选中时，四个方位的 Handle 全显 */
.workflow-node-card:hover .workflow-node-handle,
.workflow-node-card.is-selected .workflow-node-handle {
  opacity: 1;
}

/* 各方位在 Hover 时的动效，保留原 translate 同时平滑放大 1.4 倍并伴随柔和呼吸光晕 */
.workflow-node-handle--top:hover {
  transform: translate(-50%, -50%) scale(1.4) !important;
  box-shadow: 0 0 0 4px rgba(0, 82, 217, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
}

.workflow-node-handle--right:hover {
  transform: translate(50%, -50%) scale(1.4) !important;
  box-shadow: 0 0 0 4px rgba(0, 82, 217, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
}

.workflow-node-handle--bottom:hover {
  transform: translate(-50%, 50%) scale(1.4) !important;
  box-shadow: 0 0 0 4px rgba(0, 82, 217, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
}

.workflow-node-handle--left:hover {
  transform: translate(-50%, -50%) scale(1.4) !important;
  box-shadow: 0 0 0 4px rgba(0, 82, 217, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
}

/* 根据节点主题色呼应 Handle 色彩 */
.workflow-node-card--start .workflow-node-handle {
  background: #059669 !important;
  &--top:hover, &--right:hover, &--bottom:hover, &--left:hover {
    box-shadow: 0 0 0 4px rgba(5, 150, 105, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
  }
}

.workflow-node-card--end .workflow-node-handle {
  background: #0d9488 !important;
  &--top:hover, &--right:hover, &--bottom:hover, &--left:hover {
    box-shadow: 0 0 0 4px rgba(13, 148, 136, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
  }
}

.workflow-node-card--knowledge-retrieval .workflow-node-handle {
  background: #0284c7 !important;
  &--top:hover, &--right:hover, &--bottom:hover, &--left:hover {
    box-shadow: 0 0 0 4px rgba(2, 132, 199, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
  }
}

.workflow-node-card--llm-decision .workflow-node-handle {
  background: #7c3aed !important;
  &--top:hover, &--right:hover, &--bottom:hover, &--left:hover {
    box-shadow: 0 0 0 4px rgba(124, 58, 237, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
  }
}

.workflow-node-card--http-request .workflow-node-handle {
  background: #e11d48 !important;
  &--top:hover, &--right:hover, &--bottom:hover, &--left:hover {
    box-shadow: 0 0 0 4px rgba(225, 29, 72, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
  }
}

.workflow-node-card--tool .workflow-node-handle {
  background: #d97706 !important;
  &--top:hover, &--right:hover, &--bottom:hover, &--left:hover {
    box-shadow: 0 0 0 4px rgba(217, 119, 6, 0.25), 0 2px 6px rgba(0, 0, 0, 0.2) !important;
  }
}

.workflow-placeholder-note {
  position: absolute;
  top: 12px;
  left: 50%;
  z-index: 3;
  display: flex;
  max-width: calc(100% - 100px);
  align-items: center;
  gap: 7px;
  padding: 6px 12px;
  transform: translateX(-50%);
  border: 1px solid color-mix(in srgb, var(--td-brand-color) 25%, var(--td-component-stroke));
  border-radius: 7px;
  background: color-mix(in srgb, var(--td-brand-color) 7%, var(--td-bg-color-container));
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
  pointer-events: none;
}

.workflow-canvas-empty {
  position: absolute;
  top: 50%;
  left: 50%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  transform: translate(-50%, -50%);
  color: var(--td-text-color-secondary);
  pointer-events: none;

  strong {
    color: var(--td-text-color-primary);
    font-size: 14px;
  }

  span {
    font-size: 12px;
  }
}

/* --------------------------------------------------------------------------
   右侧属性检查器 (Inspector)
   -------------------------------------------------------------------------- */
.workflow-inspector {
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow-y: auto;
  padding: 16px;
  border-left: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.workflow-inspector-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.workflow-inspector-header-left {
  min-width: 0;
  flex: 1;
}

.workflow-node-type-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 7px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);

  &--start { background: #ecfdf5; color: #059669; }
  &--llm { background: #eff6ff; color: #0052d9; }
  &--knowledge-retrieval { background: #e0f2fe; color: #0284c7; }
  &--llm-decision { background: #f5f3ff; color: #7c3aed; }
  &--http-request { background: #fff1f2; color: #e11d48; }
  &--tool { background: #fffbeb; color: #d97706; }
  &--end { background: #f0fdfa; color: #0d9488; }
  &--edge { background: #f1f5f9; color: #475569; }
}

.workflow-inspector-title {
  margin: 6px 0 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  line-height: 1.35;
  word-break: break-all;
}

.workflow-node-id-row {
  margin-top: 5px;
}

.workflow-node-id-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 7px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.16s ease;

  code {
    font-family: inherit;
    color: var(--td-text-color-primary);
  }

  .workflow-copy-icon {
    color: var(--td-text-color-placeholder);
    transition: color 0.16s ease;
  }

  &:hover {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);

    .workflow-copy-icon {
      color: var(--td-brand-color);
    }
  }
}

/* Inspector 分组卡片 */
.workflow-inspector-section {
  padding: 12px;
  margin-bottom: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);

  &--help {
    background: transparent;
    border-style: dashed;
  }
}

.workflow-section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin-bottom: 10px;
}

.workflow-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 12px;

  &:last-child {
    margin-bottom: 0;
  }
}

.workflow-field-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.workflow-required {
  color: var(--td-error-color);
  font-style: normal;
  margin-left: 2px;
}

.workflow-field-help {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.45;

  code {
    padding: 1px 4px;
    border-radius: 3px;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px;
    color: var(--td-brand-color);
  }
}

.workflow-field-grid {
  display: flex;
  gap: 10px;
}

.workflow-field--inline {
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
}

/* 高级输入框与文本域 */
.workflow-input,
.workflow-textarea {
  box-sizing: border-box;
  width: 100%;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font: inherit;
  font-size: 13px;
  transition: all 0.16s ease;

  &:focus {
    border-color: var(--td-brand-color);
    outline: none;
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  }

  &:disabled {
    cursor: not-allowed;
    background: var(--td-bg-color-secondarycontainer);
    opacity: 0.6;
  }
}

.workflow-input {
  height: 34px;
  padding: 0 10px;

  &--small {
    height: 30px;
    font-size: 12px;
  }
}

.workflow-textarea {
  min-height: 72px;
  padding: 8px 10px;
  resize: vertical;
  line-height: 1.5;
}

/* 提示与报警条 */
.workflow-field-alert {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin-top: 6px;
  padding: 7px 10px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.45;

  &--error {
    border: 1px solid color-mix(in srgb, var(--td-error-color) 30%, transparent);
    background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
    color: var(--td-error-color);
  }

  &--info {
    border: 1px solid color-mix(in srgb, var(--td-brand-color) 25%, transparent);
    background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
    color: var(--td-text-color-secondary);
  }
}

.workflow-resource-empty {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 12px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 7px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 12px;

  strong {
    color: var(--td-text-color-primary);
    font-size: 13px;
  }
}

.workflow-link-button {
  border: none;
  background: transparent;
  color: var(--td-brand-color);
  padding: 0;
  font-size: 12px;
  cursor: pointer;
  text-decoration: underline;

  &:hover {
    color: var(--td-brand-color-hover);
  }
}

/* 变量选取药丸 */
.workflow-variable-picker {
  margin-top: 10px;
}

.workflow-variable-picker-title {
  display: block;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  margin-bottom: 6px;
}

.workflow-variable-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.workflow-variable-chip {
  padding: 3px 8px;
  border: 1px solid color-mix(in srgb, var(--td-brand-color) 30%, transparent);
  border-radius: 999px;
  background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
  color: var(--td-brand-color);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.16s ease;

  &:hover:not(:disabled) {
    background: var(--td-brand-color);
    border-color: var(--td-brand-color);
    color: #fff;
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }
}

.workflow-code-tag {
  display: inline-block;
  padding: 1px 5px;
  margin: 2px;
  border-radius: 4px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--td-brand-color);
}

/* 帮助参考 */
.workflow-help-grid {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.workflow-help-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 12px;
  line-height: 1.45;

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    color: var(--td-brand-color);
    background: var(--td-bg-color-container);
    padding: 2px 6px;
    border-radius: 4px;
    border: 1px solid var(--td-component-stroke);
    word-break: break-all;
  }

  span:last-child {
    color: var(--td-text-color-secondary);
  }
}

.workflow-help-label {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

/* 连线配置样式 */
.workflow-switch-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.workflow-switch-meta {
  strong {
    display: block;
    font-size: 13px;
    font-weight: 600;
  }

  small {
    display: block;
    margin-top: 2px;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    line-height: 1.4;
  }
}

.workflow-condition-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 10px;
}

.workflow-condition-card {
  padding: 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
}

.workflow-condition-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.workflow-condition-index-badge {
  font-size: 12px;
  font-weight: 600;
  color: var(--td-brand-color);
}

.workflow-icon-btn-subtle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  border-radius: 4px;
  cursor: pointer;

  &:hover {
    color: var(--td-error-color);
    background: color-mix(in srgb, var(--td-error-color) 10%, transparent);
  }
}

.workflow-condition-row-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px;
  margin-bottom: 6px;
}

/* 空状态指示 */
.workflow-inspector-empty {
  display: flex;
  min-height: 240px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 24px;
  color: var(--td-text-color-secondary);
  text-align: center;

  strong {
    color: var(--td-text-color-primary);
    font-size: 14px;
  }

  p {
    margin: 0;
    font-size: 12px;
    line-height: 1.6;
    max-width: 220px;
  }
}

.workflow-empty-icon-ring {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--td-brand-color) 8%, var(--td-bg-color-secondarycontainer));
  color: var(--td-brand-color);
}

/* 校验提示条 */
.workflow-validation {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
  padding: 9px 12px;
  border-radius: 7px;
  font-size: 13px;
  flex-shrink: 0;

  &--success {
    border: 1px solid color-mix(in srgb, var(--td-success-color) 30%, transparent);
    background: color-mix(in srgb, var(--td-success-color) 8%, transparent);
    color: var(--td-success-color);
  }

  &--error {
    border: 1px solid color-mix(in srgb, var(--td-error-color) 30%, transparent);
    background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
    color: var(--td-error-color);
  }

  &--hint {
    border: 1px solid color-mix(in srgb, var(--td-warning-color) 30%, transparent);
    background: color-mix(in srgb, var(--td-warning-color) 8%, transparent);
    color: var(--td-warning-color);
  }
}

/* --------------------------------------------------------------------------
   模板画廊面板
   -------------------------------------------------------------------------- */
.workflow-templates {
  display: flex;
  flex-direction: column;
  gap: 14px;
  overflow-y: auto;
  padding-right: 2px;
  flex: 1;
  min-height: 0;
  margin-top: 12px;
}

.workflow-templates-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.workflow-templates-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.workflow-templates-subtitle {
  max-width: 68ch;
  margin: 4px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
}

.workflow-template-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 14px;
  align-content: start;
}

.workflow-template-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  text-align: left;
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color);
    box-shadow: 0 6px 18px rgba(15, 23, 42, 0.08);
    transform: translateY(-2px);
  }

  &--active {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 1.5px var(--td-brand-color);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }
}

.workflow-template-card-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.workflow-template-icon {
  display: inline-flex;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-size: 16px;
}

.workflow-template-heading {
  strong {
    display: block;
    font-size: 14px;
    font-weight: 600;
  }

  small {
    display: block;
    margin-top: 3px;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    line-height: 1.45;
  }
}

.workflow-template-flow {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
  counter-reset: workflow-template-step;

  li {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 8px;
    border-radius: 4px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: 12px;
    counter-increment: workflow-template-step;

    &::before {
      content: counter(workflow-template-step);
      color: var(--td-brand-color);
      font-weight: 600;
    }

    & + li::before {
      content: '→';
      margin-right: 2px;
      color: var(--td-text-color-placeholder);
      font-weight: 400;
    }
  }
}

.workflow-template-detail {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}

.workflow-template-needs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: auto;
  padding-top: 4px;
}

.workflow-template-needs-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--td-warning-color);
  font-size: 12px;
  font-weight: 600;
}

.workflow-template-need {
  padding: 2px 7px;
  border-radius: 4px;
  background: var(--td-warning-color-light);
  color: var(--td-warning-color-8);
  font-size: 12px;
}

.workflow-template-action {
  color: var(--td-brand-color);
  font-size: 13px;
  font-weight: 600;
}

/* --------------------------------------------------------------------------
   响应式断点适配
   -------------------------------------------------------------------------- */
@media (max-width: 1280px) {
  .workflow-layout {
    grid-template-columns: 200px minmax(360px, 1fr) 340px;
  }
}

@media (max-width: 960px) {
  .workflow-onboarding-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .workflow-layout {
    grid-template-columns: 180px minmax(280px, 1fr);
  }

  .workflow-inspector {
    position: absolute;
    right: 12px;
    bottom: 12px;
    z-index: 5;
    width: 320px;
    max-height: 75%;
    border: 1px solid var(--td-component-stroke);
    box-shadow: 0 10px 30px rgba(15, 23, 42, 0.18);
  }
}

@media (max-width: 680px) {
  .workflow-toolbar {
    flex-direction: column;
    align-items: flex-start;
  }

  .workflow-toolbar-actions {
    width: 100%;
    flex-wrap: wrap;
    justify-content: flex-start;
  }

  .workflow-onboarding-grid {
    grid-template-columns: 1fr;
  }

  .workflow-layout {
    display: block;
  }

  .workflow-palette {
    border-right: none;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .workflow-palette-list {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
  }

  .workflow-inspector {
    position: static;
    width: auto;
    max-height: none;
    border-top: 1px solid var(--td-component-stroke);
    border-left: none;
    box-shadow: none;
  }
}
</style>
