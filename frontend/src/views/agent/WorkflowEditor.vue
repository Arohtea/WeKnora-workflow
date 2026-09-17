<template>
  <div class="workflow-editor">
    <div class="workflow-toolbar">
      <div>
        <h3 class="workflow-title">流程编排</h3>
        <p class="workflow-subtitle">把用户的问题按固定步骤处理：先检索或调用工具，再由模型组织最终回复。</p>
      </div>
      <div class="workflow-toolbar-actions">
        <button type="button" class="workflow-icon-button" title="适应画布" :disabled="disabled" @click="fitCanvas">
          <t-icon name="fullscreen-1" />
        </button>
        <button
          type="button"
          class="workflow-toolbar-button"
          :disabled="disabled"
          :title="showTemplateGallery ? '返回画布' : '用现成模板替换当前流程'"
          @click="toggleTemplateGallery"
        >
          <t-icon name="view-module" />
          {{ showTemplateGallery ? '返回画布' : '模板' }}
        </button>
        <button type="button" class="workflow-toolbar-button" :disabled="disabled" @click="validateDefinition">
          <t-icon name="check-circle" />
          校验
        </button>
        <button type="button" class="workflow-toolbar-button" :disabled="disabled" @click="emit('run')">
          <t-icon name="play-circle" />
          保存并去试用
        </button>
      </div>
    </div>

    <div class="workflow-onboarding" aria-label="工作流编排步骤">
      <div v-for="(step, index) in onboardingSteps" :key="step.title" class="workflow-onboarding-step">
        <span class="workflow-onboarding-index">{{ index + 1 }}</span>
        <span>
          <strong>{{ step.title }}</strong>
          <small>{{ step.description }}</small>
        </span>
      </div>
    </div>

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
        <div class="workflow-panel-heading">添加步骤</div>
        <p class="workflow-panel-hint">点击即可加入画布，再把它和前后步骤连起来。</p>
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
          <span class="workflow-palette-icon"><t-icon :name="item.icon" /></span>
          <span>
            <strong>{{ item.label }}</strong>
            <small>{{ item.description }}</small>
          </span>
        </button>

        <div class="workflow-legend">
          <span class="workflow-legend-dot workflow-legend-dot--start" /> 开始
          <span class="workflow-legend-dot workflow-legend-dot--end" /> 结束
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
          :nodes-draggable="!disabled"
          :nodes-connectable="!disabled"
          :elements-selectable="true"
          :default-viewport="viewport"
          :fit-view-on-init="false"
          :default-edge-options="{ type: 'smoothstep', animated: false }"
          @connect="onConnect"
          @node-click="onNodeClick"
          @edge-click="onEdgeClick"
          @pane-click="clearSelection"
          @move-end="onMoveEnd"
        >
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
            <div>
              <span class="workflow-inspector-kicker">节点属性</span>
              <h3>{{ selectedNode.data.name }}</h3>
              <button
                type="button"
                class="workflow-node-id"
                :title="`引用这个节点的输出时会用到：nodes.${selectedNode.id}`"
                @click="copyNodeVariable(selectedNode.id)"
              >
                nodes.{{ selectedNode.id }}
                <t-icon name="copy" size="12px" />
              </button>
            </div>
            <button
              v-if="!disabled && selectedNode.data.workflowType !== 'start'"
              type="button"
              class="workflow-icon-button workflow-icon-button--danger"
              title="删除节点"
              @click="removeSelectedNode"
            >
              <t-icon name="delete" />
            </button>
          </div>

          <label class="workflow-field">
            <span>节点名称</span>
            <input :value="selectedNode.data.name" :disabled="disabled" @input="updateNodeName(inputValue($event))" />
            <small>名称只影响画布显示，方便你和同事看懂每一步在做什么。</small>
          </label>

          <div class="workflow-node-type-badge" :class="`workflow-node-type-badge--${selectedNode.data.workflowType}`">
            {{ nodeTypeLabel(selectedNode.data.workflowType) }}
          </div>

          <div class="workflow-node-help">
            <div>
              <strong>必填项</strong>
              <span>{{ selectedNodeHelp.required }}</span>
            </div>
            <div>
              <strong>变量示例</strong>
              <code>{{ selectedNodeHelp.variables }}</code>
            </div>
            <div>
              <strong>输出说明</strong>
              <span>{{ selectedNodeHelp.output }}</span>
            </div>
          </div>

          <template v-if="selectedNode.data.workflowType === 'knowledge-retrieval'">
            <div v-if="knowledgeBaseOptions.length === 0" class="workflow-resource-empty">
              <t-icon name="folder" />
              <strong>暂无可用知识库</strong>
              <span>当前空间没有可选知识库，请先创建或获取知识库权限。</span>
              <button type="button" class="workflow-link-button" @click="emit('manage-knowledge-bases')">
                配置知识库
              </button>
            </div>
            <label v-else class="workflow-field">
              <span>知识库 *</span>
              <select
                multiple
                :value="retrievalKnowledgeBaseIDs"
                :disabled="disabled"
                @change="updateRetrievalKnowledgeBases(selectedValues($event))"
              >
                <option v-for="kb in knowledgeBaseOptions" :key="kb.value" :value="kb.value">{{ kb.label }}</option>
              </select>
              <small>可按住 Ctrl 或 Command 多选。</small>
              <small v-if="missingRetrievalKnowledgeBaseIDs.length" class="workflow-field-error">
                以下知识库已不可用：{{ missingRetrievalKnowledgeBaseIDs.join(', ') }}
              </small>
            </label>
            <label class="workflow-field">
              <span>查询模板 *</span>
              <textarea
                :value="configString('query_template')"
                :disabled="disabled"
                rows="3"
                placeholder="{{input.query}}"
                @input="updateConfig('query_template', inputValue($event))"
              />
            </label>
            <label class="workflow-field workflow-field--inline">
              <span>召回数量 *</span>
              <input
                type="number"
                min="1"
                max="50"
                :value="configNumber('top_k', 5)"
                :disabled="disabled"
                @input="updateConfig('top_k', numberValue($event, 5))"
              />
            </label>
          </template>

          <template v-else-if="selectedNode.data.workflowType === 'llm'">
            <label class="workflow-field">
              <span>系统提示词</span>
              <textarea
                :value="configString('system_prompt')"
                :disabled="disabled"
                rows="3"
                placeholder="你是一个严谨高效的文本处理助手。"
                @input="updateConfig('system_prompt', inputValue($event))"
              />
              <small>可选，用于设定模型的身份、角色与风格。</small>
            </label>
            <label class="workflow-field">
              <span>用户提示词 *</span>
              <textarea
                :value="configString('prompt')"
                :disabled="disabled"
                rows="6"
                placeholder="请根据 {{input.query}} 进行改写或总结..."
                @input="updateConfig('prompt', inputValue($event))"
              />
              <small>支持使用 &#123;&#123;input.query&#125;&#125; 等插值变量；模型生成的文本可通过 nodes.{{ selectedNode.id }}.text 供下游引用。</small>
            </label>
            <div style="display: flex; gap: 12px;">
              <label class="workflow-field workflow-field--inline" style="flex: 1;">
                <span>温度 (0-2)</span>
                <input
                  type="number"
                  step="0.1"
                  min="0"
                  max="2"
                  :value="configNumber('temperature', 0.7)"
                  :disabled="disabled"
                  @input="updateConfig('temperature', numberValue($event, 0.7))"
                />
              </label>
              <label class="workflow-field workflow-field--inline" style="flex: 1;">
                <span>最大 Token</span>
                <input
                  type="number"
                  min="1"
                  placeholder="默认不限"
                  :value="configNullableNumber('max_tokens')"
                  :disabled="disabled"
                  @input="updateConfig('max_tokens', nullableNumberValue($event))"
                />
              </label>
            </div>
          </template>

          <template v-else-if="selectedNode.data.workflowType === 'llm-decision'">
            <label class="workflow-field">
              <span>判断提示词 *</span>
              <textarea
                :value="configString('prompt')"
                :disabled="disabled"
                rows="7"
                placeholder="根据 {{input.query}} 返回一个候选标签"
                @input="updateConfig('prompt', inputValue($event))"
              />
            </label>
            <label class="workflow-field">
              <span>候选标签 *</span>
              <textarea
                :value="decisionChoicesText"
                :disabled="disabled"
                rows="4"
                placeholder="通过&#10;拒绝"
                @input="updateDecisionChoices(inputValue($event))"
              />
              <small>每行一个标签，输出可通过 nodes.节点ID.data.choice 引用。</small>
            </label>
          </template>

          <template v-else-if="selectedNode.data.workflowType === 'http-request'">
            <label class="workflow-field workflow-field--inline">
              <span>请求方法 *</span>
              <select :value="configString('method', 'GET')" :disabled="disabled" @change="updateConfig('method', inputValue($event))">
                <option v-for="method in httpMethods" :key="method" :value="method">{{ method }}</option>
              </select>
            </label>
            <label class="workflow-field">
              <span>URL *</span>
              <input
                :value="configString('url')"
                :disabled="disabled"
                placeholder="https://example.com/api/submit"
                @input="updateConfig('url', inputValue($event))"
              />
              <small>{{ urlFieldHint }}</small>
            </label>
            <label class="workflow-field">
              <span>请求头 JSON</span>
              <textarea
                :value="httpHeadersText"
                :disabled="disabled"
                rows="4"
                placeholder='{"Content-Type":"application/json"}'
                @input="updateHttpHeaders(inputValue($event))"
              />
              <small v-if="jsonFieldErrors.headers" class="workflow-field-error">{{ jsonFieldErrors.headers }}</small>
              <small v-else class="workflow-field-warning">
                为了安全，这里不能填 Authorization、Cookie、API Key 等认证头。需要带密钥调用接口时，请改用 MCP 工具接入。
              </small>
            </label>
            <label class="workflow-field">
              <span>请求体模板</span>
              <textarea
                :value="configString('body_template')"
                :disabled="disabled"
                rows="5"
                placeholder='{"query":"{{input.query}}"}'
                @input="updateConfig('body_template', inputValue($event))"
              />
            </label>
          </template>

          <template v-else-if="selectedNode.data.workflowType === 'tool'">
            <label class="workflow-field">
              <span>工具类型 *</span>
              <select :value="configString('kind', 'builtin')" :disabled="disabled" @change="changeToolKind(inputValue($event))">
                <option value="builtin">内置工具</option>
                <option value="mcp">MCP 工具</option>
                <option value="skill">Skill</option>
              </select>
            </label>

            <div v-if="toolKind === 'builtin' && builtinTools.length === 0" class="workflow-resource-empty">
              <t-icon name="tools" />
              <strong>暂无可用内置工具</strong>
              <span>当前部署没有可用于工作流的内置工具。</span>
            </div>
            <label v-else-if="toolKind === 'builtin'" class="workflow-field">
              <span>内置工具 *</span>
              <select :value="configString('tool_name')" :disabled="disabled" @change="updateConfig('tool_name', inputValue($event))">
                <option value="">请选择内置工具</option>
                <option v-if="configString('tool_name') && !selectedBuiltinTool" :value="configString('tool_name')" disabled>
                  已失效：{{ configString('tool_name') }}
                </option>
                <option v-for="tool in builtinTools" :key="tool.name" :value="tool.name">
                  {{ tool.display_name || tool.name }}
                </option>
              </select>
              <small v-if="selectedBuiltinTool?.description">{{ selectedBuiltinTool.description }}</small>
            </label>

            <template v-else-if="toolKind === 'mcp'">
              <div v-if="mcpServices.length === 0" class="workflow-resource-empty">
                <t-icon name="server" />
                <strong>暂无可用 MCP 服务</strong>
                <span>当前空间没有已启用的 MCP 服务，请先完成配置。</span>
                <button type="button" class="workflow-link-button" @click="emit('manage-mcp')">
                  管理 MCP
                </button>
              </div>
              <label v-else class="workflow-field">
                <span>MCP 服务 *</span>
                <select :value="configString('service_id')" :disabled="disabled" @change="changeMCPService(inputValue($event))">
                  <option value="">请选择 MCP 服务</option>
                  <option v-if="configString('service_id') && !selectedMCPService" :value="configString('service_id')" disabled>
                    已失效：{{ configString('service_id') }}
                  </option>
                  <option v-for="service in mcpServices" :key="service.id" :value="service.id">{{ service.name }}</option>
                </select>
              </label>
              <label v-if="selectedMCPService" class="workflow-field">
                <span>MCP 工具 *</span>
                <select :value="configString('tool_name')" :disabled="disabled" @change="updateConfig('tool_name', inputValue($event))">
                  <option value="">请选择 MCP 工具</option>
                  <option v-if="configString('tool_name') && !selectedMCPTool" :value="configString('tool_name')" disabled>
                    已失效：{{ configString('tool_name') }}
                  </option>
                  <option v-for="tool in selectedMCPService?.tools || []" :key="tool.name" :value="tool.name">
                    {{ tool.display_name || tool.name }}
                  </option>
                </select>
                <small v-if="selectedMCPTool?.description">{{ selectedMCPTool.description }}</small>
                <small v-else-if="selectedMCPService.tools.length === 0" class="workflow-field-error">
                  该服务没有可用工具，请检查服务配置。
                </small>
              </label>
            </template>

            <label v-else class="workflow-field">
              <span>Skill *</span>
              <select :value="configString('skill_name')" :disabled="disabled || !sandboxConfigId || skills.length === 0" @change="updateConfig('skill_name', inputValue($event))">
                <option value="">请选择 Skill</option>
                <option v-if="configString('skill_name') && !selectedSkill" :value="configString('skill_name')" disabled>
                  已失效：{{ configString('skill_name') }}
                </option>
                <option v-for="skill in skills" :key="skill.name" :value="skill.name">{{ skillOptionLabel(skill) }}</option>
              </select>
              <small v-if="selectedSkill?.description">{{ selectedSkill.description }}</small>
            </label>

            <div v-if="toolKind === 'skill' && !sandboxConfigId" class="workflow-resource-empty">
              <t-icon name="server" />
              <strong>尚未选择运行沙箱</strong>
              <span>Skill 只能从当前运行沙箱的已安装可用列表中选择。</span>
              <button type="button" class="workflow-link-button" @click="emit('select-sandbox')">
                选择运行沙箱
              </button>
            </div>
            <div v-else-if="toolKind === 'skill' && skills.length === 0" class="workflow-resource-empty">
              <t-icon name="tools" />
              <strong>当前沙箱没有可用 Skill</strong>
              <span>请安装并启用 Skill，等待状态变为就绪后再选择。</span>
              <button type="button" class="workflow-link-button" @click="emit('manage-skills')">
                管理技能
              </button>
            </div>

            <label v-if="toolKind !== 'skill'" class="workflow-field">
              <span>参数 JSON</span>
              <textarea
                :value="toolArgumentsText"
                :disabled="disabled"
                rows="7"
                :placeholder="toolArgumentPlaceholder"
                @input="updateToolArguments(inputValue($event))"
              />
              <small v-if="jsonFieldErrors.arguments" class="workflow-field-error">{{ jsonFieldErrors.arguments }}</small>
              <small v-else-if="toolArgumentFields.length">
                这个工具需要的参数：<code v-for="field in toolArgumentFields" :key="field.name">{{ field.label }}</code>
              </small>
              <small v-else>
                {{ toolArgumentHint }}
              </small>
            </label>
            <label v-else class="workflow-field">
              <span>任务模板 *</span>
              <textarea
                :value="configString('task_template')"
                :disabled="disabled"
                rows="7"
                placeholder="请根据 {{input.query}} 完成任务，并返回可交付结果。"
                @input="updateConfig('task_template', inputValue($event))"
              />
            </label>
          </template>

          <template v-else-if="selectedNode.data.workflowType === 'end'">
            <label class="workflow-field">
              <span>最终输出模板</span>
              <textarea
                :value="configString('text_template')"
                :disabled="disabled"
                rows="6"
                :placeholder="endTemplatePlaceholder"
                @input="updateConfig('text_template', inputValue($event))"
              />
              <small>这里写的内容就是用户最终看到的回答；留空则沿用上一步的输出。</small>
            </label>
            <div v-if="upstreamNodeOptions.length" class="workflow-variable-picker">
              <span class="workflow-variable-picker-label">点击插入上一步的结果</span>
              <button
                v-for="option in upstreamNodeOptions"
                :key="option.value"
                type="button"
                class="workflow-variable-chip"
                :disabled="disabled"
                :title="option.value"
                @click="appendTemplateVariable('text_template', option.value)"
              >
                {{ option.label }}
              </button>
            </div>
          </template>

          <p v-if="selectedNode.data.workflowType === 'start'" class="workflow-inspector-note">
            用户输入使用 <code>input.query</code>；附件文本使用 <code>input.attachments_text</code>。
          </p>
        </template>

        <template v-else-if="selectedEdge">
          <div class="workflow-inspector-header">
            <div>
              <span class="workflow-inspector-kicker">连线属性</span>
              <h3>{{ selectedEdge.source }} → {{ selectedEdge.target }}</h3>
            </div>
            <button v-if="!disabled" type="button" class="workflow-icon-button workflow-icon-button--danger" title="删除连线" @click="removeSelectedEdge">
              <t-icon name="delete" />
            </button>
          </div>

          <label class="workflow-check-field">
            <input type="checkbox" :checked="selectedEdge.data?.is_default" :disabled="disabled" @change="updateEdgeDefault(($event.target as HTMLInputElement).checked)" />
            <span>默认分支</span>
          </label>
          <p class="workflow-field-help">当其他条件均不满足时走默认分支；一个节点最多只能有一条。</p>

          <template v-if="!selectedEdge.data?.is_default">
            <label class="workflow-field workflow-field--inline">
              <span>条件模式</span>
              <select :value="edgeConditionMode" :disabled="disabled" @change="updateEdgeConditionMode(inputValue($event))">
                <option value="all">全部满足</option>
                <option value="any">任一满足</option>
              </select>
            </label>
            <div v-for="(item, index) in edgeConditionItems" :key="index" class="workflow-condition-row">
              <select :value="item.variable" :disabled="disabled" @change="updateConditionItem(index, 'variable', inputValue($event))">
                <option v-for="variable in variableOptions" :key="variable" :value="variable">{{ variable }}</option>
              </select>
              <select :value="item.operator" :disabled="disabled" @change="updateConditionItem(index, 'operator', inputValue($event))">
                <option v-for="operator in conditionOperators" :key="operator.value" :value="operator.value">{{ operator.label }}</option>
              </select>
              <input :value="conditionValue(item.value)" :disabled="disabled" placeholder="比较值" @input="updateConditionItem(index, 'value', inputValue($event))" />
              <button v-if="!disabled" type="button" class="workflow-icon-button workflow-icon-button--small" title="删除条件" @click="removeConditionItem(index)">
                <t-icon name="close" />
              </button>
            </div>
            <button v-if="!disabled" type="button" class="workflow-link-button" @click="addConditionItem">
              <t-icon name="add" /> 添加条件
            </button>
          </template>
        </template>

        <div v-else class="workflow-inspector-empty">
          <t-icon name="edit-1" size="26px" />
          <strong>选择节点或连线</strong>
          <span>点击节点可配置必填项、变量和输出。</span>
          <span>点击连线可配置默认分支和路由条件。</span>
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
import { computed, nextTick, ref, watch } from 'vue';
import { Background } from '@vue-flow/background';
import { Controls } from '@vue-flow/controls';
import { MiniMap } from '@vue-flow/minimap';
import { Position, VueFlow, useVueFlow, type Connection } from '@vue-flow/core';
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
    type: 'smoothstep',
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
}

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

