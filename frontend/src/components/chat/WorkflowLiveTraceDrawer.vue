<template>
  <div
    class="workflow-trace-drawer"
    :class="{ 'is-open': visible }"
  >
    <!-- 遮罩层（仅在较小屏幕下生效） -->
    <div
      v-if="visible"
      class="workflow-trace-backdrop"
      @click="handleClose"
    />

    <aside class="workflow-trace-panel">
      <!-- 头部 -->
      <div class="workflow-trace-header">
        <div class="workflow-trace-header-left">
          <div class="workflow-trace-badge" :class="{ 'is-running': Boolean(activeNodeId) }">
            <span class="workflow-trace-badge-dot" />
            <span class="workflow-trace-badge-text">
              {{ activeNodeId ? '正在执行' : (completedNodeIds.length > 0 ? '执行完成' : '就绪') }}
            </span>
          </div>
          <h3 class="workflow-trace-title">{{ title || '工作流执行轨迹' }}</h3>
        </div>
        <div class="workflow-trace-header-actions">
          <button
            type="button"
            class="workflow-trace-icon-btn"
            title="自适应视野"
            @click="fitView"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3" />
            </svg>
          </button>
          <button
            type="button"
            class="workflow-trace-icon-btn"
            title="关闭面板"
            @click="handleClose"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>

      <!-- 进度指示 -->
      <div class="workflow-trace-progress-wrap">
        <div class="workflow-trace-progress-info">
          <span class="workflow-trace-progress-label">节点进度</span>
          <span class="workflow-trace-progress-count">
            {{ completedCount }} / {{ totalNodesCount }}
          </span>
        </div>
        <div class="workflow-trace-progress-bar">
          <div
            class="workflow-trace-progress-fill"
            :style="{ width: `${progressPercent}%` }"
          />
        </div>
      </div>

      <!-- 画布容器 -->
      <div class="workflow-trace-canvas">
        <VueFlow
          v-if="flowNodes.length > 0"
          :nodes="flowNodes"
          :edges="flowEdges"
          :nodes-draggable="false"
          :nodes-connectable="false"
          :zoom-on-scroll="true"
          :pan-on-drag="true"
          :prevent-scrolling="false"
          :fit-view-on-init="true"
          class="workflow-trace-vueflow"
        >
          <template #node-default="{ id, data }">
            <div
              class="trace-node-card"
              :class="[
                `trace-node-card--${data.workflowType}`,
                `is-${getNodeState(id)}`
              ]"
            >
              <Handle id="top" type="target" :position="Position.Top" :connectable="false" class="trace-node-handle" />
              <Handle id="right" type="source" :position="Position.Right" :connectable="false" class="trace-node-handle" />
              <Handle id="bottom" type="source" :position="Position.Bottom" :connectable="false" class="trace-node-handle" />
              <Handle id="left" type="target" :position="Position.Left" :connectable="false" class="trace-node-handle" />

              <div class="trace-node-stripe" />
              <div class="trace-node-body">
                <div class="trace-node-icon-wrap">
                  <svg v-if="data.workflowType === 'start'" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polygon points="5 3 19 12 5 21 5 3" />
                  </svg>
                  <svg v-else-if="data.workflowType === 'end'" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <circle cx="12" cy="12" r="10" />
                    <polyline points="9 12 11 14 15 10" />
                  </svg>
                  <svg v-else-if="data.workflowType === 'llm'" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="m12 3-1.9 5.8a2 2 0 0 1-1.3 1.3L3 12l5.8 1.9a2 2 0 0 1 1.3 1.3L12 21l1.9-5.8a2 2 0 0 1 1.3-1.3L21 12l-5.8-1.9a2 2 0 0 1-1.3-1.3Z" />
                  </svg>
                  <svg v-else-if="data.workflowType === 'llm-decision'" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <circle cx="18" cy="18" r="3" />
                    <circle cx="6" cy="6" r="3" />
                    <path d="M18 6h-5a3 3 0 0 0-3 3v6" />
                    <line x1="6" y1="9" x2="6" y2="21" />
                  </svg>
                  <svg v-else-if="data.workflowType === 'knowledge-retrieval'" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1-2.5-2.5Z" />
                    <line x1="8" y1="7" x2="16" y2="7" />
                    <line x1="8" y1="11" x2="14" y2="11" />
                  </svg>
                  <svg v-else-if="data.workflowType === 'http-request'" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <circle cx="12" cy="12" r="10" />
                    <line x1="2" y1="12" x2="22" y2="12" />
                    <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
                  </svg>
                  <svg v-else viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
                  </svg>
                </div>
                <div class="trace-node-info">
                  <span class="trace-node-name" :title="data.name">{{ data.name }}</span>
                  <span class="trace-node-type">{{ nodeTypeLabel(data.workflowType) }}</span>
                </div>
                <div class="trace-node-status">
                  <span v-if="getNodeState(id) === 'running'" class="trace-status-spinner">
                    <svg class="trace-spin" viewBox="0 0 24 24" width="14" height="14" fill="none">
                      <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" stroke-dasharray="32" stroke-linecap="round" />
                    </svg>
                  </span>
                  <span v-else-if="getNodeState(id) === 'success'" class="trace-status-success">
                    <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  </span>
                  <span v-else-if="getNodeState(id) === 'failed'" class="trace-status-failed">
                    <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                      <line x1="18" y1="6" x2="6" y2="18" />
                      <line x1="6" y1="6" x2="18" y2="18" />
                    </svg>
                  </span>
                  <span v-else class="trace-status-idle" />
                </div>
              </div>
            </div>
          </template>
          <Background pattern-color="#e2e8f0" :gap="16" />
          <Controls position="bottom-left" :show-interactive="false" />
        </VueFlow>
        <div v-else class="workflow-trace-empty">
          <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="#94a3b8" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="14" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
          </svg>
          <span>正在加载工作流结构...</span>
        </div>
      </div>

      <!-- 底部当前节点动态卡片 -->
      <div v-if="activeNodeDetails || completedNodeIds.length > 0" class="workflow-trace-footer">
        <div class="workflow-trace-status-card">
          <div class="trace-status-header">
            <span class="trace-status-indicator" :class="{ 'is-running': Boolean(activeNodeId) }" />
            <strong>{{ activeNodeDetails ? `当前：${activeNodeDetails.name}` : '工作流运行完成' }}</strong>
          </div>
          <p class="trace-status-desc">
            {{ activeNodeDetails ? `${nodeTypeLabel(activeNodeDetails.type)} 正在处理任务...` : '所有节点已顺序执行完成，回答已呈现。' }}
          </p>
        </div>
      </div>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { VueFlow, useVueFlow, Handle, Position } from '@vue-flow/core';
