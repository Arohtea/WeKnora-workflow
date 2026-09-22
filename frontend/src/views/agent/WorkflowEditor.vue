<template>
  <div class="workflow-editor">
    <div class="workflow-toolbar">
      <div class="workflow-toolbar-left">
        <div class="workflow-toolbar-title-wrap">
          <h3 class="workflow-title">流程编排</h3>
          <span class="workflow-stats-badge">{{ flowNodes.length }} 节点 · {{ flowEdges.length }} 连线</span>
          <span class="workflow-revision-badge" :title="`草稿修订号 r${draftRevision}`">
            草稿 r{{ draftRevision }}
          </span>
          <span class="workflow-revision-badge" :title="publishedVersion ? `已发布 v${publishedVersion}` : '尚未发布'">
            {{ publishedVersion ? `已发布 v${publishedVersion}` : '未发布' }}
          </span>
          <span
            class="workflow-unpublished-badge"
            :class="{ 'is-dirty': hasUnpublishedChanges }"
          >
            {{ hasUnpublishedChanges ? '有未发布修改' : '已是最新' }}
          </span>
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
          class="workflow-toolbar-btn workflow-toolbar-btn--icon"
          title="导入工作流 (JSON)"
          :disabled="disabled"
          @click="triggerImportWorkflow"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="7 10 12 15 17 10" />
            <line x1="12" y1="15" x2="12" y2="3" />
          </svg>
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn workflow-toolbar-btn--icon"
          title="导出工作流 (JSON)"
          :disabled="disabled || flowNodes.length === 0"
          @click="triggerExportWorkflow"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="17 8 12 3 7 8" />
            <line x1="12" y1="3" x2="12" y2="15" />
          </svg>
        </button>
        <input
          ref="workflowFileInputRef"
          type="file"
          accept=".json,application/json"
          style="display: none;"
          @change="onWorkflowFileSelected"
        />
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
          class="workflow-toolbar-btn workflow-toolbar-btn--debug"
          :disabled="disabled || !agentId || debugLoading"
          title="使用当前草稿快照在编辑器内试跑"
          @click="openDebugRun"
        >
          <t-icon :name="debugLoading ? 'loading' : 'play-circle'" />
          <span>试跑</span>
        </button>
        <button
          type="button"
          class="workflow-toolbar-btn workflow-toolbar-btn--primary"
          :disabled="!canPublish"
          :title="hasUnpublishedChanges ? '校验并发布当前草稿为新版本' : '发布当前草稿为新版本'"
          @click="publishDefinition"
        >
          <t-icon :name="publishInFlight ? 'loading' : 'cloud-upload'" />
          <span>{{ publishInFlight ? '发布中…' : '发布' }}</span>
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
        @dragenter.prevent
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
          @node-drag-start="onNodeDragStart"
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
                  :title="`点击复制下游标准引用变量 ${primaryVariableForSelectedNode}`"
                  @click="copyNodeVariable(selectedNode.id)"
                >
                  <t-icon name="code" size="12px" />
                  <code>{{ primaryVariableForSelectedNode }}</code>
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

          <!-- 分支模式：仅对非开始节点有意义，开始节点没有上游路由语义 -->
          <div v-if="selectedNode.data.workflowType !== 'start'" class="workflow-inspector-section">
            <div class="workflow-section-title">多分支执行方式</div>
            <div class="workflow-branch-mode">
              <button
                type="button"
                class="workflow-branch-mode-option"
                :class="{ 'is-active': selectedNode.data.branchMode !== 'all_match' }"
                :disabled="disabled"
                @click="updateNodeBranchMode('first_match')"
              >
                <strong>只走第一条命中</strong>
                <small>按出边顺序判断，命中第一个条件为真的分支后就停止，其余分支不执行。适合"二选一"的互斥判断。</small>
              </button>
              <button
                type="button"
                class="workflow-branch-mode-option"
                :class="{ 'is-active': selectedNode.data.branchMode === 'all_match' }"
                :disabled="disabled"
                @click="updateNodeBranchMode('all_match')"
              >
                <strong>并行走全部命中</strong>
                <small>所有条件为真的分支同时执行，适合一次触发多个下游动作（如同时查库并调用接口），再由各自的输出节点汇总。</small>
              </button>
            </div>
            <small class="workflow-field-help">仅当该节点有多条出边时才会生效；单条出边不会触发分支选择。</small>
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
                <div v-if="quickUpstreamVariableOptions.length" class="workflow-variable-picker">
                  <span class="workflow-variable-picker-title">快捷插入变量：</span>
                  <div class="workflow-variable-chips">
                    <button
                      v-for="opt in quickUpstreamVariableOptions"
                      :key="opt.value"
                      type="button"
                      class="workflow-variable-chip"
                      :disabled="disabled"
                      :title="opt.hint"
                      @click="appendTemplateVariable('query_template', opt.value)"
                    >
                      + {{ opt.label }}
                    </button>
                  </div>
                </div>
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
                <div v-if="quickUpstreamVariableOptions.length" class="workflow-variable-picker">
                  <span class="workflow-variable-picker-title">快捷插入变量：</span>
                  <div class="workflow-variable-chips">
                    <button
                      v-for="opt in quickUpstreamVariableOptions"
                      :key="opt.value"
                      type="button"
                      class="workflow-variable-chip"
                      :disabled="disabled"
                      :title="opt.hint"
                      @click="appendTemplateVariable('system_prompt', opt.value)"
                    >
                      + {{ opt.label }}
                    </button>
                  </div>
                </div>
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
                  支持使用 <code>&#123;&#123;input.query&#125;&#125;</code> 或上游节点输出 <code>&#123;&#123;nodes.&lt;节点ID&gt;.text&#125;&#125;</code>。
                </small>
                <div v-if="quickUpstreamVariableOptions.length" class="workflow-variable-picker">
                  <span class="workflow-variable-picker-title">快捷插入上游变量：</span>
                  <div class="workflow-variable-chips">
                    <button
                      v-for="opt in quickUpstreamVariableOptions"
                      :key="opt.value"
                      type="button"
                      class="workflow-variable-chip"
                      :disabled="disabled"
                      :title="opt.hint"
                      @click="appendTemplateVariable('prompt', opt.value)"
                    >
                      + {{ opt.label }}
                    </button>
                  </div>
                </div>
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
                <div v-if="quickUpstreamVariableOptions.length" class="workflow-variable-picker">
                  <span class="workflow-variable-picker-title">快捷插入变量：</span>
                  <div class="workflow-variable-chips">
                    <button
                      v-for="opt in quickUpstreamVariableOptions"
                      :key="opt.value"
                      type="button"
                      class="workflow-variable-chip"
                      :disabled="disabled"
                      :title="opt.hint"
                      @click="appendTemplateVariable('prompt', opt.value)"
                    >
                      + {{ opt.label }}
                    </button>
                  </div>
                </div>
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
                <div v-if="quickUpstreamVariableOptions.length" class="workflow-variable-picker">
                  <span class="workflow-variable-picker-title">快捷插入变量：</span>
                  <div class="workflow-variable-chips">
                    <button
                      v-for="opt in quickUpstreamVariableOptions"
                      :key="opt.value"
                      type="button"
                      class="workflow-variable-chip"
                      :disabled="disabled"
                      :title="opt.hint"
                      @click="appendTemplateVariable('body_template', opt.value)"
                    >
                      + {{ opt.label }}
                    </button>
                  </div>
                </div>
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
                    v-for="tool in formattedBuiltinTools"
                    :key="tool.name"
                    :value="tool.name"
                    :label="tool.label"
                  />
                </t-select>
                <small v-if="selectedBuiltinToolHelp" class="workflow-field-help">{{ selectedBuiltinToolHelp }}</small>
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
                <div v-if="quickUpstreamVariableOptions.length" class="workflow-variable-picker">
                  <span class="workflow-variable-picker-title">快捷插入变量：</span>
                  <div class="workflow-variable-chips">
                    <button
                      v-for="opt in quickUpstreamVariableOptions"
                      :key="opt.value"
                      type="button"
                      class="workflow-variable-chip"
                      :disabled="disabled"
                      :title="opt.hint"
                      @click="appendTemplateVariable('task_template', opt.value)"
                    >
                      + {{ opt.label }}
                    </button>
                  </div>
                </div>
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
              <span>流程入口节点。下游节点可通过 <code>&#123;&#123;input.query&#125;&#125;</code> 获取用户提问，通过 <code>&#123;&#123;input.attachments_text&#125;&#125;</code> 读取附件提取文本。</span>
            </div>
          </div>

          <!-- 当前步骤输出与下游引用规范 -->
          <div class="workflow-inspector-section workflow-inspector-section--help">
            <div class="workflow-section-title">
              <span>当前步骤输出与下游引用</span>
            </div>
            <small class="workflow-field-help" style="margin-bottom: 8px; display: block;">
              下游步骤（提示词、文本模板或分支连线）可引用的当前步骤参数规范：
            </small>
            <div class="workflow-output-cards">
              <div
                v-for="field in selectedNodeOutputFields"
                :key="field.fullPath"
                class="workflow-output-card"
              >
                <div class="workflow-output-card-top">
                  <div class="workflow-output-card-meta">
                    <strong class="workflow-output-card-label">{{ field.label }}</strong>
                    <span class="workflow-type-badge">{{ field.type }}</span>
                    <span v-if="field.isPrimary" class="workflow-primary-tag">主要输出</span>
                  </div>
                </div>
                <div class="workflow-output-card-desc">{{ field.desc }}</div>
                <div class="workflow-output-card-codes">
                  <div class="workflow-code-row">
                    <span class="workflow-code-row-title">模板语法：</span>
                    <code
                      class="workflow-clickable-code"
                      title="点击复制模板变量"
                      @click="copyVariableText(field.templateSyntax, `已复制模板变量 ${field.templateSyntax}`)"
                    >
                      {{ field.templateSyntax }}
                    </code>
                    <button
                      type="button"
                      class="workflow-copy-mini-btn"
                      title="复制模板语法"
                      @click="copyVariableText(field.templateSyntax, `已复制模板变量 ${field.templateSyntax}`)"
                    >
                      <t-icon name="copy" size="12px" />
                    </button>
                  </div>
                  <div class="workflow-code-row">
                    <span class="workflow-code-row-title">条件路径：</span>
                    <code
                      class="workflow-clickable-code"
                      title="点击复制条件路径"
                      @click="copyVariableText(field.fullPath, `已复制条件路径 ${field.fullPath}`)"
                    >
                      {{ field.fullPath }}
                    </code>
                    <button
                      type="button"
                      class="workflow-copy-mini-btn"
                      title="复制条件路径"
                      @click="copyVariableText(field.fullPath, `已复制条件路径 ${field.fullPath}`)"
                    >
                      <t-icon name="copy" size="12px" />
                    </button>
                  </div>
                </div>
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
              <h3 class="workflow-inspector-title">{{ getNodeName(selectedEdge.source) }} → {{ getNodeName(selectedEdge.target) }}</h3>
              <div class="workflow-edge-endpoints-row">
                <span class="workflow-edge-endpoint-chip" :title="`起点节点: ${getNodeName(selectedEdge.source)} (${selectedEdge.source})`">
                  <span class="workflow-edge-endpoint-dot workflow-edge-endpoint-dot--source" />
                  <code>{{ selectedEdge.source }}</code>
                </span>
                <t-icon name="arrow-right" size="12px" class="workflow-edge-arrow-icon" />
                <span class="workflow-edge-endpoint-chip" :title="`终点节点: ${getNodeName(selectedEdge.target)} (${selectedEdge.target})`">
                  <span class="workflow-edge-endpoint-dot workflow-edge-endpoint-dot--target" />
                  <code>{{ selectedEdge.target }}</code>
                </span>
              </div>
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
                      :auto-width="false"
                      class="workflow-condition-select-var"
                      @change="updateConditionItem(index, 'variable', String($event))"
                    >
                      <t-option
                        v-for="opt in currentEdgeVariableOptions"
                        :key="opt.value"
                        :value="opt.value"
                        :label="opt.label"
                      />
                    </t-select>
                    <t-select
                      :value="item.operator"
                      :disabled="disabled"
                      size="small"
                      placeholder="操作符"
                      :auto-width="false"
                      class="workflow-condition-select-op"
                      @change="updateConditionItem(index, 'operator', String($event))"
                    >
                      <t-option v-for="operator in conditionOperators" :key="operator.value" :value="operator.value" :label="operator.label" />
                    </t-select>
                  </div>
                  <div v-if="item.operator === 'is_empty' || item.operator === 'is_not_empty'" class="workflow-condition-unary-hint">
                    <t-icon name="info-circle" size="13px" />
                    <span>自动判断是否为空，无需比较值</span>
                  </div>
                  <input
                    v-else
                    :value="conditionValue(item.value)"
                    :disabled="disabled"
                    class="workflow-input workflow-input--small"
                    placeholder="目标比较值..."
                    @input="updateConditionItem(index, 'value', inputValue($event))"
                  />
                  <div v-if="edgeSourceDecisionChoices.length && item.operator !== 'is_empty' && item.operator !== 'is_not_empty'" class="workflow-quick-choices">
                    <span class="workflow-quick-choices-title">快捷填入分支标签：</span>
                    <div class="workflow-quick-choice-chips">
                      <button
                        v-for="choice in edgeSourceDecisionChoices"
                        :key="choice"
                        type="button"
                        class="workflow-quick-choice-chip"
                        :class="{ 'workflow-quick-choice-chip--active': conditionValue(item.value) === choice }"
                        :disabled="disabled"
                        :title="`点击填入比较值：${choice}`"
                        @click="updateConditionItem(index, 'value', choice)"
                      >
                        {{ choice }}
                      </button>
                    </div>
                  </div>
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

    <!-- 修订冲突：不提供覆盖操作，只提示刷新，避免用陈旧草稿覆盖他人改动 -->
    <div v-if="revisionConflict !== null" class="workflow-revision-conflict" role="alert">
      <t-icon name="error-circle" />
      <div class="workflow-revision-conflict-body">
        <strong>草稿已被其他更新修改</strong>
        <span>
          服务端当前草稿修订号为
          <code>r{{ revisionConflict || '未知' }}</code>，你的画布基于 <code>r{{ draftRevision }}</code>。
          请重新加载最新内容后再进行发布，不要直接覆盖。
        </span>
      </div>
      <button type="button" class="workflow-toolbar-btn" @click="requestReload">
        <t-icon name="refresh" />
        <span>重新加载</span>
      </button>
    </div>

    <aside
      v-if="debugDrawerOpen"
      class="workflow-debug-drawer"
      :class="{ 'is-minimized': debugMinimized }"
      aria-label="工作流草稿试跑控制台"
    >
      <div class="workflow-debug-drawer__header">
        <div class="workflow-debug-drawer__title-area">
          <div class="workflow-debug-drawer__badge-icon">
            <t-icon :name="debugLoading ? 'loading' : 'terminal'" />
          </div>
          <div class="workflow-debug-drawer__titles">
            <div class="workflow-debug-drawer__headline">
              <strong>草稿试跑控制台</strong>
              <span class="workflow-debug-pill workflow-debug-pill--revision">快照 r{{ draftRevision }}</span>
              <span
                class="workflow-debug-pill"
                :class="debugLoading ? 'workflow-debug-pill--running' : (debugRun ? `workflow-debug-pill--${debugRun.status}` : 'workflow-debug-pill--idle')"
              >
                <span class="workflow-debug-pill__dot" />
                {{ debugLoading ? '执行中…' : (debugRun ? workflowRunStatusLabel(debugRun.status) : '就绪待跑') }}
              </span>
            </div>
            <small class="workflow-debug-drawer__sub">基于当前草稿快照隔离运行，不影响线上正式版本</small>
          </div>
        </div>

        <div class="workflow-debug-drawer__header-actions">
          <button
            v-if="!debugMinimized"
            type="button"
            class="workflow-debug-tool-btn"
            title="快捷填入测试样例问题"
            @click="fillSampleDebugQuery"
          >
            <t-icon name="edit" />
            <span>示例样例</span>
          </button>
          <button
            v-if="debugRun && debugRunTerminal && !debugMinimized"
            type="button"
            class="workflow-debug-tool-btn"
            :disabled="debugLoading"
            title="使用当前参数重新执行一次整次试跑"
            @click="retryDebugRun"
          >
            <t-icon name="refresh" />
            <span>整次重跑</span>
          </button>
          <button
            type="button"
            class="workflow-icon-button"
            :title="debugMinimized ? '展开试跑控制台' : '最小化试跑控制台'"
            @click="debugMinimized = !debugMinimized"
          >
            <t-icon :name="debugMinimized ? 'chevron-up' : 'chevron-down'" />
          </button>
          <button
            type="button"
            class="workflow-icon-button workflow-icon-button--close"
            title="关闭试跑面板"
            @click="closeDebugRun"
          >
            <t-icon name="close" />
          </button>
        </div>
      </div>

      <div v-show="!debugMinimized" class="workflow-debug-drawer__body">
        <!-- 左栏：测试入参配置 -->
        <div class="workflow-debug-left">
          <div class="workflow-debug-section-head">
            <div class="workflow-debug-section-head__title">
              <t-icon name="chat" />
              <span>测试入参配置</span>
            </div>
            <button
              v-if="debugQuery || debugAttachmentsText"
              type="button"
              class="workflow-link-button workflow-debug-clear-btn"
              title="清空当前输入"
              @click="clearDebugInputs"
            >
              清空输入
            </button>
          </div>

          <div class="workflow-debug-form">
            <div class="workflow-debug-field">
              <div class="workflow-debug-field__label">
                <span>用户提问 (Query)</span>
                <span class="workflow-debug-field__required">*必填</span>
              </div>
              <div class="workflow-debug-textarea-wrap">
                <textarea
                  v-model="debugQuery"
                  rows="3"
                  placeholder="输入一条样例问题，试跑当前草稿"
                  :disabled="debugLoading"
                  @keydown.enter.meta.prevent="startDebugRun"
                  @keydown.enter.ctrl.prevent="startDebugRun"
                />
                <span class="workflow-debug-textarea-hint">按 ⌘+Enter 快捷试跑</span>
              </div>
            </div>

            <div class="workflow-debug-field">
              <div class="workflow-debug-field__label">
                <span>附件文本 (Attachments)</span>
                <span class="workflow-debug-field__optional">可选</span>
              </div>
              <textarea
                v-model="debugAttachmentsText"
                rows="2"
                placeholder="粘贴附件解析文本、补充数据等"
                :disabled="debugLoading"
              />
            </div>
          </div>

          <div class="workflow-debug-actions">
            <button
              v-if="debugRun && !debugRunTerminal"
              type="button"
              class="workflow-debug-run-btn workflow-debug-run-btn--danger"
              :disabled="debugLoading"
              @click="cancelDebugRun"
            >
              <t-icon name="stop-circle" />
              <span>停止试跑</span>
            </button>
            <button
              v-else
              type="button"
              class="workflow-debug-run-btn workflow-debug-run-btn--primary"
              :disabled="debugLoading || !debugQuery.trim()"
              @click="startDebugRun"
            >
              <t-icon :name="debugLoading ? 'loading' : 'play-circle'" />
              <span>{{ debugLoading ? '正在执行试跑…' : (debugRun ? '再次试跑' : '开始试跑') }}</span>
            </button>
          </div>
        </div>

        <!-- 右栏：执行监控与结果看板 -->
        <div class="workflow-debug-right">
          <!-- 运行中 / 完成态监控 -->
          <div v-if="debugRun" class="workflow-debug-output-panel">
            <!-- 运行指标摘要 -->
            <div class="workflow-debug-metrics-bar">
              <div class="workflow-debug-metrics-bar__left">
                <span class="workflow-debug-run-id">#{{ debugRun.id ? debugRun.id.slice(-8) : '' }}</span>
                <span class="workflow-debug-badge" :class="`is-${debugRun.status}`">
                  {{ workflowRunStatusLabel(debugRun.status) }}
                </span>
              </div>
              <div class="workflow-debug-metrics-bar__right">
                <span v-if="debugRunDurationMs" class="workflow-debug-metric-item">
                  <t-icon name="time" />
                  <span>耗时 <strong>{{ debugRunDurationMs }}ms</strong></span>
                </span>
                <span v-if="debugRun.nodes?.length" class="workflow-debug-metric-item">
                  <t-icon name="check-circle" />
                  <span>节点 <strong>{{ debugRunSuccessCount }}/{{ debugRun.nodes.length }}</strong></span>
                </span>
              </div>
            </div>

            <!-- 全局报错提示 -->
            <div v-if="debugRun.error_summary" class="workflow-debug-error-banner" role="alert">
              <t-icon name="error-circle-filled" />
              <div class="workflow-debug-error-banner__body">
                <strong>执行遇到错误</strong>
                <p>{{ debugRun.error_summary }}</p>
              </div>
            </div>

            <!-- 最终输出回复 -->
            <div v-if="debugRun.output_summary" class="workflow-debug-final-output">
              <div class="workflow-debug-final-output__head">
                <div class="workflow-debug-final-output__title">
                  <t-icon name="chat" />
                  <span>工作流最终输出</span>
                </div>
                <button
                  type="button"
                  class="workflow-link-button"
                  title="复制最终回复文本"
                  @click="copyDebugOutput(debugRun.output_summary)"
                >
                  <t-icon name="copy" />
                  <span>复制结果</span>
                </button>
              </div>
              <div class="workflow-debug-final-output__content">{{ debugRun.output_summary }}</div>
            </div>

            <!-- 节点执行流水线明细 -->
            <div v-if="debugRun.nodes?.length" class="workflow-debug-timeline">
              <div class="workflow-debug-timeline__title">节点流转轨迹 ({{ debugRun.nodes.length }})</div>
              <div class="workflow-debug-nodes-list">
                <div
                  v-for="(node, nIdx) in debugRun.nodes"
                  :key="node.id"
                  class="workflow-debug-node-card"
                  :class="`is-${node.status}`"
                >
                  <div class="workflow-debug-node-card__header">
                    <div class="workflow-debug-node-card__index">{{ nIdx + 1 }}</div>
                    <div class="workflow-debug-node-card__status-dot" :class="`is-${node.status}`" />
                    <div class="workflow-debug-node-card__meta">
                      <strong class="workflow-debug-node-card__name">{{ node.node_name }}</strong>
                      <span class="workflow-debug-node-card__badge">{{ node.node_type || '节点' }}</span>
                    </div>
                    <div class="workflow-debug-node-card__extra">
                      <span class="workflow-debug-node-card__duration">{{ node.duration_ms || 0 }}ms</span>
                      <button
                        v-if="node.status === 'failed'"
                        type="button"
                        class="workflow-debug-retry-node-btn"
                        :disabled="debugLoading"
                        title="单独重试此失败节点"
                        @click="retryDebugNode(node.id)"
                      >
                        <t-icon name="refresh" />
                        <span>重试</span>
                      </button>
                    </div>
                  </div>

                  <div v-if="node.error_summary" class="workflow-debug-node-card__error">
                    {{ node.error_summary }}
                  </div>
                  <div v-else-if="node.output_summary" class="workflow-debug-node-card__output">
                    {{ node.output_summary }}
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- 未运行时的就绪空状态 -->
          <div v-else class="workflow-debug-empty-state">
            <div class="workflow-debug-empty-state__icon-ring">
              <t-icon name="play-circle" size="28px" />
            </div>
            <h4>工作流调试就绪</h4>
            <p>在左侧输入测试参数并点击「开始试跑」，即可在此实时监控全链路节点流转、分支路由与最终输出。</p>
            <div class="workflow-debug-empty-state__features">
              <div class="workflow-debug-feature-tag">
                <t-icon name="check-circle" />
                <span>快照隔离运行</span>
              </div>
              <div class="workflow-debug-feature-tag">
                <t-icon name="refresh" />
                <span>单节点可重试</span>
              </div>
              <div class="workflow-debug-feature-tag">
                <t-icon name="time" />
                <span>全链路耗时追踪</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </aside>

    <!-- 发布前结构化校验问题：按 node_id/edge_id 一键定位到画布 -->
    <div v-if="publishIssues.length > 0" class="workflow-publish-issues" role="alert">
      <div class="workflow-publish-issues-title">发布前校验未通过（{{ publishIssues.length }}）</div>
      <ul>
        <li v-for="(issue, index) in publishIssues" :key="`${issue.code}-${index}`">
          <button type="button" class="workflow-publish-issue" @click="focusValidationIssue([issue])">
            <code class="workflow-publish-issue-code">{{ issue.code }}</code>
            <span>{{ issue.message }}</span>
            <em v-if="issue.node_id">节点：{{ issue.node_id }}</em>
            <em v-else-if="issue.edge_id">连线：{{ issue.edge_id }}</em>
            <em v-else-if="issue.field_path">{{ issue.field_path }}</em>
          </button>
        </li>
      </ul>
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
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { copyToClipboard } from '@/utils/clipboard';
import { exportWorkflowPackage, parseWorkflowJSON, readJSONFile } from '@/utils/workflowExportImport';
import type {
  CustomAgentConfig,
  WorkflowBranchMode,
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
  WorkflowRun,
  WorkflowValidationIssue,
} from '@/api/agent';
import {
  cancelWorkflowRun,
  getWorkflowRun,
  publishWorkflow,
  previewWorkflowImport,
  retryWorkflowNode,
  retryWorkflowRun,
  startWorkflowDebugRun,
  validateWorkflowDefinition,
} from '@/api/agent';
import '@vue-flow/core/dist/style.css';
import '@vue-flow/core/dist/theme-default.css';
import '@vue-flow/controls/dist/style.css';
import '@vue-flow/minimap/dist/style.css';
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
  /** 分支模式：控制该节点多条出边的执行策略，默认 first_match。 */
  branchMode: WorkflowBranchMode;
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
  /** 当前智能体 ID；未保存的新智能体为空，此时禁用发布。 */
  agentId?: string;
  /** 当前草稿修订号，来自后端 draft_revision；用于展示与发布乐观锁。 */
  draftRevision?: number;
  /** 已发布版本号，来自后端 published_version；0 表示从未发布。 */
  publishedVersion?: number;
  /** 最近一次发布时的草稿修订号；与 draftRevision 比较可判断是否有未发布修改。 */
  publishedDraftRevision?: number;
  /** 父组件是否正在执行发布（含保存草稿）流程。 */
  publishing?: boolean;
  /**
   * 保存当前草稿并返回最新 revision 的回调，由父组件注入（复用现有 updateAgent）。
   * 发布前必须先把画布落库，否则发布会拿到与画布不一致的 revision。
   * 返回 null 表示保存失败，此时中断发布。
   */
  saveDraft?: () => Promise<number | null>;
}>(), {
  modelValue: null,
  catalog: null,
  knowledgeBaseOptions: () => [],
  sandboxConfigId: '',
  disabled: false,
  agentId: '',
  draftRevision: 0,
  publishedVersion: 0,
  publishedDraftRevision: 0,
  publishing: false,
});