function nodeTypeLabel(type: WorkflowNodeType): string {
  return nodePalette.find((item) => item.type === type)?.label || type;
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

function onConnect(connection: Connection) {
  if (props.disabled || !connection.source || !connection.target) return;
  if (connection.source === connection.target) {
    showHint('一个步骤不能连到自己，请连到后面的步骤。');
    return;
  }
  if (flowEdges.value.some((edge) => edge.source === connection.source && edge.target === connection.target)) {
    showHint('这两个步骤已经连好了，不用再连一次。');
    return;
  }
  const order = flowEdges.value.filter((edge) => edge.source === connection.source).length;
  const edge: EditorEdge = {
    id: `edge-${Date.now()}-${order}`,
    source: connection.source,
    target: connection.target,
    type: 'smoothstep',
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
  const id = selectedNode.value.id;
  flowNodes.value = flowNodes.value.filter((node) => node.id !== id);
  flowEdges.value = flowEdges.value.filter((edge) => edge.source !== id && edge.target !== id);
  clearSelection();
  emitDefinition();
}

function removeSelectedEdge() {
  if (props.disabled || !selectedEdge.value) return;
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
}

.workflow-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.workflow-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

.workflow-subtitle,
.workflow-panel-hint,
.workflow-field-help {
  margin: 4px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.workflow-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.workflow-toolbar-button,
.workflow-icon-button,
.workflow-link-button {
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  cursor: pointer;
  transition: border-color .15s ease, color .15s ease, background .15s ease;
}

.workflow-toolbar-button,
.workflow-link-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 32px;
  padding: 0 10px;
  font-size: 12px;
}

.workflow-icon-button {
  display: inline-flex;
  width: 32px;
  height: 32px;
  align-items: center;
  justify-content: center;
  padding: 0;
}

.workflow-icon-button--small {
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
}

.workflow-toolbar-button:hover:not(:disabled),
.workflow-icon-button:hover:not(:disabled),
.workflow-link-button:hover:not(:disabled) {
  border-color: var(--td-brand-color);
  color: var(--td-brand-color);
}

.workflow-icon-button--danger:hover:not(:disabled) {
  border-color: var(--td-error-color);
  color: var(--td-error-color);
}

.workflow-toolbar-button:disabled,
.workflow-icon-button:disabled,
.workflow-link-button:disabled {
  cursor: not-allowed;
  opacity: .5;
}

.workflow-onboarding {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1px;
  margin-top: 12px;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: 7px;
  background: var(--td-component-stroke);
}

.workflow-onboarding-step {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
  padding: 9px 10px;
  background: var(--td-bg-color-secondarycontainer);
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
  font-size: 10px;
  font-weight: 600;
}

.workflow-onboarding-step strong,
.workflow-onboarding-step small {
  display: block;
}

.workflow-onboarding-step strong {
  font-size: 11px;
  line-height: 20px;
}

.workflow-onboarding-step small {
  margin-top: 1px;
  color: var(--td-text-color-secondary);
  font-size: 10px;
  line-height: 1.35;
}

.workflow-layout,
.workflow-templates {
  min-height: 0;
  flex: 1;
  margin-top: 16px;
}

.workflow-layout {
  display: grid;
  grid-template-columns: 182px minmax(420px, 1fr) 290px;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
}

/* --------------------------------------------------------------------------
   模板面板：让空画布状态先给出"可以选一个现成的"，而不是要求用户从零连线。
   -------------------------------------------------------------------------- */
.workflow-templates {
  display: flex;
  flex-direction: column;
  gap: 14px;
  overflow-y: auto;
  padding-right: 2px;
}

.workflow-templates-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.workflow-templates-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}

.workflow-templates-subtitle {
  max-width: 62ch;
  margin: 4px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}

.workflow-template-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 12px;
  align-content: start;
}