import { Background } from '@vue-flow/background';
import { Controls } from '@vue-flow/controls';
import type { Node, Edge } from '@vue-flow/core';
import type { WorkflowDefinition, WorkflowNodeType } from '@/api/agent';
import '@vue-flow/core/dist/style.css';
import '@vue-flow/core/dist/theme-default.css';
import '@vue-flow/controls/dist/style.css';

const props = withDefaults(
  defineProps<{
    visible: boolean;
    title?: string;
    definition?: WorkflowDefinition | null;
    activeNodeId?: string | null;
    completedNodeIds?: string[];
    failedNodeIds?: string[];
  }>(),
  {
    visible: false,
    title: '',
    definition: null,
    activeNodeId: null,
    completedNodeIds: () => [],
    failedNodeIds: () => [],
  }
);

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void;
}>();

const { fitView: vueFlowFitView, setCenter } = useVueFlow();

const handleClose = () => {
  emit('update:visible', false);
};

const fitView = () => {
  nextTick(() => {
    vueFlowFitView({ padding: 0.25, duration: 400 });
  });
};

const getNodeState = (nodeId: string): 'running' | 'success' | 'failed' | 'idle' => {
  if (props.activeNodeId === nodeId) return 'running';
  if (props.failedNodeIds.includes(nodeId)) return 'failed';
  if (props.completedNodeIds.includes(nodeId)) return 'success';
  return 'idle';
};

const nodeTypeIcon = (type?: WorkflowNodeType): string => {
  switch (type) {
    case 'start': return 'play-circle';
    case 'end': return 'check-circle';
    case 'llm': return 'chat';
    case 'llm-decision': return 'fork';
    case 'knowledge-retrieval': return 'data-search';
    case 'http-request': return 'internet';
    case 'tool': return 'tools';
    default: return 'flow';
  }
};

const nodeTypeLabel = (type?: WorkflowNodeType): string => {
  switch (type) {
    case 'start': return '开始节点';
    case 'end': return '结束节点';
    case 'llm': return '大模型处理';
    case 'llm-decision': return '路由判断';
    case 'knowledge-retrieval': return '知识库检索';
    case 'http-request': return 'HTTP请求';
    case 'tool': return '工具执行';
    default: return '处理节点';
  }
};