const emit = defineEmits<{
  (event: 'update:modelValue', value: WorkflowDefinition): void;
  (event: 'validation-error', message: string): void;
  (event: 'select-sandbox'): void;
  (event: 'manage-skills'): void;
  (event: 'manage-knowledge-bases'): void;
  (event: 'manage-mcp'): void;
  (event: 'run'): void;
  /** 发布成功：父组件刷新草稿 revision / 已发布版本。 */
  (event: 'published', payload: { version: number; draftRevision: number }): void;
  /** 发布前发现未保存修改或修订冲突，父组件负责先保存草稿或重新加载智能体。 */
  (event: 'publish-request', payload: { expectedRevision: number }): void;
  /** 修订冲突后用户选择重新加载：父组件重新拉取智能体详情。 */
  (event: 'reload'): void;
}>();

const onboardingSteps = [
  { title: '选一个模板', description: '最接近你需求的流程，会自动连线。' },
  { title: '补齐待配置项', description: '按右侧提示换成自己的知识库或接口。' },
  { title: '改节点文字', description: '把名称改成同事看得懂的说法。' },
  { title: '校验与试跑', description: '校验通过后，可点击底部“保存并试跑”打开试跑控制台，或点击“保存并关闭”。' },
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

/** 发布流程本地状态：避免按钮在请求期间被重复点击。 */
const publishInFlight = ref(false);
/** 发布前结构化校验发现的问题；按节点/连线定位并展示。 */
const publishIssues = ref<WorkflowValidationIssue[]>([]);
/** 修订冲突时后端回传的当前 revision；非 null 时展示"重新加载"提示。 */
const revisionConflict = ref<number | null>(null);

/** 编辑器内试跑状态；运行数据来自服务端持久化记录，避免只依赖前端内存。 */
const debugDrawerOpen = ref(false);
const debugMinimized = ref(false);
const debugQuery = ref('');
const debugAttachmentsText = ref('');
const debugRun = ref<WorkflowRun | null>(null);
const debugLoading = ref(false);
let debugPollTimer: ReturnType<typeof setInterval> | null = null;

const debugRunTerminal = computed(() => {
  const status = debugRun.value?.status;
  return status === 'succeeded' || status === 'partial' || status === 'failed' || status === 'canceled';
});

const debugRunDurationMs = computed(() => {
  if (!debugRun.value?.nodes?.length) return 0;
  return debugRun.value.nodes.reduce((acc, n) => acc + (n.duration_ms || 0), 0);
});

const debugRunSuccessCount = computed(() => {
  if (!debugRun.value?.nodes?.length) return 0;
  return debugRun.value.nodes.filter(n => n.status === 'succeeded').length;
});

function clearDebugInputs() {
  debugQuery.value = '';
  debugAttachmentsText.value = '';
}

function fillSampleDebugQuery() {
  debugQuery.value = '请帮我梳理当前系统的核心功能特点，并给出两点改进建议。';
}

function copyDebugOutput(text?: string) {
  if (!text) return;
  if (navigator?.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(() => {
      MessagePlugin.success('已复制到剪贴板');
    }).catch(() => {
      MessagePlugin.warning('复制失败，请手动选取');
    });
  } else {
    MessagePlugin.info(text);
  }
}

/** 画布是否有尚未发布的修改：保存时 revision 落后于最近发布记录的 draft_revision。 */
const hasUnpublishedChanges = computed(() => {
  // 从未发布过时，只要有草稿内容（非空白默认流程）就视为待发布。
  if (!props.publishedVersion) return true;
  return props.draftRevision > props.publishedDraftRevision;
});

/** 发布按钮是否可用：需要已保存的智能体、非内置/只读、且不在请求中。 */
const canPublish = computed(() => Boolean(props.agentId) && !props.disabled && !publishInFlight.value && !props.publishing);

const { fitView, screenToFlowCoordinate } = useVueFlow();

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
    version: 2,
    schema_version: 2,
    nodes: [
      { id: 'start', type: 'start', name: '开始', branch_mode: 'first_match', position: { x: 80, y: 160 }, config: {} },
      { id: 'end', type: 'end', name: '结束', branch_mode: 'first_match', position: { x: 420, y: 160 }, config: { text_template: '{{input.query}}' } },
    ],
    edges: [{ id: 'start-end', source: 'start', target: 'end', order: 0, is_default: false }],
    viewport: { x: 0, y: 0, zoom: 1 },
  };
}