.workflow-template-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  text-align: left;
  cursor: pointer;
  transition: border-color 160ms ease-out, box-shadow 160ms ease-out, transform 160ms ease-out;
}

.workflow-template-card:hover:not(:disabled) {
  border-color: var(--td-brand-color);
  box-shadow: 0 4px 14px rgba(15, 18, 22, 0.08);
  transform: translateY(-1px);
}

.workflow-template-card:active:not(:disabled) {
  transform: scale(0.99);
}

.workflow-template-card--active {
  border-color: var(--td-brand-color);
  box-shadow: 0 0 0 1px var(--td-brand-color) inset;
}

.workflow-template-card:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.workflow-template-card-head {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.workflow-template-icon {
  display: inline-flex;
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  align-items: center;
  justify-content: center;
  border-radius: 7px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-size: 15px;
}

.workflow-template-heading strong,
.workflow-template-heading small {
  display: block;
}

.workflow-template-heading strong {
  font-size: 13px;
}

.workflow-template-heading small {
  margin-top: 2px;
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 1.45;
}

/* 步骤条真实反映执行顺序，因此这里的有序列表承载信息而不是装饰。 */
.workflow-template-flow {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
  counter-reset: workflow-template-step;
}

.workflow-template-flow li {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 7px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 10px;
  counter-increment: workflow-template-step;
}

.workflow-template-flow li::before {
  content: counter(workflow-template-step);
  color: var(--td-brand-color);
  font-weight: 600;
}

.workflow-template-flow li + li::before {
  content: '→';
  margin-right: 2px;
  color: var(--td-text-color-placeholder);
  font-weight: 400;
}

.workflow-template-detail {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 1.6;
}

.workflow-template-needs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: auto;
  padding-top: 2px;
}