const flowNodes = computed<Node[]>(() => {
  if (!props.definition?.nodes) return [];
  return props.definition.nodes.map(node => ({
    id: node.id,
    type: 'default',
    position: { x: node.position.x, y: node.position.y },
    data: {
      name: node.name,
      workflowType: node.type,
      config: node.config,
    },
  }));
});

const flowEdges = computed<Edge[]>(() => {
  if (!props.definition?.edges) return [];
  return props.definition.edges.map(edge => {
    const isSourceDone = props.completedNodeIds.includes(edge.source);
    const isTargetActive = props.activeNodeId === edge.target || props.completedNodeIds.includes(edge.target);
    const isLive = isSourceDone && isTargetActive;
    return {
      id: edge.id,
      source: edge.source,
      target: edge.target,
      sourceHandle: edge.source_handle || undefined,
      targetHandle: edge.target_handle || undefined,
      animated: isLive,
      style: {
        stroke: isLive ? '#0052d9' : (isSourceDone ? '#10b981' : '#cbd5e1'),
        strokeWidth: isLive ? 2.5 : 1.5,
      },
    };
  });
});

const totalNodesCount = computed(() => props.definition?.nodes?.length || 0);
const completedCount = computed(() => {
  if (!props.definition?.nodes) return 0;
  return props.completedNodeIds.length;
});
const progressPercent = computed(() => {
  if (totalNodesCount.value === 0) return 0;
  return Math.min(100, Math.round((completedCount.value / totalNodesCount.value) * 100));
});

const activeNodeDetails = computed(() => {
  if (!props.activeNodeId || !props.definition?.nodes) return null;
  return props.definition.nodes.find(n => n.id === props.activeNodeId) || null;
});

// 监听活跃节点变动，平滑聚焦
watch(
  () => props.activeNodeId,
  (newId) => {
    if (newId && props.visible && props.definition?.nodes) {
      const target = props.definition.nodes.find(n => n.id === newId);
      if (target) {
        nextTick(() => {
          setCenter(target.position.x + 90, target.position.y + 35, { duration: 600, zoom: 0.95 });
        });
      }
    }
  }
);

// 展开时自适应
watch(
  () => props.visible,
  (isOpen) => {
    if (isOpen) {
      fitView();
    }
  }
);
</script>

<style scoped>
.workflow-trace-drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 1200;
  pointer-events: none;
}

.workflow-trace-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.25);
  backdrop-filter: blur(2px);
  pointer-events: auto;
  opacity: 0;
  animation: fadeInBackdrop 0.25s forwards;
}

@keyframes fadeInBackdrop {
  to { opacity: 1; }
}

.workflow-trace-panel {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: 420px;
  max-width: 90vw;
  background: #ffffff;
  border-left: 1px solid #e2e8f0;
  box-shadow: -8px 0 32px rgba(15, 23, 42, 0.08);
  display: flex;
  flex-direction: column;
  transform: translateX(100%);
  transition: transform 0.35s cubic-bezier(0.16, 1, 0.3, 1);
  pointer-events: auto;
  z-index: 1210;
}

.workflow-trace-drawer.is-open .workflow-trace-panel {
  transform: translateX(0);
}

.workflow-trace-header {
  padding: 18px 20px 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid #f1f5f9;
}

.workflow-trace-header-left {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.workflow-trace-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  font-weight: 600;
  color: #64748b;
}

.workflow-trace-badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #94a3b8;
}

.workflow-trace-badge.is-running {
  color: #0052d9;
}

.workflow-trace-badge.is-running .workflow-trace-badge-dot {
  background: #0052d9;
  box-shadow: 0 0 0 3px rgba(0, 82, 217, 0.25);
  animation: tracePulse 1.6s infinite;
}

@keyframes tracePulse {
  0% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(0, 82, 217, 0.5); }
  70% { transform: scale(1.1); box-shadow: 0 0 0 6px rgba(0, 82, 217, 0); }
  100% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(0, 82, 217, 0); }
}

.workflow-trace-title {
  font-size: 16px;
  font-weight: 700;
  color: #0f172a;
  margin: 0;
  letter-spacing: -0.01em;
}

.workflow-trace-header-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

.workflow-trace-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 6px;
  border: 1px solid transparent;
  background: transparent;
  color: #64748b;
  cursor: pointer;
  transition: all 0.15s ease;
}