function normalizeDefinition(value?: WorkflowDefinition | null): WorkflowDefinition {
  const definition = value && value.nodes?.length ? clone(value) : defaultDefinition();
  const schemaVersion = Number(definition.schema_version || definition.version || 1);
  const isLegacySchema = !Number.isFinite(schemaVersion) || schemaVersion < 2;
  const outgoingCount = new Map<string, number>();
  for (const edge of definition.edges || []) {
    outgoingCount.set(edge.source, (outgoingCount.get(edge.source) || 0) + 1);
  }
  const orderBySource = new Map<string, number>();
  definition.version = 2;
  definition.schema_version = 2;
  definition.nodes = (definition.nodes || []).map((node) => ({
    ...node,
    name: node.name || node.id,
    position: { x: Number(node.position?.x) || 0, y: Number(node.position?.y) || 0 },
    branch_mode: node.branch_mode === 'all_match' || node.branch_mode === 'first_match'
      ? node.branch_mode
      : isLegacySchema && (outgoingCount.get(node.id) || 0) > 1
        ? 'all_match'
        : 'first_match',
    config: node.config && typeof node.config === 'object' ? node.config : defaultConfig(node.type),
  }));
  definition.edges = (definition.edges || []).map((edge, index) => {
    const sourceOrder = orderBySource.get(edge.source) || 0;
    const hasExplicitOrder = edge.order !== undefined && edge.order !== null && Number.isFinite(Number(edge.order));
    const order = isLegacySchema || !hasExplicitOrder ? sourceOrder : Number(edge.order);
    orderBySource.set(edge.source, sourceOrder + 1);
    return {
      ...edge,
      id: edge.id || `edge-${index + 1}`,
      order,
      is_default: !!edge.is_default,
    };
  });
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

/**
 * 获取指定节点的展示名称（若找不到节点则回退至节点 ID）。
 */
function getNodeName(nodeId?: string): string {
  if (!nodeId) return '';
  const node = flowNodes.value.find((n) => n.id === nodeId);
  return node?.data?.name || nodeId;
}

/**
 * 智能计算连线在画布上展示的文字标签。
 * - 默认分支：展示“默认”；
 * - 决策分支（LLM decision）：优先展示匹配的候选分支标签；
 * - 单条件分支：若已填写目标值展示如“query = 苹果”，若单目运算符展示“为空/非空”，未填值展示“待设条件”；
 * - 多条件分支：展示“N项条件 (AND/OR)”；
 * - 新建未设分支：展示“条件分支”。
 */
function computeEdgeLabel(edge: {
  is_default?: boolean;
  data?: { is_default?: boolean; condition?: WorkflowCondition; order?: number };
  condition?: WorkflowCondition;
  source?: string;
}): string {
  const isDefault = edge.is_default ?? edge.data?.is_default;
  if (isDefault) return '默认';

  const condition = edge.condition || edge.data?.condition;
  if (!condition || !Array.isArray(condition.items) || condition.items.length === 0) {
    return '条件分支';
  }

  const items = condition.items;
  if (items.length === 1) {
    const item = items[0];
    const op = item.operator;
    const opLabel = conditionOperatorLabels[op as keyof typeof conditionOperatorLabels] || op || '等于';

    if (op === 'is_empty') return '为空';
    if (op === 'is_not_empty') return '非空';

    const rawVal = item.value;
    const valStr = (rawVal == null ? '' : typeof rawVal === 'string' ? rawVal : JSON.stringify(rawVal)).trim();
    if (!valStr) {
      return '待设条件';
    }

    const sourceNode = flowNodes.value.find((n) => n.id === edge.source);
    if (sourceNode?.data?.workflowType === 'llm-decision' && (item.variable.endsWith('.choice') || item.variable === 'choice')) {
      return valStr.length > 12 ? `${valStr.slice(0, 12)}...` : valStr;
    }

    let varName = item.variable;
    if (varName.startsWith('nodes.')) {
      const parts = varName.split('.');
      const nId = parts[1];
      const n = flowNodes.value.find((x) => x.id === nId);
      const field = parts.slice(2).join('.');
      varName = n ? `${n.data.name}.${field}` : field;
    } else if (varName.startsWith('input.')) {
      varName = varName.replace('input.', '');
    }

    let opSymbol = opLabel;
    if (op === 'eq') opSymbol = '=';
    else if (op === 'neq') opSymbol = '≠';
    else if (op === 'gt') opSymbol = '>';
    else if (op === 'gte') opSymbol = '≥';
    else if (op === 'lt') opSymbol = '<';
    else if (op === 'lte') opSymbol = '≤';

    const brief = `${varName} ${opSymbol} ${valStr}`;
    return brief.length > 16 ? `${brief.slice(0, 16)}...` : brief;
  }

  return `${items.length}项条件 (${condition.mode === 'any' ? 'OR' : 'AND'})`;
}

/** 刷新所有连线的展示标签（例如当上游节点改名或规则变更时） */
function refreshAllEdgeLabels() {
  for (const edge of flowEdges.value) {
    edge.label = computeEdgeLabel(edge);
  }
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
    class: nodeClass(node.type),
    data: { workflowType: node.type, name: node.name, config: clone(node.config || defaultConfig(node.type)), branchMode: node.branch_mode === 'all_match' ? 'all_match' : 'first_match' },
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
    label: computeEdgeLabel(edge),
    data: {
      order: edge.order,
      is_default: !!edge.is_default,
      condition: edge.condition ? clone(edge.condition) : undefined,
    },
    deletable: !props.disabled,
  }));
  selectedNodeId.value = '';
  selectedEdgeId.value = '';
  if (snapshotDebounceTimer) {
    clearTimeout(snapshotDebounceTimer);
    snapshotDebounceTimer = null;
  }
  undoStack.value = [];
  redoStack.value = [];
  lastEmitted = JSON.stringify(toDefinition());
  nextTick(() => {
    applyingModel.value = false;
  });
}