.workflow-template-needs-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--td-warning-color);
  font-size: 10px;
  font-weight: 600;
}

.workflow-template-need {
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--td-warning-color-light);
  color: var(--td-warning-color-8);
  font-size: 10px;
}

.workflow-template-action {
  color: var(--td-brand-color);
  font-size: 11px;
  font-weight: 600;
}

.workflow-palette,
.workflow-inspector {
  min-width: 0;
  overflow-y: auto;
  background: var(--td-bg-color-secondarycontainer);
}

.workflow-palette {
  padding: 16px 12px;
  border-right: 1px solid var(--td-component-stroke);
}

.workflow-panel-heading {
  font-size: 13px;
  font-weight: 600;
}

.workflow-palette-item {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  margin-top: 10px;
  padding: 10px 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  text-align: left;
  cursor: grab;
}

.workflow-palette-item:active {
  cursor: grabbing;
}

.workflow-palette-item:hover:not(:disabled) {
  border-color: var(--td-brand-color);
}

.workflow-palette-item:disabled {
  cursor: not-allowed;
  opacity: .48;
}

.workflow-palette-icon {
  display: inline-flex;
  width: 28px;
  height: 28px;
  align-items: center;
  justify-content: center;
  flex: 0 0 28px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  color: var(--td-brand-color);
}