.workflow-trace-icon-btn:hover {
  background: #f1f5f9;
  color: #0f172a;
}

.workflow-trace-progress-wrap {
  padding: 10px 20px 12px;
  background: #f8fafc;
  border-bottom: 1px solid #f1f5f9;
}

.workflow-trace-progress-info {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: #64748b;
  margin-bottom: 6px;
}

.workflow-trace-progress-bar {
  height: 4px;
  border-radius: 999px;
  background: #e2e8f0;
  overflow: hidden;
}

.workflow-trace-progress-fill {
  height: 100%;
  background: linear-gradient(90deg, #0052d9, #10b981);
  border-radius: 999px;
  transition: width 0.4s ease;
}

.workflow-trace-canvas {
  flex: 1;
  position: relative;
  background: #fdfdfd;
}

.workflow-trace-vueflow {
  width: 100%;
  height: 100%;
}

.workflow-trace-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 12px;
  color: #94a3b8;
  font-size: 13px;
}

/* 节点卡片定制 */
.trace-node-card {
  position: relative;
  width: 180px;
  border-radius: 8px;
  background: #ffffff;
  border: 1px solid #cbd5e1;
  box-shadow: 0 2px 8px rgba(15, 23, 42, 0.05);
  transition: all 0.25s ease;
}

.trace-node-handle {
  width: 4px;
  height: 4px;
  opacity: 0;
  pointer-events: none;
  border: none;
  background: transparent;
}

.trace-node-stripe {
  height: 3px;
  border-radius: 8px 8px 0 0;
  background: #94a3b8;
}

.trace-node-card--start .trace-node-stripe { background: #059669; }
.trace-node-card--llm .trace-node-stripe { background: #0052d9; }
.trace-node-card--knowledge-retrieval .trace-node-stripe { background: #0284c7; }
.trace-node-card--llm-decision .trace-node-stripe { background: #7c3aed; }
.trace-node-card--http-request .trace-node-stripe { background: #e11d48; }
.trace-node-card--tool .trace-node-stripe { background: #d97706; }
.trace-node-card--end .trace-node-stripe { background: #0d9488; }

.trace-node-body {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
}

.trace-node-icon-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 6px;
  background: #f1f5f9;
  color: #475569;
  font-size: 13px;
  flex-shrink: 0;
}

.trace-node-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.trace-node-name {
  font-size: 12px;
  font-weight: 600;
  color: #1e293b;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.trace-node-type {
  font-size: 10px;
  color: #94a3b8;
}

/* 节点状态样式 */
.trace-node-card.is-running {
  border-color: #0052d9;
  box-shadow: 0 0 0 2px rgba(0, 82, 217, 0.25), 0 8px 20px rgba(0, 82, 217, 0.15);
  animation: nodeCardGlow 1.8s infinite alternate;
}

@keyframes nodeCardGlow {
  from { box-shadow: 0 0 0 2px rgba(0, 82, 217, 0.2), 0 4px 12px rgba(0, 82, 217, 0.1); }
  to { box-shadow: 0 0 0 4px rgba(0, 82, 217, 0.4), 0 8px 24px rgba(0, 82, 217, 0.25); }
}

.trace-node-card.is-success {
  border-color: #10b981;
  background: #fcfdfc;
}

.trace-node-card.is-failed {
  border-color: #ef4444;
  background: #fffafa;
}

.trace-node-card.is-idle {
  opacity: 0.82;
}

.trace-node-status {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  flex-shrink: 0;
}

.trace-spin {
  animation: spin 1s linear infinite;
  color: #0052d9;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.trace-status-success {
  color: #10b981;
}

.trace-status-failed {
  color: #ef4444;
}

.trace-status-idle {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #cbd5e1;
}

.workflow-trace-footer {
  padding: 14px 18px;
  border-top: 1px solid #f1f5f9;
  background: #ffffff;
}

.workflow-trace-status-card {
  padding: 10px 12px;
  border-radius: 8px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
}

.trace-status-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: #0f172a;
}

.trace-status-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #10b981;
}

.trace-status-indicator.is-running {
  background: #0052d9;
  box-shadow: 0 0 0 3px rgba(0, 82, 217, 0.25);
  animation: tracePulse 1.6s infinite;
}

.trace-status-desc {
  margin: 4px 0 0 16px;
  font-size: 12px;
  color: #64748b;
}
</style>