function toDefinition(): WorkflowDefinition {
  return {
    version: 2,
    schema_version: 2,
    nodes: flowNodes.value.map((node): WorkflowNode => ({
      id: node.id,
      type: node.data.workflowType,
      name: node.data.name,
      branch_mode: node.data.branchMode === 'all_match' ? 'all_match' : 'first_match',
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
    pushSnapshotDebounced(800);
  }
}

/** 捕获当前画布的序列化快照 */
function captureCurrentSnapshot(): string {
  return JSON.stringify({
    nodes: flowNodes.value.map((node) => ({
      id: node.id,
      type: node.data.workflowType,
      name: node.data.name,
      branch_mode: node.data.branchMode === 'all_match' ? 'all_match' : 'first_match',
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
  if (snapshotDebounceTimer) {
    clearTimeout(snapshotDebounceTimer);
    snapshotDebounceTimer = null;
  }
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
function pushSnapshotDebounced(delay = 800) {
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
    isHistoryApplying.value = true;
    if (snapshotDebounceTimer) {
      clearTimeout(snapshotDebounceTimer);
      snapshotDebounceTimer = null;
    }
    const snapshot = JSON.parse(snapshotStr);
    flowNodes.value = (snapshot.nodes || []).map((node: any): EditorNode => ({
      id: node.id,
      type: 'default',
      label: node.name,
      position: { x: node.position.x, y: node.position.y },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      class: nodeClass(node.type),
      data: { workflowType: node.type, name: node.name, config: clone(node.config || defaultConfig(node.type)), branchMode: node.branch_mode === 'all_match' ? 'all_match' : 'first_match' },
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
      label: computeEdgeLabel(edge),
      data: {
        order: edge.order,
        is_default: !!edge.is_default,
        condition: edge.condition ? clone(edge.condition) : undefined,
      },
      deletable: !props.disabled,
    }));
    clearSelection();
  } finally {
    const next = toDefinition();
    lastEmitted = JSON.stringify(next);
    emit('update:modelValue', next);
    nextTick(() => {
      isHistoryApplying.value = false;
    });
  }
}

/** 执行撤回 */
function undo() {
  if (!canUndo.value || props.disabled) return;
  if (snapshotDebounceTimer) {
    clearTimeout(snapshotDebounceTimer);
    snapshotDebounceTimer = null;
  }

  const current = captureCurrentSnapshot();

  // 关键：持续出栈与当前状态相同的快照，直到定位到最近一个真正有变化的历史状态
  let previous: string | undefined;
  while (undoStack.value.length > 0) {
    const candidate = undoStack.value.pop();
    if (candidate && candidate !== current) {
      previous = candidate;
      break;
    }
  }

  if (previous) {
    redoStack.value.push(current);
    restoreSnapshot(previous);
    showHint('已撤回上一操作');
  }
}

/** 执行重做 */
function redo() {
  if (!canRedo.value || props.disabled) return;
  if (snapshotDebounceTimer) {
    clearTimeout(snapshotDebounceTimer);
    snapshotDebounceTimer = null;
  }

  const current = captureCurrentSnapshot();

  // 关键：持续出栈与当前状态相同的快照，直到定位到真正有变化的状态
  let next: string | undefined;
  while (redoStack.value.length > 0) {
    const candidate = redoStack.value.pop();
    if (candidate && candidate !== current) {
      next = candidate;
      break;
    }
  }

  if (next) {
    undoStack.value.push(current);
    restoreSnapshot(next);
    showHint('已重做操作');
  }
}

/** 节点拖拽移动开始：记录拖拽前的位置快照 */
function onNodeDragStart() {
  if (props.disabled) return;
  pushSnapshot();
}

/** 节点拖拽移动停止时通知外部保存 */
function onNodeDragStop() {
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
  stopDebugPolling();
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
const BUILTIN_TOOL_METADATA: Record<string, { label: string; desc: string }> = {
  web_search: { label: '网络搜索', desc: '在互联网上搜索最新公开信息，适合补充实时数据' },
  web_fetch: { label: '网页抓取', desc: '抓取并提取指定网页的正文文本内容' },
  database_query: { label: '数据库查询', desc: '在配置的关联数据库中执行只读 SQL 查询并返回表格数据' },
  data_analysis: { label: '数据分析', desc: '统计和分析表格、CSV或结构化数据' },
  data_schema: { label: '数据结构元信息', desc: '查看数据库或数据表的元结构与字段信息' },
  wiki_search: { label: 'Wiki 搜索', desc: '在 Wiki 知识库中按关键词与语义检索页面' },
  wiki_read_page: { label: 'Wiki 页面阅读', desc: '读取指定 Wiki 页面的完整内容' },
  wiki_read_source_doc: { label: '精读源文档', desc: '深入阅读 Wiki 页面背后的原始源文档' },
  wiki_read_issue: { label: '查看 Wiki 问题', desc: '查看特定 Wiki 页面上标记的事实或冲突问题' },
  wiki_flag_issue: { label: '标记 Wiki 问题', desc: '标记页面中存在的事实错误或合并冲突问题' },
  wiki_write_page: { label: '创建/覆盖 Wiki', desc: '创建新页面或完全覆盖已有 Wiki 页面' },
  wiki_replace_text: { label: '局部替换 Wiki', desc: '替换 Wiki 页面中的特定文本' },
  wiki_rename_page: { label: '重命名 Wiki', desc: '重命名 Wiki 页面并自动更新关联链接' },
  wiki_delete_page: { label: '删除 Wiki', desc: '删除 Wiki 页面并自动清理关联死链' },
  wiki_update_issue: { label: '更新 Wiki 问题', desc: '更新 Wiki 页面问题的处理状态' },
  search_conversations: { label: '搜索历史会话', desc: '检索过去的对话记录与用户提问' },
  search_memory: { label: '检索长期记忆', desc: '查找当前用户的长期记忆与个人偏好' },
  grep_chunks: { label: '关键词搜索', desc: '在知识库切片中进行精准全文匹配' },
  knowledge_search: { label: '知识库语义检索', desc: '基于向量语义在知识库中匹配相关片段' },
  list_knowledge_chunks: { label: '查看知识切片', desc: '按序浏览或检索知识文档切片清单' },
  query_knowledge_graph: { label: '查询知识图谱', desc: '查询知识库构建的实体与关系图谱' },
  get_document_info: { label: '获取文档信息', desc: '查看知识库中原始文档的元数据' },
  todo_write: { label: '计划管理', desc: '维护多步任务的待办清单与完成状态' },
  thinking: { label: '深度思考', desc: '输出推理与分析过程' },
};

function getBuiltinToolLabel(name?: string): string {
  if (!name) return '未配置工具';
  const fromCatalog = builtinTools.value.find((t) => t.name === name);
  if (fromCatalog?.display_name && fromCatalog.display_name !== fromCatalog.name) {
    return fromCatalog.display_name;
  }
  return BUILTIN_TOOL_METADATA[name]?.label || name;
}

const formattedBuiltinTools = computed(() => {
  return builtinTools.value.map((tool) => {
    const meta = BUILTIN_TOOL_METADATA[tool.name];
    const friendly = (tool.display_name && tool.display_name !== tool.name)
      ? tool.display_name
      : (meta?.label || tool.name);
    const label = friendly !== tool.name ? `${friendly} (${tool.name})` : tool.name;
    return {
      name: tool.name,
      label,
      friendly,
      description: tool.description || meta?.desc || '',
    };
  });
});

const selectedBuiltinTool = computed<WorkflowCatalogTool | undefined>(() =>
  builtinTools.value.find((tool) => tool.name === configString('tool_name')),
);

const selectedBuiltinToolHelp = computed(() => {
  if (!selectedBuiltinTool.value) return '';
  const meta = BUILTIN_TOOL_METADATA[selectedBuiltinTool.value.name];
  return selectedBuiltinTool.value.description || meta?.desc || '';
});

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

/** 节点输出字段元数据定义，严格对应后端 types.WorkflowNodeOutput 结构 */
interface WorkflowOutputFieldMeta {
  key: string;              // 简短字段名，如 'text', 'count', 'choice'
  fullPath: string;         // 变量路径，如 'nodes.retrieval-1.data.count' 或 'input.query'
  templateSyntax: string;   // 模板插入表达式，如 '{{nodes.retrieval-1.data.count}}'
  label: string;            // 人性化中文标签
  type: string;             // 数据类型：string | number | boolean | object
  desc: string;             // 字段详细业务含义及引用规范
  isPrimary?: boolean;      // 是否为该节点的主文本输出
}

/** 快捷插入变量选项 */
interface QuickVariableOption {
  label: string;
  value: string;
  hint: string;
  type: string;
  isPrimary?: boolean;
}

/**
 * 获取指定节点的全部标准输出字段清单。
 * 字段路径与后端 runtime.go 的 workflowTemplateRE 与 nodeVariableRE 完全一致。
 */
function getNodeOutputFields(node: EditorNode | WorkflowNode | { id: string; data?: { workflowType?: string; name?: string }; type?: string; name?: string }): WorkflowOutputFieldMeta[] {
  const nodeId = node.id;
  const workflowType = (('data' in node && node.data?.workflowType) || ('type' in node && node.type) || '') as WorkflowNodeType;

  if (workflowType === 'start') {
    return [
      {
        key: 'query',
        fullPath: 'input.query',
        templateSyntax: '{{input.query}}',
        label: '用户提问文本',
        type: 'string',
        desc: '当前对话轮次用户输入的原始问题文本',
        isPrimary: true,
      },
      {
        key: 'attachments_text',
        fullPath: 'input.attachments_text',
        templateSyntax: '{{input.attachments_text}}',
        label: '附件提取文本',
        type: 'string',
        desc: '用户上传的文档或附件解析出的全文文本',
      },
    ];
  }

  if (workflowType === 'knowledge-retrieval') {
    return [
      {
        key: 'text',
        fullPath: `nodes.${nodeId}.text`,
        templateSyntax: `{{nodes.${nodeId}.text}}`,
        label: '检索片段文本',
        type: 'string',
        desc: '知识库中召回并合并后的文档切片内容，供大模型作为参考上下文',
        isPrimary: true,
      },
      {
        key: 'count',
        fullPath: `nodes.${nodeId}.data.count`,
        templateSyntax: `{{nodes.${nodeId}.data.count}}`,
        label: '切片命中数',
        type: 'number',
        desc: '实际召回命中的知识切片总数量，可用于连线分支判断 count > 0',
      },
      {
        key: 'status',
        fullPath: `nodes.${nodeId}.status`,
        templateSyntax: `{{nodes.${nodeId}.status}}`,
        label: '执行状态',
        type: 'string',
        desc: '节点运行状态："success" 或 "failed"',
      },
    ];
  }

  if (workflowType === 'llm') {
    return [
      {
        key: 'text',
        fullPath: `nodes.${nodeId}.text`,
        templateSyntax: `{{nodes.${nodeId}.text}}`,
        label: '模型生成文本',
        type: 'string',
        desc: '大模型推理后生成的完整回答文本，可供下游步骤或最终输出引用',
        isPrimary: true,
      },
      {
        key: 'reasoning_content',
        fullPath: `nodes.${nodeId}.data.reasoning_content`,
        templateSyntax: `{{nodes.${nodeId}.data.reasoning_content}}`,
        label: '思维链推理过程',
        type: 'string',
        desc: '深度思考模型（如 DeepSeek-R1）输出的思考过程文本',
      },
      {
        key: 'status',
        fullPath: `nodes.${nodeId}.status`,
        templateSyntax: `{{nodes.${nodeId}.status}}`,
        label: '执行状态',
        type: 'string',
        desc: '节点运行状态："success" 或 "failed"',
      },
    ];
  }

  if (workflowType === 'llm-decision') {
    return [
      {
        key: 'choice',
        fullPath: `nodes.${nodeId}.data.choice`,
        templateSyntax: `{{nodes.${nodeId}.data.choice}}`,
        label: '分支决策标签',
        type: 'string',
        desc: '大模型意图判定命中的分支标签名，用于边条件的分支路由匹配',
        isPrimary: true,
      },
      {
        key: 'text',
        fullPath: `nodes.${nodeId}.text`,
        templateSyntax: `{{nodes.${nodeId}.text}}`,
        label: '决策输出文本',
        type: 'string',
        desc: '同 data.choice，大模型选中的分支标签名',
      },
      {
        key: 'reason',
        fullPath: `nodes.${nodeId}.data.reason`,
        templateSyntax: `{{nodes.${nodeId}.data.reason}}`,
        label: '判定简要理由',
        type: 'string',
        desc: '大模型做出该分支判定的简要理由分析',
      },
      {
        key: 'status',
        fullPath: `nodes.${nodeId}.status`,
        templateSyntax: `{{nodes.${nodeId}.status}}`,
        label: '执行状态',
        type: 'string',
        desc: '节点运行状态："success" 或 "failed"',
      },
    ];
  }

  if (workflowType === 'http-request') {
    return [
      {
        key: 'text',
        fullPath: `nodes.${nodeId}.text`,
        templateSyntax: `{{nodes.${nodeId}.text}}`,
        label: '响应正文文本',
        type: 'string',
        desc: 'HTTP 接口返回的原始文本内容（同 data.body）',
        isPrimary: true,
      },
      {
        key: 'status_code',
        fullPath: `nodes.${nodeId}.data.status_code`,
        templateSyntax: `{{nodes.${nodeId}.data.status_code}}`,
        label: 'HTTP 状态码',
        type: 'number',
        desc: '接口返回的状态码（如 200, 404, 500），常用于分支条件路由',
      },
      {
        key: 'body',
        fullPath: `nodes.${nodeId}.data.body`,
        templateSyntax: `{{nodes.${nodeId}.data.body}}`,
        label: '原始响应体 (body)',
        type: 'string',
        desc: '接口返回的原始字符串响应体',
      },
      {
        key: 'json',
        fullPath: `nodes.${nodeId}.data.json`,
        templateSyntax: `{{nodes.${nodeId}.data.json}}`,
        label: '解析后 JSON 对象',
        type: 'object',
        desc: '若接口返回 JSON，下游可通过 nodes.<节点ID>.data.json.<属性> 读取子字段',
      },
      {
        key: 'status',
        fullPath: `nodes.${nodeId}.status`,
        templateSyntax: `{{nodes.${nodeId}.status}}`,
        label: '请求执行状态',
        type: 'string',
        desc: 'HTTP 状态码处于 2xx 时为 "success"，其余为 "failed"',
      },
    ];
  }

  if (workflowType === 'tool') {
    const config = ('data' in node ? node.data?.config : {}) || {};
    const toolName = (config.tool_name as string) || '';
    const friendlyTool = config.kind === 'builtin'
      ? getBuiltinToolLabel(toolName)
      : (config.kind === 'mcp' ? (toolName || 'MCP') : ((config.skill_name as string) || '沙箱技能'));
    const suffix = friendlyTool && friendlyTool !== '未配置工具' ? ` · ${friendlyTool}` : '';
    return [
      {
        key: 'text',
        fullPath: `nodes.${nodeId}.text`,
        templateSyntax: `{{nodes.${nodeId}.text}}`,
        label: `工具输出结果${suffix}`,
        type: 'string',
        desc: `${friendlyTool || '工具'}执行完毕后返回的文本输出，可供下游步骤或最终输出引用`,
        isPrimary: true,
      },
      {
        key: 'status',
        fullPath: `nodes.${nodeId}.status`,
        templateSyntax: `{{nodes.${nodeId}.status}}`,
        label: `工具执行状态${suffix}`,
        type: 'string',
        desc: '工具调用成功为 "success"，发生异常为 "failed"',
      },
    ];
  }

  if (workflowType === 'end') {
    return [
      {
        key: 'text',
        fullPath: `nodes.${nodeId}.text`,
        templateSyntax: `{{nodes.${nodeId}.text}}`,
        label: '最终合成回复',
        type: 'string',
        desc: '工作流最终渲染后交付给用户的完整回复内容',
        isPrimary: true,
      },
    ];
  }

  return [];
}

/** 当前选中节点对外开放的全部标准输出字段 */
const selectedNodeOutputFields = computed<WorkflowOutputFieldMeta[]>(() => {
  if (!selectedNode.value) return [];
  return getNodeOutputFields(selectedNode.value);
});

/** 当前选中节点的主要下游引用模板表达式 */
const primaryVariableForSelectedNode = computed(() => {
  if (!selectedNode.value) return '';
  if (selectedNode.value.data.workflowType === 'start') {
    return '{{input.query}}';
  }
  if (selectedNode.value.data.workflowType === 'llm-decision') {
    return `{{nodes.${selectedNode.value.id}.data.choice}}`;
  }
  return `{{nodes.${selectedNode.value.id}.text}}`;
});

function skillOptionLabel(skill: WorkflowCatalogSkill): string {
  const versionedName = skill.version ? `${skill.name} · v${skill.version}` : skill.name;
  return skill.description ? `${versionedName} — ${skill.description}` : versionedName;
}

/**
 * 沿入边回溯，得到某个节点的全部上游祖先节点（从最早执行的入口到紧邻的上一步）。
 *
 * 后端只允许当前执行分支上游的变量参与渲染，所以必须按真实路径递归回溯。
 *
 * @param nodeId 起始节点 ID。
 * @returns 祖先节点列表（按拓扑升序排列，即起始步骤在前，最近步骤在后）。
 */
function allAncestorsFor(nodeId: string): EditorNode[] {
  const incomingByTarget = new Map<string, string>();
  for (const edge of flowEdges.value) {
    if (!incomingByTarget.has(edge.target)) incomingByTarget.set(edge.target, edge.source);
  }
  const result: EditorNode[] = [];
  const seen = new Set<string>([nodeId]);
  let current = incomingByTarget.get(nodeId);
  while (current && !seen.has(current)) {
    seen.add(current);
    const node = flowNodes.value.find((item) => item.id === current);
    if (!node) break;
    result.unshift(node);
    current = incomingByTarget.get(current);
  }
  return result;
}

/** 向上回溯上游处理节点（排除 start 节点），供历史兼容调用 */
function upstreamNodesFor(nodeId: string): EditorNode[] {
  return allAncestorsFor(nodeId).filter((n) => n.data.workflowType !== 'start');
}

/** 当前选中节点可引用的快捷上游变量药丸 */
const quickUpstreamVariableOptions = computed<QuickVariableOption[]>(() => {
  if (!selectedNode.value) return [];
  const ancestors = allAncestorsFor(selectedNode.value.id);
  const options: QuickVariableOption[] = [
    {
      label: '用户提问',
      value: '{{input.query}}',
      hint: '用户输入的原始问题 (input.query)',
      type: 'string',
      isPrimary: true,
    },
    {
      label: '附件文本',
      value: '{{input.attachments_text}}',
      hint: '附件解析全文 (input.attachments_text)',
      type: 'string',
    },
  ];

  for (const node of ancestors) {
    if (node.data.workflowType === 'start') continue;
    const nodeName = node.data.name || node.id;
    const fields = getNodeOutputFields(node);
    // 优先加入主文本输出
    const primary = fields.find((f) => f.isPrimary) || fields[0];
    if (primary) {
      options.push({
        label: `${nodeName} · ${primary.label}`,
        value: primary.templateSyntax,
        hint: `[${nodeTypeLabel(node.data.workflowType)}] ${primary.desc} (${primary.fullPath})`,
        type: primary.type,
        isPrimary: true,
      });
    }
    // 特殊节点的关键副字段也加入快捷选项
    if (node.data.workflowType === 'knowledge-retrieval') {
      const countField = fields.find((f) => f.key === 'count');
      if (countField) {
        options.push({
          label: `${nodeName} · 命中数`,
          value: countField.templateSyntax,
          hint: `${countField.desc} (${countField.fullPath})`,
          type: countField.type,
        });
      }
    } else if (node.data.workflowType === 'llm-decision') {
      const choiceField = fields.find((f) => f.key === 'choice');
      if (choiceField && primary?.key !== 'choice') {
        options.push({
          label: `${nodeName} · 决策标签`,
          value: choiceField.templateSyntax,
          hint: `${choiceField.desc} (${choiceField.fullPath})`,
          type: choiceField.type,
        });
      }
    } else if (node.data.workflowType === 'http-request') {
      const statusField = fields.find((f) => f.key === 'status_code');
      if (statusField) {
        options.push({
          label: `${nodeName} · 状态码`,
          value: statusField.templateSyntax,
          hint: `${statusField.desc} (${statusField.fullPath})`,
          type: statusField.type,
        });
      }
    }
  }

  return options;
});

/** 结束节点可引用的上游输出，用节点名称展示 */
const upstreamNodeOptions = computed(() => {
  if (selectedNode.value?.data.workflowType !== 'end') return [];
  return quickUpstreamVariableOptions.value;
});

/** 结束节点默认取紧邻的上一步输出，而不是让用户面对空占位符 */
const endTemplatePlaceholder = computed(() => {
  const list = quickUpstreamVariableOptions.value;
  // 倒序找最后一个非全局输入的主输出
  const lastStep = [...list].reverse().find((item) => item.value !== '{{input.query}}' && item.value !== '{{input.attachments_text}}');
  return lastStep?.value || '{{input.query}}';
});

/**
 * 字段旁的说明文案里需要出现 {{...}} 字面量，在脚本中拼好
 */
const urlFieldHint = '必须带 http:// 或 https://；变量只能写在路径或参数里，例如 https://example.com/api/{{nodes.retrieval-1.text}}';
const toolArgumentHint = '按工具的入参写一个 JSON 对象；可用 {{input.query}} 引用用户提问。';

/**
 * 把变量占位符追加到某个文本模板字段中。
 * 若字段非空且为多行字段，则换行追加；单行字段则无缝拼接。
 *
 * @param key 节点配置字段名。
 * @param variable 形如 {{nodes.x.text}} 的占位符。
 */
function appendTemplateVariable(key: string, variable: string) {
  if (props.disabled) return;
  const current = configString(key);
  if (!current) {
    updateConfig(key, variable);
    return;
  }
  if (key === 'url') {
    updateConfig(key, `${current}/${variable}`);
    return;
  }
  updateConfig(key, `${current}\n${variable}`);
}

/**
 * 复制变量到剪贴板，并给出明确提示。
 *
 * @param text 待复制的文本（支持模板变量或判断路径）。
 * @param successMessage 成功提示文案。
 */
async function copyVariableText(text: string, successMessage?: string) {
  const ok = await copyToClipboard(text);
  if (ok) {
    MessagePlugin.success(successMessage || `已复制 ${text}`);
  }
}

/**
 * 复制选中节点的主引用变量。
 */
async function copyNodeVariable(nodeId: string) {
  const node = flowNodes.value.find((n) => n.id === nodeId);
  let text = `{{nodes.${nodeId}.text}}`;
  if (node?.data.workflowType === 'start') {
    text = '{{input.query}}';
  } else if (node?.data.workflowType === 'llm-decision') {
    text = `{{nodes.${nodeId}.data.choice}}`;
  }
  await copyVariableText(text, `已复制引用变量 ${text}，可直接粘贴到提示词或模板中`);
}

/**
 * 针对当前选中的连线，严格计算其源节点及所有上游祖先节点的合法变量。
 * 与后端 validation.go 中 ancestorSet(edge.Source, incoming) 逻辑 100% 对齐。
 */
const currentEdgeVariableOptions = computed<Array<{ label: string; value: string; hint?: string }>>(() => {
  if (!selectedEdge.value) return [];
  const sourceNode = flowNodes.value.find((n) => n.id === selectedEdge.value?.source);
  if (!sourceNode) return [];

  const options: Array<{ label: string; value: string; hint?: string }> = [
    { label: '用户提问 (input.query)', value: 'input.query', hint: '字符串：用户提问内容' },
    { label: '附件提取文本 (input.attachments_text)', value: 'input.attachments_text', hint: '字符串：附件提取文本' },
  ];

  // 源节点自身 + 源节点的所有上游祖先
  const validNodes: EditorNode[] = [];
  if (sourceNode.data.workflowType !== 'start') {
    validNodes.push(sourceNode);
  }
  const ancestors = allAncestorsFor(sourceNode.id).filter((n) => n.data.workflowType !== 'start');
  for (const anc of ancestors) {
    if (!validNodes.some((n) => n.id === anc.id)) {
      validNodes.push(anc);
    }
  }

  for (const node of validNodes) {
    const nodeName = node.data.name || node.id;
    const fields = getNodeOutputFields(node);
    for (const field of fields) {
      options.push({
        label: `[${nodeName}] ${field.label} · ${field.fullPath}`,
        value: field.fullPath,
        hint: `${field.type}：${field.desc}`,
      });
    }
  }

  return options;
});

/** 兼容旧逻辑的变量列表引用 */
const variableOptions = computed(() => {
  return currentEdgeVariableOptions.value.map((opt) => opt.value);
});

/** 连线源节点为分支判断时的候选标签 */
const edgeSourceDecisionChoices = computed<string[]>(() => {
  if (!selectedEdge.value) return [];
  const sourceNode = flowNodes.value.find((n) => n.id === selectedEdge.value?.source);
  if (sourceNode?.data.workflowType !== 'llm-decision') return [];
  const rawChoices = sourceNode.data.config?.choices;
  return Array.isArray(rawChoices) ? rawChoices.map(String).filter(Boolean) : [];
});

/** 连线新建判断条件时的默认变量 */
const defaultConditionVariable = computed(() => {
  if (!selectedEdge.value) return 'input.query';
  const sourceNode = flowNodes.value.find((n) => n.id === selectedEdge.value?.source);
  if (!sourceNode) return 'input.query';

  if (sourceNode.data.workflowType === 'llm-decision') {
    return `nodes.${sourceNode.id}.data.choice`;
  }
  if (sourceNode.data.workflowType === 'knowledge-retrieval') {
    return `nodes.${sourceNode.id}.data.count`;
  }
  if (sourceNode.data.workflowType === 'http-request') {
    return `nodes.${sourceNode.id}.data.status_code`;
  }
  if (sourceNode.data.workflowType !== 'start') {
    return `nodes.${sourceNode.id}.text`;
  }
  return 'input.query';
});

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
  refreshAllEdgeLabels();
}

/**
 * 修改选中节点的分支执行策略。
 * 语义差异：first_match=按出边稳定顺序只走第一个条件命中的分支（互斥路由）；
 * all_match=所有条件命中的分支并行执行（扇出后由各自终点汇聚）。
 *
 * @param mode 目标分支模式。
 */
function updateNodeBranchMode(mode: WorkflowBranchMode) {
  updateSelectedNode((node) => { node.data.branchMode = mode; });
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

function updateSelectedEdge(mutator: (edge: EditorEdge) => void, recordSnapshot = true) {
  if (props.disabled || !selectedEdge.value) return;
  if (recordSnapshot) {
    pushSnapshot();
  }
  const edge = selectedEdge.value;
  mutator(edge);
  edge.label = computeEdgeLabel(edge);
  emitDefinition();
}

function updateEdgeDefault(value: boolean) {
  updateSelectedEdge((edge) => {
    edge.data = { ...(edge.data || { order: 0 }), is_default: value };
    if (value) edge.data.condition = undefined;
  }, true);
}

function updateEdgeConditionMode(mode: string) {
  updateSelectedEdge((edge) => {
    edge.data = {
      ...(edge.data || { order: 0, is_default: false }),
      condition: { mode: mode === 'any' ? 'any' : 'all', items: clone(edge.data?.condition?.items || [{ variable: defaultConditionVariable.value, operator: 'eq', value: '' }]) },
    };
  }, true);
}

function updateConditionItem(index: number, key: keyof WorkflowConditionItem, value: unknown) {
  if (key === 'value') {
    pushSnapshotDebounced();
    updateSelectedEdge((edge) => {
      const condition = edge.data?.condition || { mode: 'all', items: [] };
      const items = [...condition.items];
      const nextValue = typeof value === 'string' ? parseConditionValue(value) : value;
      items[index] = { ...items[index], [key]: nextValue };
      edge.data = { ...(edge.data || { order: 0, is_default: false }), condition: { ...condition, items } };
    }, false);
  } else {
    updateSelectedEdge((edge) => {
      const condition = edge.data?.condition || { mode: 'all', items: [] };
      const items = [...condition.items];
      items[index] = { ...items[index], [key]: value };
      if (key === 'operator' && (value === 'is_empty' || value === 'is_not_empty')) {
        items[index].value = '';
      }
      edge.data = { ...(edge.data || { order: 0, is_default: false }), condition: { ...condition, items } };
    }, true);
  }
}

function addConditionItem() {
  updateSelectedEdge((edge) => {
    const condition = edge.data?.condition || { mode: 'all', items: [] };
    edge.data = {
      ...(edge.data || { order: 0, is_default: false }),
      condition: {
        ...condition,
        items: [...condition.items, { variable: defaultConditionVariable.value, operator: 'eq', value: '' }],
      },
    };
  }, true);
}

function removeConditionItem(index: number) {
  updateSelectedEdge((edge) => {
    const condition = edge.data?.condition;
    if (!condition) return;
    const items = condition.items.filter((_item: WorkflowConditionItem, itemIndex: number) => itemIndex !== index);
    edge.data = { ...(edge.data || { order: 0, is_default: false }), condition: items.length ? { ...condition, items } : undefined };
  }, true);
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
      if (kind === 'builtin') {
        const toolName = (data.config.tool_name as string) || '';
        if (!toolName) return '未选内置工具';
        return `内置: ${getBuiltinToolLabel(toolName)}`;
      }
      if (kind === 'mcp') {
        const toolName = (data.config.tool_name as string) || '';
        const serviceId = (data.config.service_id as string) || '';
        const service = mcpServices.value.find((s) => s.id === serviceId);
        const tool = service?.tools.find((t) => t.name === toolName);
        const toolLabel = tool?.display_name || toolName || '未选工具';
        return `MCP: ${service?.name ? `${service.name} · ` : ''}${toolLabel}`;
      }
      const skillName = (data.config.skill_name as string) || '';
      if (!skillName) return '未选沙箱技能';
      const skill = skills.value.find((s) => s.name === skillName);
      return `技能: ${skill?.name || skillName}`;
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
  // 基于当前画布视口变换，确保直接点击添加时节点呈现在可见视野中
  const zoom = viewport.value.zoom || 1;
  const vx = viewport.value.x || 0;
  const vy = viewport.value.y || 0;
  const centerX = (-vx + 450) / zoom;
  const centerY = (-vy + 260) / zoom;
  const offset = (index % 6) * 36;
  return {
    x: Math.round(centerX - 105 + offset),
    y: Math.round(centerY - 35 + offset),
  };
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
    data: { workflowType: type, name, config: defaultConfig(type), branchMode: 'first_match' },
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
  event.preventDefault();
  const type = event.dataTransfer?.getData('application/x-workflow-node') as WorkflowNodeType;
  if (!type) return;

  // 将鼠标松开时的屏幕像素坐标精准转换为 VueFlow 画布当前坐标系
  let dropPosition: { x: number; y: number } | undefined;
  if (typeof screenToFlowCoordinate === 'function') {
    const rawPos = screenToFlowCoordinate({ x: event.clientX, y: event.clientY });
    // 卡片标准宽度为 210px，让松开时光标落在卡片中心偏上位置
    dropPosition = {
      x: Math.round(rawPos.x - 105),
      y: Math.round(rawPos.y - 35),
    };
  } else {
    // 兼容兜底方案
    const canvasEl = (document.querySelector('.workflow-canvas .vue-flow') as HTMLElement | null)
      || (document.querySelector('.workflow-canvas') as HTMLElement | null);
    if (canvasEl) {
      const rect = canvasEl.getBoundingClientRect();
      const zoom = viewport.value.zoom || 1;
      dropPosition = {
        x: Math.round((event.clientX - rect.left - (viewport.value.x || 0)) / zoom - 105),
        y: Math.round((event.clientY - rect.top - (viewport.value.y || 0)) / zoom - 35),
      };
    }
  }

  addNode(type, dropPosition);
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
    label: computeEdgeLabel({ is_default: order === 0, source: connection.source }),
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

/**
 * 把结构化校验问题聚焦回画布：优先选中出问题的连线，其次节点。
 * 后端返回 code 是机器可读标识，这里用 message 直接展示给用户，
 * 不把它翻译成中文以免与后端文案脱节。
 *
 * @param issues 后端返回的校验问题列表。
 */
function focusValidationIssue(issues: WorkflowValidationIssue[]) {
  const first = issues[0];
  if (!first) return;
  if (first.edge_id && flowEdges.value.some((edge) => edge.id === first.edge_id)) {
    selectedNodeId.value = '';
    selectedEdgeId.value = first.edge_id;
  } else if (first.node_id && flowNodes.value.some((node) => node.id === first.node_id)) {
    selectedNodeId.value = first.node_id;
    selectedEdgeId.value = '';
  }
}

/**
 * 发布当前工作流草稿。
 *
 * 顺序固定为"先落库 → 结构化校验 → 发布"：
 * 1. 发布接口按 expected_revision 做乐观锁，如果画布与库里的草稿不一致，
 *    发布出去的就是旧内容，所以必须先把当前画布保存成新 revision；
 * 2. 再调 validate 拿结构化问题，把用户直接定位到出问题的节点/连线；
 * 3. 最后 publish 生成不可变版本。
 *
 * 修订冲突（409）时不覆盖、不重试，只提示刷新并展示服务端当前 revision。
 */
async function publishDefinition() {
  if (!props.agentId || publishInFlight.value || props.disabled) return;

  // 本地静态校验先兜一层：能立刻定位的错误不必往返后端。
  if (!validateDefinition()) return;

  publishIssues.value = [];
  revisionConflict.value = null;
  publishInFlight.value = true;
  try {
    // 发布前必须先把画布落库，否则 expected_revision 与内容不匹配。
    let revision = props.draftRevision;
    if (props.saveDraft) {
      const saved = await props.saveDraft();
      if (saved === null) return; // 保存失败，父组件已提示，直接中断
      revision = saved;
    }

    const validated = await validateWorkflowDefinition(props.agentId, cloneAgentConfigForPublish());
    const issues = Array.isArray(validated?.data) ? validated.data : [];
    if (issues.length > 0) {
      publishIssues.value = issues;
      validationStatus.value = 'error';
      validationMessage.value = `发布前校验发现 ${issues.length} 个问题：${issues[0].message}`;
      emit('validation-error', validationMessage.value);
      focusValidationIssue(issues);
      MessagePlugin.error(validationMessage.value);
      return;
    }

    const result = await publishWorkflow(props.agentId, revision);
    const version = result?.data?.version ?? 0;
    const publishedDraftRevision = result?.data?.draft_revision ?? revision;
    validationStatus.value = 'success';
    validationMessage.value = `已发布版本 v${version}`;
    emit('validation-error', '');
    emit('published', { version, draftRevision: publishedDraftRevision });
    MessagePlugin.success(`已发布为版本 v${version}`);
  } catch (error: any) {
    handlePublishError(error);
  } finally {
    publishInFlight.value = false;
  }
}

/**
 * 组装发布校验用的配置载荷。
 * 后端 validate 先通过 agent_type 确认配置属于工作流，再校验 workflow 定义与依赖资源；
 * 这里必须带上当前的 sandbox_config_id，以便后端正确校验 Skill 节点的沙箱依赖。
 */
function cloneAgentConfigForPublish(): CustomAgentConfig {
  return {
    agent_type: 'workflow',
    sandbox_config_id: props.sandboxConfigId || undefined,
    workflow: toDefinition(),
  };
}

/**
 * 统一处理发布失败：区分修订冲突与普通失败。
 * 请求层会把后端 { error: { code, message, details } } 原样抛出，
 * 因此冲突时可以从 error.details.current_revision 读到服务端 revision。
 *
 * @param error 请求层抛出的错误对象。
 */
function handlePublishError(error: any) {
  const status = error?.$httpStatus ?? error?.status;
  if (status === 409) {
    const currentRevision = error?.error?.details?.current_revision;
    revisionConflict.value = typeof currentRevision === 'number' ? currentRevision : 0;
    validationStatus.value = 'error';
    // 冲突时绝不静默覆盖：清楚告诉用户库里已经是新版，需要刷新后重做。
    validationMessage.value = revisionConflict.value
      ? `草稿已被其他更新修改：服务端当前修订号为 r${revisionConflict.value}，你的画布基于 r${props.draftRevision}。请先重新加载最新内容，再重新做出你的修改，切勿直接覆盖。`
      : `草稿已被其他更新修改，服务端修订号已变化。请先重新加载最新内容，再重新做出你的修改，切勿直接覆盖。`;
    emit('validation-error', validationMessage.value);
    MessagePlugin.warning(validationMessage.value);
    return;
  }
  const message = error?.message || '发布失败，请稍后重试。';
  validationStatus.value = 'error';
  validationMessage.value = message;
  emit('validation-error', message);
  MessagePlugin.error(message);
}

/** 用户在冲突提示中点击"重新加载"：交给父组件重新拉取智能体详情。 */
function requestReload() {
  revisionConflict.value = null;
  emit('reload');
}

const workflowRunStatusLabels: Record<string, string> = {
  running: '运行中',
  succeeded: '成功',
  partial: '部分成功',
  failed: '失败',
  canceled: '已取消',
};

const workflowNodeStatusLabels: Record<string, string> = {
  pending: '排队中',
  running: '运行中',
  succeeded: '成功',
  failed: '失败',
  canceled: '已取消',
  skipped: '已跳过',
};

function workflowRunStatusLabel(status: string) {
  return workflowRunStatusLabels[status] || status;
}

function workflowNodeStatusLabel(status: string) {
  return workflowNodeStatusLabels[status] || status;
}

function stopDebugPolling() {
  if (debugPollTimer) {
    clearInterval(debugPollTimer);
    debugPollTimer = null;
  }
}

async function refreshDebugRun() {
  if (!props.agentId || !debugRun.value?.id) return;
  try {
    const result = await getWorkflowRun(props.agentId, debugRun.value.id);
    debugRun.value = result?.data || debugRun.value;
    if (debugRunTerminal.value) stopDebugPolling();
  } catch (error: any) {
    validationStatus.value = 'error';
    validationMessage.value = error?.message || '读取试跑状态失败';
  }
}

function startDebugPolling() {
  stopDebugPolling();
  debugPollTimer = setInterval(() => {
    void refreshDebugRun();
  }, 1000);
}

/** 打开试跑抽屉；父组件的“保存并试跑”也复用这个入口。 */
function openDebugRun() {
  if (!props.agentId) {
    MessagePlugin.warning('请先保存工作流，再使用编辑器内试跑');
    return;
  }
  debugDrawerOpen.value = true;
  debugMinimized.value = false;
}

function closeDebugRun() {
  if (debugRun.value && !debugRunTerminal.value) {
    MessagePlugin.warning('当前试跑仍在运行，请先停止后再关闭面板');
    return;
  }
  debugDrawerOpen.value = false;
  stopDebugPolling();
}

async function startDebugRun() {
  if (!props.agentId || debugLoading.value || !debugQuery.value.trim()) return;
  if (!validateDefinition()) return;
  debugLoading.value = true;
  try {
    let expectedRevision = props.draftRevision;
    if (props.saveDraft) {
      const savedRevision = await props.saveDraft();
      if (savedRevision === null) return;
      expectedRevision = savedRevision;
    }
    const result = await startWorkflowDebugRun(
      props.agentId,
      expectedRevision,
      { query: debugQuery.value.trim(), attachments_text: debugAttachmentsText.value.trim() },
      `debug:${props.agentId}:${expectedRevision}:${Date.now()}`,
    );
    debugRun.value = result?.data || null;
    debugDrawerOpen.value = true;
    if (debugRun.value && !debugRunTerminal.value) startDebugPolling();
  } catch (error: any) {
    MessagePlugin.error(error?.message || '试跑启动失败');
  } finally {
    debugLoading.value = false;
  }
}

async function cancelDebugRun() {
  if (!props.agentId || !debugRun.value?.id || debugLoading.value) return;
  debugLoading.value = true;
  try {
    const result = await cancelWorkflowRun(props.agentId, debugRun.value.id);
    if (result?.data?.run) debugRun.value = result.data.run;
    await refreshDebugRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || '停止试跑失败');
  } finally {
    debugLoading.value = false;
  }
}

async function retryDebugRun() {
  if (!props.agentId || !debugRun.value?.id || debugLoading.value) return;
  debugLoading.value = true;
  try {
    const result = await retryWorkflowRun(props.agentId, debugRun.value.id);
    debugRun.value = result?.data || null;
    if (debugRun.value && !debugRunTerminal.value) startDebugPolling();
  } catch (error: any) {
    MessagePlugin.error(error?.message || '整次重跑失败');
  } finally {
    debugLoading.value = false;
  }
}

async function retryDebugNode(nodeRunId: number) {
  if (!props.agentId || !debugRun.value?.id || debugLoading.value) return;
  debugLoading.value = true;
  try {
    const result = await retryWorkflowNode(props.agentId, debugRun.value.id, nodeRunId);
    debugRun.value = result?.data || null;
    if (debugRun.value && !debugRunTerminal.value) startDebugPolling();
  } catch (error: any) {
    MessagePlugin.error(error?.message || '节点重试失败');
  } finally {
    debugLoading.value = false;
  }
}

const workflowFileInputRef = ref<HTMLInputElement | null>(null);

/**
 * 触发当前画布工作流导出为标准 JSON 文件
 */
function triggerExportWorkflow() {
  const definition = toDefinition();
  if (!definition.nodes || definition.nodes.length === 0) {
    MessagePlugin.warning('当前画布没有任何节点，无法导出');
    return;
  }
  try {
    exportWorkflowPackage({
      name: '工作流',
      description: '',
      workflow: definition,
    });
    showHint('工作流导出成功');
    MessagePlugin.success('工作流导出成功');
  } catch (err: any) {
    MessagePlugin.error(`导出失败：${err?.message || '未知错误'}`);
  }
}

/**
 * 唤起本地 JSON 文件选择器
 */
function triggerImportWorkflow() {
  if (props.disabled) return;
  if (workflowFileInputRef.value) {
    workflowFileInputRef.value.value = '';
    workflowFileInputRef.value.click();
  }
}

/**
 * 处理用户选中的工作流 JSON 文件
 */
async function onWorkflowFileSelected(event: Event) {
  const target = event.target as HTMLInputElement;
  const file = target?.files?.[0];
  if (!file) return;

  try {
    const fileText = await readJSONFile(file);
    const result = parseWorkflowJSON(fileText);
    if (!result.success) {
      MessagePlugin.error(result.error);
      return;
    }

    if (!props.agentId) {
      MessagePlugin.warning('请先保存工作流，再导入并进行服务端资源预检');
      return;
    }
    const document = JSON.parse(fileText);
    const previewResult = await previewWorkflowImport(props.agentId, document);
    const preview = previewResult?.data;
    const newWorkflow = preview?.definition || result.data.workflow;
    if (!newWorkflow) {
      MessagePlugin.error('服务端没有返回可替换的工作流定义');
      return;
    }

    const notices = [
      ...(preview?.issues || []).map((issue) => `结构问题：${issue.message}`),
      ...(preview?.missing_resources || []).map((resource) => `缺失资源：${resource}`),
      ...(preview?.sensitive_fields || []).map((field) => `已脱敏：${field}`),
    ];

    const doApplyImport = () => {
      // 记录撤回快照，确保用户可以 Ctrl+Z 一键撤回
      pushSnapshot();
      loadDefinition(newWorkflow);
      emitDefinition();
      nextTick(() => {
        fitCanvas();
        showHint('已成功载入导入的工作流');
      });
      MessagePlugin.success('工作流导入成功');
    };

    // 预检结果或当前画布有内容时都要明确确认，避免用户误替换或误以为资源已自动补齐。
    if (notices.length > 0 || (flowNodes.value.length > 0 && !isUntouchedDefinition(toDefinition()))) {
      const confirmDialog = DialogPlugin.confirm({
        header: '确认导入工作流',
        body: `${notices.length ? `${notices.join('\n')}\n\n` : ''}导入新流程将替换当前画布的所有节点与连线，是否继续？`,
        confirmBtn: { content: '确认导入', theme: 'primary' },
        cancelBtn: { content: '取消' },
        onConfirm: () => {
          confirmDialog.destroy();
          doApplyImport();
        },
        onClose: () => {
          confirmDialog.destroy();
        },
      });
    } else {
      doApplyImport();
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || '读取工作流文件失败');
  } finally {
    if (target) {
      target.value = '';
    }
  }
}

// 暴露给父组件的能力
defineExpose({
  validate: validateDefinition,
  validateDraftSyntax: () => pendingJSONErrors().length === 0,
  isUntouched: () => isUntouchedFlow.value,
  isTemplateGalleryOpen: () => showTemplateGallery.value,
  loadWorkflowDefinition: (def: WorkflowDefinition) => {
    loadDefinition(def);
    nextTick(() => {
      fitCanvas();
    });
  },
  toDefinition,
  publish: publishDefinition,
  openDebugRun,
  clearRevisionConflict: () => { revisionConflict.value = null; },
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
  padding: 0 48px 12px 0;
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

/* 修订与发布状态徽标：让用户一眼看到"库里是什么版本、画布改没改" */
.workflow-revision-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.workflow-unpublished-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 500;
  background: color-mix(in srgb, var(--td-success-color) 12%, transparent);
  color: var(--td-success-color);

  &.is-dirty {
    background: color-mix(in srgb, var(--td-warning-color) 14%, transparent);
    color: var(--td-warning-color);
  }
}

/* 修订冲突横幅：强调"刷新而非覆盖"的操作导向 */
.workflow-revision-conflict {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 10px 48px 0 0;
  padding: 10px 14px;
  border: 1px solid color-mix(in srgb, var(--td-error-color) 40%, transparent);
  border-radius: 8px;
  background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
  color: var(--td-error-color);
  font-size: 13px;
}

.workflow-revision-conflict-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;

  strong {
    font-weight: 600;
  }

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
  }
}

/* 发布前结构化校验问题列表 */
.workflow-publish-issues {
  margin: 10px 48px 0 0;
  padding: 10px 14px;
  border: 1px solid color-mix(in srgb, var(--td-warning-color) 40%, transparent);
  border-radius: 8px;
  background: color-mix(in srgb, var(--td-warning-color) 8%, transparent);

  ul {
    margin: 6px 0 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
}

.workflow-publish-issues-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.workflow-publish-issue {
  display: flex;
  align-items: baseline;
  gap: 8px;
  width: 100%;
  padding: 4px 6px;
  border: none;
  border-radius: 6px;
  background: transparent;
  text-align: left;
  font-size: 13px;
  color: var(--td-text-color-primary);
  cursor: pointer;

  &:hover {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
  }

  em {
    font-style: normal;
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }
}

.workflow-publish-issue-code {
  flex-shrink: 0;
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-brand-color);
}

.workflow-debug-drawer {
  position: relative;
  margin-top: 12px;
  margin-right: 0;
  width: 100%;
  border: 1px solid var(--td-component-stroke);
  border-radius: 12px;
  background: var(--td-bg-color-container);
  box-shadow: 0 12px 32px -4px rgba(15, 23, 42, 0.08), 0 4px 12px -2px rgba(15, 23, 42, 0.04);
  overflow: hidden;
  flex-shrink: 0;
  transition: all 0.25s cubic-bezier(0.16, 1, 0.3, 1);

  &.is-minimized {
    .workflow-debug-drawer__header {
      border-bottom: none;
    }
  }
}

.workflow-debug-drawer__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 16px;
  background: color-mix(in srgb, var(--td-bg-color-container) 95%, var(--td-brand-color) 5%);
  border-bottom: 1px solid var(--td-component-stroke);
}

.workflow-debug-drawer__title-area {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.workflow-debug-drawer__badge-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  color: var(--td-brand-color);
  font-size: 15px;
  flex-shrink: 0;
}

.workflow-debug-drawer__titles {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.workflow-debug-drawer__headline {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;

  strong {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    letter-spacing: -0.01em;
  }
}

.workflow-debug-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 500;
  line-height: 1.5;

  &--revision {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    border: 1px solid var(--td-component-stroke);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }

  &--idle {
    background: var(--td-bg-color-page);
    color: var(--td-text-color-placeholder);
    border: 1px solid var(--td-component-stroke);
  }

  &--running {
    background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    color: var(--td-brand-color);
    border: 1px solid color-mix(in srgb, var(--td-brand-color) 30%, transparent);

    .workflow-debug-pill__dot {
      background: var(--td-brand-color);
      animation: workflow-pulse-dot 1.4s ease-in-out infinite;
    }
  }

  &--succeeded {
    background: color-mix(in srgb, var(--td-success-color) 12%, transparent);
    color: var(--td-success-color);
    border: 1px solid color-mix(in srgb, var(--td-success-color) 30%, transparent);

    .workflow-debug-pill__dot {
      background: var(--td-success-color);
    }
  }

  &--failed, &--canceled {
    background: color-mix(in srgb, var(--td-error-color) 12%, transparent);
    color: var(--td-error-color);
    border: 1px solid color-mix(in srgb, var(--td-error-color) 30%, transparent);

    .workflow-debug-pill__dot {
      background: var(--td-error-color);
    }
  }

  &--partial {
    background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    color: var(--td-warning-color);
    border: 1px solid color-mix(in srgb, var(--td-warning-color) 30%, transparent);

    .workflow-debug-pill__dot {
      background: var(--td-warning-color);
    }
  }

  &__dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
  }
}

.workflow-debug-drawer__sub {
  font-size: 11px;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.workflow-debug-drawer__header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.workflow-debug-tool-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s ease;

  &:hover:not(:disabled) {
    color: var(--td-brand-color);
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 5%, transparent);
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
}

.workflow-icon-button--close:hover {
  color: var(--td-error-color);
  background: color-mix(in srgb, var(--td-error-color) 10%, transparent);
}

.workflow-debug-drawer__body {
  display: grid;
  grid-template-columns: 380px minmax(0, 1fr);
  gap: 16px;
  padding: 14px 16px;
  height: 310px;
  box-sizing: border-box;
  background: var(--td-bg-color-container);
}

/* 左侧参数区 */
.workflow-debug-left {
  display: flex;
  flex-direction: column;
  gap: 10px;
  height: 100%;
  min-height: 0;
  padding-right: 14px;
  border-right: 1px solid var(--td-component-stroke);
}

.workflow-debug-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.workflow-debug-section-head__title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.workflow-debug-clear-btn {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  &:hover {
    color: var(--td-brand-color);
  }
}

.workflow-debug-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.workflow-debug-field {
  display: flex;
  flex-direction: column;
  gap: 5px;

  &__label {
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 12px;
    color: var(--td-text-color-secondary);
    font-weight: 500;
  }

  &__required {
    font-size: 11px;
    color: var(--td-brand-color);
  }

  &__optional {
    font-size: 11px;
    color: var(--td-text-color-placeholder);
  }
}

.workflow-debug-textarea-wrap {
  position: relative;
  display: flex;
  flex-direction: column;

  textarea {
    width: 100%;
    min-height: 80px;
    resize: none;
    padding: 8px 10px 22px;
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    background: var(--td-bg-color-page);
    color: var(--td-text-color-primary);
    font: inherit;
    font-size: 13px;
    line-height: 1.5;
    box-sizing: border-box;
    transition: all 0.15s ease;

    &:focus {
      outline: none;
      border-color: var(--td-brand-color);
      background: var(--td-bg-color-container);
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    }
  }
}

.workflow-debug-textarea-hint {
  position: absolute;
  bottom: 6px;
  right: 8px;
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  pointer-events: none;
}

.workflow-debug-field > textarea {
  width: 100%;
  min-height: 52px;
  resize: none;
  padding: 6px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-page);
  color: var(--td-text-color-primary);
  font: inherit;
  font-size: 12px;
  line-height: 1.5;
  box-sizing: border-box;
  transition: all 0.15s ease;

  &:focus {
    outline: none;
    border-color: var(--td-brand-color);
    background: var(--td-bg-color-container);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  }
}

.workflow-debug-actions {
  margin-top: auto;
  padding-top: 8px;
}

.workflow-debug-run-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  height: 36px;
  border: none;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s cubic-bezier(0.16, 1, 0.3, 1);

  &--primary {
    background: var(--td-brand-color);
    color: #fff;
    box-shadow: 0 2px 8px color-mix(in srgb, var(--td-brand-color) 35%, transparent);

    &:hover:not(:disabled) {
      background: color-mix(in srgb, var(--td-brand-color) 88%, #000);
      transform: translateY(-1px);
      box-shadow: 0 4px 12px color-mix(in srgb, var(--td-brand-color) 45%, transparent);
    }

    &:active:not(:disabled) {
      transform: translateY(0);
    }
  }

  &--danger {
    background: var(--td-error-color);
    color: #fff;

    &:hover:not(:disabled) {
      background: color-mix(in srgb, var(--td-error-color) 88%, #000);
    }
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
    transform: none !important;
    box-shadow: none !important;
  }
}

/* 右侧流转监控与结果面板 */
.workflow-debug-right {
  height: 100%;
  min-height: 0;
  min-width: 0;
  overflow-y: auto;
  padding-right: 4px;
}

/* 就绪空状态 */
.workflow-debug-empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  text-align: center;
  padding: 16px;
  box-sizing: border-box;

  h4 {
    margin: 12px 0 4px;
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 0 0 16px;
    max-width: 420px;
    font-size: 12px;
    line-height: 1.6;
    color: var(--td-text-color-secondary);
  }
}

.workflow-debug-empty-state__icon-ring {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 56px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
  color: var(--td-brand-color);
  border: 1px dashed color-mix(in srgb, var(--td-brand-color) 30%, transparent);
}

.workflow-debug-empty-state__features {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  justify-content: center;
}

.workflow-debug-feature-tag {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 10px;
  border-radius: 999px;
  background: var(--td-bg-color-page);
  border: 1px solid var(--td-component-stroke);
  font-size: 11px;
  color: var(--td-text-color-secondary);
}

/* 运行态与流水线 */
.workflow-debug-output-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.workflow-debug-metrics-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  border-radius: 8px;
  background: var(--td-bg-color-page);
  border: 1px solid var(--td-component-stroke);
}

.workflow-debug-metrics-bar__left,
.workflow-debug-metrics-bar__right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.workflow-debug-run-id {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.workflow-debug-badge {
  display: inline-flex;
  padding: 2px 7px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 500;

  &.is-running {
    background: color-mix(in srgb, var(--td-brand-color) 15%, transparent);
    color: var(--td-brand-color);
  }
  &.is-succeeded {
    background: color-mix(in srgb, var(--td-success-color) 15%, transparent);
    color: var(--td-success-color);
  }
  &.is-failed, &.is-canceled {
    background: color-mix(in srgb, var(--td-error-color) 15%, transparent);
    color: var(--td-error-color);
  }
  &.is-partial {
    background: color-mix(in srgb, var(--td-warning-color) 15%, transparent);
    color: var(--td-warning-color);
  }
}

.workflow-debug-metric-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: var(--td-text-color-secondary);

  strong {
    color: var(--td-text-color-primary);
    font-weight: 600;
  }
}

.workflow-debug-error-banner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
  border: 1px solid color-mix(in srgb, var(--td-error-color) 25%, transparent);
  color: var(--td-error-color);
  font-size: 12px;

  &__body {
    strong {
      display: block;
      font-weight: 600;
      margin-bottom: 2px;
    }
    p {
      margin: 0;
      line-height: 1.5;
      word-break: break-all;
    }
  }
}

.workflow-debug-final-output {
  padding: 10px 12px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--td-brand-color) 4%, transparent);
  border: 1px solid color-mix(in srgb, var(--td-brand-color) 18%, transparent);

  &__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 6px;
  }

  &__title {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--td-brand-color);
  }

  &__content {
    font-size: 13px;
    line-height: 1.6;
    color: var(--td-text-color-primary);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 120px;
    overflow-y: auto;
    padding: 6px 8px;
    background: var(--td-bg-color-container);
    border-radius: 6px;
    border: 1px solid var(--td-component-stroke);
  }
}