.workflow-palette-item strong,
.workflow-palette-item small {
  display: block;
}

.workflow-palette-item strong {
  font-size: 12px;
  font-weight: 600;
}

.workflow-palette-item small {
  margin-top: 2px;
  color: var(--td-text-color-secondary);
  font-size: 10px;
  line-height: 1.3;
}

.workflow-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 18px;
  color: var(--td-text-color-secondary);
  font-size: 11px;
}

.workflow-legend-dot {
  width: 8px;
  height: 8px;
  margin: 3px 0 0 4px;
  border-radius: 50%;
}

.workflow-legend-dot--start { background: #2ba471; }
.workflow-legend-dot--end { background: #d54941; }

.workflow-canvas {
  position: relative;
  min-width: 0;
  min-height: 420px;
  background-color: #f8fafc;
  background-image: radial-gradient(#d7dee8 .8px, transparent .8px);
  background-size: 20px 20px;
}

.workflow-canvas--disabled {
  background-color: var(--td-bg-color-container);
}

.workflow-canvas :deep(.vue-flow) {
  width: 100%;
  height: 100%;
}

.workflow-canvas :deep(.vue-flow__node) {
  min-width: 148px;
  border: 1px solid #cbd5e1;
  border-radius: 7px;
  background: #fff;
  box-shadow: 0 2px 8px rgba(15, 23, 42, .08);
}

.workflow-canvas :deep(.vue-flow__node.selected) {
  border-color: var(--td-brand-color);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 18%, transparent), 0 2px 8px rgba(15, 23, 42, .08);
}

.workflow-canvas :deep(.workflow-flow-node--start) { border-top: 3px solid #2ba471; }
.workflow-canvas :deep(.workflow-flow-node--end) { border-top: 3px solid #d54941; }
.workflow-canvas :deep(.workflow-flow-node--llm) { border-top: 3px solid #0052d9; }
.workflow-canvas :deep(.workflow-flow-node--knowledge-retrieval) { border-top: 3px solid #165dff; }
.workflow-canvas :deep(.workflow-flow-node--llm-decision) { border-top: 3px solid #8e56dd; }
.workflow-canvas :deep(.workflow-flow-node--http-request) { border-top: 3px solid #d54941; }
.workflow-canvas :deep(.workflow-flow-node--tool) { border-top: 3px solid #ed7b2f; }

.workflow-canvas :deep(.vue-flow__node-default .vue-flow__handle) {
  width: 8px;
  height: 8px;
  border: 2px solid #fff;
  background: var(--td-brand-color);
}

.workflow-canvas :deep(.vue-flow__edge-textbg) {
  fill: var(--td-bg-color-container);
}

.workflow-placeholder-note {
  position: absolute;
  top: 10px;
  left: 50%;
  z-index: 3;
  display: flex;
  max-width: calc(100% - 120px);
  align-items: center;
  gap: 6px;
  padding: 6px 9px;
  transform: translateX(-50%);
  border: 1px solid color-mix(in srgb, var(--td-brand-color) 24%, var(--td-component-stroke));
  border-radius: 6px;
  background: color-mix(in srgb, var(--td-brand-color) 7%, var(--td-bg-color-container));
  color: var(--td-text-color-secondary);
  font-size: 10px;
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
  gap: 6px;
  transform: translate(-50%, -50%);
  color: var(--td-text-color-secondary);
  pointer-events: none;
}

.workflow-canvas-empty strong {
  color: var(--td-text-color-primary);
  font-size: 13px;
}

.workflow-canvas-empty span {
  font-size: 11px;
}

.workflow-inspector {
  padding: 16px;
  border-left: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.workflow-inspector-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 16px;
}

.workflow-inspector-kicker {
  color: var(--td-brand-color);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: .04em;
}

/* 条件变量和输出模板都要求写 nodes.<节点 ID>，所以把 ID 直接摆出来。 */
.workflow-node-id {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 190px;
  margin-top: 4px;
  padding: 2px 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  cursor: pointer;
  transition: border-color 160ms ease-out, color 160ms ease-out;
}

.workflow-node-id:hover {
  border-color: var(--td-brand-color);
  color: var(--td-brand-color);
}

.workflow-variable-picker {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-bottom: 13px;
}

.workflow-variable-picker-label {
  width: 100%;
  color: var(--td-text-color-secondary);
  font-size: 10px;
}

.workflow-variable-chip {
  padding: 3px 8px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 999px;
  background: transparent;
  color: var(--td-brand-color);
  font-size: 10px;
  cursor: pointer;
  transition: border-color 160ms ease-out, background 160ms ease-out;
}

.workflow-variable-chip:hover:not(:disabled) {
  border-style: solid;
  border-color: var(--td-brand-color);
  background: var(--td-brand-color-light);
}

.workflow-variable-chip:disabled {
  cursor: not-allowed;
  opacity: .5;
}

.workflow-inspector-header h3 {
  max-width: 210px;
  margin: 3px 0 0;
  overflow: hidden;
  font-size: 15px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workflow-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  margin-bottom: 13px;
  font-size: 12px;
}

.workflow-field > span,
.workflow-check-field span {
  font-weight: 500;
}

.workflow-field input,
.workflow-field textarea,
.workflow-field select,
.workflow-condition-row input,
.workflow-condition-row select {
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: 5px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font: inherit;
}

.workflow-field input,
.workflow-field select,
.workflow-condition-row input,
.workflow-condition-row select {
  height: 32px;
  padding: 0 8px;
}

.workflow-field textarea {
  min-height: 72px;
  padding: 8px;
  resize: vertical;
  line-height: 1.45;
}

.workflow-field select[multiple] {
  height: 100px;
  padding: 5px;
}

.workflow-field input:focus,
.workflow-field textarea:focus,
.workflow-field select:focus,
.workflow-condition-row input:focus,
.workflow-condition-row select:focus {
  border-color: var(--td-brand-color);
  outline: 2px solid color-mix(in srgb, var(--td-brand-color) 15%, transparent);
}

.workflow-field small {
  color: var(--td-text-color-secondary);
  font-size: 10px;
  line-height: 1.4;
}

.workflow-field--inline {
  display: grid;
  grid-template-columns: 1fr 1fr;
  align-items: center;
}

.workflow-field--inline > span {
  align-self: center;
}

.workflow-node-type-badge {
  display: inline-flex;
  margin-bottom: 15px;
  padding: 3px 7px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 10px;
}

.workflow-node-type-badge--start { color: #16855b; }
.workflow-node-type-badge--llm { color: #0052d9; }
.workflow-node-type-badge--end { color: #b52a25; }

.workflow-node-help {
  display: grid;
  gap: 7px;
  margin-bottom: 15px;
  padding: 9px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  font-size: 10px;
  line-height: 1.45;
}

.workflow-node-help > div {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr);
  gap: 7px;
}

.workflow-node-help strong {
  color: var(--td-text-color-primary);
  font-weight: 600;
}

.workflow-node-help span,
.workflow-node-help code {
  min-width: 0;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
}

.workflow-resource-empty {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
  margin-bottom: 13px;
  padding: 10px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 10px;
  line-height: 1.45;
}

.workflow-resource-empty strong {
  color: var(--td-text-color-primary);
  font-size: 11px;
}

.workflow-field-error {
  color: var(--td-error-color) !important;
}

/* 规则性提醒（例如认证头限制），是"提前告知"而不是"已经出错"。 */
.workflow-field-warning {
  color: var(--td-warning-color) !important;
}

.workflow-inspector-note {
  margin: 12px 0;
  padding: 10px;
  border-left: 3px solid var(--td-brand-color);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 1.5;
}

.workflow-check-field {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.workflow-check-field input {
  accent-color: var(--td-brand-color);
}

.workflow-condition-row {
  display: grid;
  grid-template-columns: minmax(84px, 1.25fr) minmax(70px, .8fr) minmax(48px, .8fr) 28px;
  gap: 5px;
  margin-top: 8px;
}

.workflow-condition-row input,
.workflow-condition-row select {
  height: 29px;
  padding: 0 5px;
  font-size: 10px;
}

.workflow-link-button {
  margin-top: 10px;
  border-color: transparent;
  background: transparent;
  color: var(--td-brand-color);
  padding-left: 0;
}

.workflow-inspector-empty {
  display: flex;
  min-height: 180px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--td-text-color-secondary);
  text-align: center;
}

.workflow-inspector-empty strong {
  color: var(--td-text-color-primary);
  font-size: 13px;
}

.workflow-inspector-empty span {
  max-width: 180px;
  font-size: 11px;
  line-height: 1.5;
}

.workflow-validation {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-top: 10px;
  padding: 9px 11px;
  border: 1px solid color-mix(in srgb, var(--td-error-color) 35%, transparent);
  border-radius: 6px;
  background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
  color: var(--td-error-color);
  font-size: 12px;
}

.workflow-validation--success {
  border-color: color-mix(in srgb, var(--td-success-color) 35%, transparent);
  background: color-mix(in srgb, var(--td-success-color) 8%, transparent);
  color: var(--td-success-color);
}

.workflow-validation--error {
  border-color: color-mix(in srgb, var(--td-error-color) 35%, transparent);
  background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
  color: var(--td-error-color);
}

.workflow-validation--hint {
  border-color: color-mix(in srgb, var(--td-warning-color) 35%, transparent);
  background: color-mix(in srgb, var(--td-warning-color) 8%, transparent);
  color: var(--td-warning-color);
}

@media (max-width: 1180px) {
  .workflow-layout {
    grid-template-columns: 160px minmax(360px, 1fr) 260px;
  }
}

@media (max-width: 900px) {
  .workflow-onboarding {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .workflow-layout {
    grid-template-columns: 150px minmax(300px, 1fr);
  }

  .workflow-inspector {
    position: absolute;
    right: 12px;
    bottom: 12px;
    z-index: 5;
    width: 260px;
    max-height: 70%;
    border: 1px solid var(--td-component-stroke);
    box-shadow: 0 8px 24px rgba(15, 23, 42, .16);
  }
}

@media (max-width: 640px) {
  .workflow-toolbar {
    align-items: flex-start;
  }

  .workflow-toolbar-actions {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .workflow-onboarding {
    grid-template-columns: 1fr;
  }

  .workflow-layout {
    display: block;
    overflow: visible;
  }

  .workflow-palette {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 6px;
    border-right: 0;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .workflow-panel-heading,
  .workflow-panel-hint,
  .workflow-legend {
    grid-column: 1 / -1;
  }

  .workflow-palette-item {
    margin-top: 0;
  }

  .workflow-canvas {
    min-height: 440px;
  }

  .workflow-inspector {
    position: static;
    width: auto;
    max-height: none;
    border-top: 1px solid var(--td-component-stroke);
    border-left: 0;
    box-shadow: none;
  }
}
</style>