.workflow-debug-timeline {
  &__title {
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    margin-bottom: 8px;
  }
}

.workflow-debug-nodes-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.workflow-debug-node-card {
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--td-bg-color-page);
  border: 1px solid var(--td-component-stroke);
  transition: all 0.15s ease;

  &:hover {
    border-color: color-mix(in srgb, var(--td-brand-color) 35%, transparent);
    background: var(--td-bg-color-container);
  }

  &.is-running {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 15%, transparent);
  }

  &.is-failed {
    border-color: color-mix(in srgb, var(--td-error-color) 40%, transparent);
  }

  &__header {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  &__index {
    font-size: 11px;
    font-family: ui-monospace, monospace;
    color: var(--td-text-color-placeholder);
    width: 14px;
    text-align: center;
  }

  &__status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--td-text-color-placeholder);
    flex-shrink: 0;

    &.is-running {
      background: var(--td-brand-color);
      animation: workflow-pulse-dot 1.4s infinite;
    }
    &.is-succeeded { background: var(--td-success-color); }
    &.is-failed, &.is-canceled { background: var(--td-error-color); }
    &.is-pending { background: var(--td-warning-color); }
  }

  &__meta {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
  }

  &__name {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  &__badge {
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 4px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
    flex-shrink: 0;
  }

  &__extra {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }

  &__duration {
    font-size: 11px;
    font-family: ui-monospace, monospace;
    color: var(--td-text-color-placeholder);
  }

  &__error {
    margin-top: 6px;
    padding: 5px 8px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
    color: var(--td-error-color);
    font-size: 11px;
    line-height: 1.4;
    word-break: break-all;
  }

  &__output {
    margin-top: 6px;
    padding: 5px 8px;
    border-radius: 4px;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    color: var(--td-text-color-secondary);
    font-size: 11px;
    line-height: 1.4;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 80px;
    overflow-y: auto;
  }
}

.workflow-debug-retry-node-btn {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  height: 22px;
  padding: 0 6px;
  border: 1px solid color-mix(in srgb, var(--td-brand-color) 30%, transparent);
  border-radius: 4px;
  background: transparent;
  color: var(--td-brand-color);
  font-size: 11px;
  cursor: pointer;

  &:hover:not(:disabled) {
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  }
}

@keyframes workflow-pulse-dot {
  0%, 100% { transform: scale(1); opacity: 1; }
  50% { transform: scale(1.35); opacity: 0.6; }
}

.workflow-icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover { border-color: var(--td-component-stroke); color: var(--td-text-color-primary); }
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

  :deep(.vue-flow__edge-textwrapper) {
    pointer-events: all;
    cursor: pointer;
  }

  :deep(.vue-flow__edge-textbg) {
    fill: #ffffff;
    stroke: #e2e8f0;
    stroke-width: 1px;
    rx: 6px;
    ry: 6px;
    filter: drop-shadow(0 1px 3px rgba(15, 23, 42, 0.08));
    transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  }

  :deep(.vue-flow__edge-text) {
    fill: #475569;
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.02em;
    user-select: none;
    transition: fill 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  }

  :deep(.vue-flow__edge:hover .vue-flow__edge-textbg) {
    stroke: #94a3b8;
    filter: drop-shadow(0 2px 6px rgba(15, 23, 42, 0.12));
  }

  :deep(.vue-flow__edge:hover .vue-flow__edge-text) {
    fill: #0f172a;
  }

  :deep(.vue-flow__edge.selected .vue-flow__edge-textbg) {
    fill: #f0f7ff;
    stroke: var(--td-brand-color, #0052d9);
    stroke-width: 1.5px;
    filter: drop-shadow(0 2px 8px rgba(0, 82, 217, 0.22));
  }

  :deep(.vue-flow__edge.selected .vue-flow__edge-text) {
    fill: var(--td-brand-color, #0052d9);
    font-weight: 600;
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
  overflow-x: hidden;
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

.workflow-edge-endpoints-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
  flex-wrap: wrap;
}

.workflow-edge-endpoint-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 7px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  line-height: 1.3;

  code {
    font-family: inherit;
    color: var(--td-text-color-primary);
  }
}

.workflow-edge-endpoint-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;

  &--source { background: #059669; }
  &--target { background: #0284c7; }
}

.workflow-edge-arrow-icon {
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
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

/* 分支模式选择：两张可点卡片，用边框与底色区分当前生效的策略 */
.workflow-branch-mode {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.workflow-branch-mode-option {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 10px 12px;
  text-align: left;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;

  strong {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  small {
    font-size: 12px;
    line-height: 1.45;
    color: var(--td-text-color-secondary);
  }

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
  }

  &:disabled {
    opacity: 0.6;
    cursor: not-allowed;
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

/* 步骤输出与下游引用卡片规范 */
.workflow-output-cards {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.workflow-output-card {
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer, #f8fafc);
  border: 1px solid var(--td-component-stroke, #e2e8f0);
  border-radius: 8px;
  transition: all 0.18s ease;

  &:hover {
    border-color: color-mix(in srgb, var(--td-brand-color) 40%, #e2e8f0);
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04);
  }
}

.workflow-output-card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}

.workflow-output-card-meta {
  display: flex;
  align-items: center;
  gap: 6px;
}

.workflow-output-card-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--td-text-color-primary, #0f172a);
}

.workflow-type-badge {
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--td-bg-color-container, #ffffff);
  color: var(--td-text-color-secondary, #64748b);
  border: 1px solid var(--td-component-stroke, #e2e8f0);
}

.workflow-primary-tag {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  color: var(--td-brand-color);
  font-weight: 500;
}

.workflow-output-card-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary, #64748b);
  line-height: 1.45;
  margin-bottom: 8px;
}

.workflow-output-card-codes {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-top: 6px;
  border-top: 1px dashed color-mix(in srgb, var(--td-component-stroke) 80%, transparent);
}

.workflow-code-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}

.workflow-code-row-title {
  color: var(--td-text-color-placeholder, #94a3b8);
  font-size: 11px;
  min-width: 54px;
}

.workflow-clickable-code {
  flex: 1;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-brand-color);
  background: var(--td-bg-color-container, #ffffff);
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid var(--td-component-stroke, #e2e8f0);
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: all 0.16s ease;

  &:hover {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    border-color: var(--td-brand-color);
  }
}

.workflow-copy-mini-btn {
  padding: 3px;
  border: none;
  background: transparent;
  color: var(--td-text-color-placeholder, #94a3b8);
  border-radius: 4px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.16s ease;

  &:hover {
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  }
}

/* 连线快捷候选标签 */
.workflow-quick-choices {
  margin-top: 6px;
}

.workflow-quick-choices-title {
  display: block;
  font-size: 11px;
  color: var(--td-text-color-secondary, #64748b);
  margin-bottom: 4px;
}

.workflow-quick-choice-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.workflow-quick-choice-chip {
  padding: 2px 8px;
  border: 1px dashed var(--td-brand-color);
  border-radius: 999px;
  background: color-mix(in srgb, var(--td-brand-color) 5%, transparent);
  color: var(--td-brand-color);
  font-size: 11px;
  cursor: pointer;
  transition: all 0.16s ease;

  &:hover:not(:disabled) {
    background: var(--td-brand-color);
    color: #fff;
    border-style: solid;
  }

  &--active {
    background: var(--td-brand-color);
    color: #fff;
    border-style: solid;
    font-weight: 600;
    box-shadow: 0 1px 4px rgba(0, 82, 217, 0.25);
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
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
  padding: 10px 12px;
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
  grid-template-columns: minmax(0, 1fr) 104px;
  gap: 8px;
  margin-bottom: 8px;
  width: 100%;
  min-width: 0;
}

.workflow-condition-select-var,
.workflow-condition-select-op {
  width: 100% !important;
  min-width: 0 !important;
}

.workflow-condition-select-var :deep(.t-input),
.workflow-condition-select-op :deep(.t-input) {
  width: 100% !important;
  min-width: 0 !important;
}

.workflow-condition-select-var :deep(.t-input__inner),
.workflow-condition-select-op :deep(.t-input__inner) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workflow-condition-unary-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
  border: 1px dashed var(--td-component-stroke);
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
