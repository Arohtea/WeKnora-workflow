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
            <span class="workflow-trace-badge-text">{{ statusLabel }}</span>
          </div>
          <h3 class="workflow-trace-title">{{ title || '工作流执行轨迹' }}</h3>
        </div>
        <div class="workflow-trace-header-actions">
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

      <!-- 进度指示与视图切换 -->
      <div class="workflow-trace-progress-wrap">
        <div class="workflow-trace-progress-info">
          <span class="workflow-trace-progress-label">节点进度</span>
          <span class="workflow-trace-progress-count">
            <span class="count-main">{{ completedCount }} / {{ effectiveTotalCount }}</span>
            <span v-if="skippedCount > 0 && viewMode === 'all'" class="count-skipped-tip">
              ({{ skippedCount }} 个分支节点已跳过)
            </span>
          </span>
        </div>
        <div class="workflow-trace-progress-bar">
          <div
            class="workflow-trace-progress-fill"
            :style="{ width: `${progressPercent}%` }"
          />
        </div>

        <!-- 视图切换：当存在跳过分支时展示分段控制器，默认「执行路径」 -->
        <div v-if="skippedCount > 0" class="workflow-trace-filter-wrap">
          <div class="workflow-trace-segmented">
            <button
              type="button"
              class="segmented-btn"
              :class="{ 'is-active': viewMode === 'executed' }"
              @click="viewMode = 'executed'"
            >
              <span>执行路径</span>
              <span class="segmented-badge">{{ executedCount }}</span>
            </button>
            <button
              type="button"
              class="segmented-btn"
              :class="{ 'is-active': viewMode === 'all' }"
              @click="viewMode = 'all'"
            >
              <span>完整流程</span>
              <span class="segmented-badge">{{ allStepsCount }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- 纵向步骤时间线 -->
      <div ref="timelineEl" class="workflow-trace-canvas">
        <ol v-if="steps.length > 0" class="trace-timeline">
          <li
            v-for="(step, index) in steps"
            :key="step.id"
            class="trace-step"
            :class="[`is-${step.state}`, `trace-step--${step.type || 'unknown'}`]"
          >
            <!-- 状态节点：时间线的圆点 -->
            <span class="trace-step-dot" aria-hidden="true">
              <svg
                v-if="step.state === 'running'"
                class="trace-spin"
                viewBox="0 0 24 24"
                width="12"
                height="12"
                fill="none"
              >
                <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" stroke-dasharray="32" stroke-linecap="round" />
              </svg>
              <svg v-else-if="step.state === 'success'" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12" />
              </svg>
              <svg v-else-if="step.state === 'failed'" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
              <!-- 分支跳过图标：虚线细圆环配居中线，语义明确且符合 Lucide 标准 -->
              <svg v-else-if="step.state === 'skipped'" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
                <circle cx="12" cy="12" r="8" stroke-dasharray="3 2" opacity="0.8" />
                <line x1="8.5" y1="12" x2="15.5" y2="12" stroke-width="2.2" />
              </svg>
              <span v-else class="trace-step-index">{{ step.displayIndex || (index + 1) }}</span>
            </span>

            <!-- 步骤卡片 -->
            <div class="trace-step-card" :title="step.state === 'skipped' ? '该分支未被路由条件命中，未执行' : step.name">
              <div class="trace-step-icon-wrap">
                <svg v-if="step.type === 'start'" viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <polygon points="5 3 19 12 5 21 5 3" />
                </svg>
                <svg v-else-if="step.type === 'end'" viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="12" cy="12" r="10" />
                  <polyline points="9 12 11 14 15 10" />
                </svg>
                <svg v-else-if="step.type === 'llm'" viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="m12 3-1.9 5.8a2 2 0 0 1-1.3 1.3L3 12l5.8 1.9a2 2 0 0 1 1.3 1.3L12 21l1.9-5.8a2 2 0 0 1 1.3-1.3L21 12l-5.8-1.9a2 2 0 0 1-1.3-1.3Z" />
                </svg>
                <svg v-else-if="step.type === 'llm-decision'" viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="18" cy="18" r="3" />
                  <circle cx="6" cy="6" r="3" />
                  <path d="M18 6h-5a3 3 0 0 0-3 3v6" />
                  <line x1="6" y1="9" x2="6" y2="21" />
                </svg>
                <svg v-else-if="step.type === 'knowledge-retrieval'" viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1-2.5-2.5Z" />
                  <line x1="8" y1="7" x2="16" y2="7" />
                  <line x1="8" y1="11" x2="14" y2="11" />
                </svg>
                <svg v-else-if="step.type === 'http-request'" viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="12" cy="12" r="10" />
                  <line x1="2" y1="12" x2="22" y2="12" />
                  <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
                </svg>
                <svg v-else viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
                </svg>
              </div>

              <div class="trace-step-info">
                <span class="trace-step-name" :title="step.name">{{ step.name }}</span>
                <span class="trace-step-type">{{ nodeTypeLabel(step.type) }}</span>
              </div>

              <span v-if="step.state === 'running'" class="trace-step-tag">执行中</span>
              <span v-else-if="step.state === 'failed'" class="trace-step-tag is-failed">失败</span>
              <span v-else-if="step.state === 'skipped'" class="trace-step-tag is-skipped">已跳过分支</span>
            </div>
          </li>
        </ol>
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
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import type { WorkflowDefinition, WorkflowNode, WorkflowNodeType } from '@/api/agent';

type StepState = 'running' | 'success' | 'failed' | 'idle' | 'skipped';

interface TraceStep {
  id: string;
  name: string;
  type?: WorkflowNodeType;
  state: StepState;
  displayIndex?: number;
}

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

const timelineEl = ref<HTMLElement | null>(null);

/**
 * 视图过滤模式：
 * - 'executed': 仅展示实际执行路径上的节点（默认，隐藏未进行的分支节点）
 * - 'all': 展示完整流程图谱中的全部节点（未进行的分支节点带明显虚线淡化与跳过标识）
 */
const viewMode = ref<'executed' | 'all'>('executed');

const handleClose = () => {
  emit('update:visible', false);
};

const nodeById = computed(() => {
  const map = new Map<string, WorkflowNode>();
  props.definition?.nodes?.forEach((node) => map.set(node.id, node));
  return map;
});

/**
 * 全量节点状态归一化列表（包含分支可达性分析）：
 * - 已执行节点按实际流式事件到达顺序排列在前；
 * - 若工作流已执行完毕（无 activeNodeId 且已有执行记录或到达 end 节点），所有未执行节点 100% 确定为已跳过的分支 (skipped)；
 * - 若工作流正在执行中，从 activeNodeId 进行拓扑出边可达性分析：可达节点为排队中 (idle)，不可达节点判定为已跳过分支 (skipped)。
 */
const allSteps = computed<TraceStep[]>(() => {
  const seen = new Set<string>();
  const list: TraceStep[] = [];

  const push = (id: string | null | undefined, state: StepState) => {
    if (!id) return;
    const existing = list.find((step) => step.id === id);
    if (existing) {
      existing.state = state;
      return;
    }
    seen.add(id);
    const node = nodeById.value.get(id);
    list.push({ id, name: node?.name || id, type: node?.type, state });
  };

  // 1. 先推入真实执行记录与当前正在执行的节点
  props.completedNodeIds.forEach((id) => push(id, 'success'));
  props.failedNodeIds.forEach((id) => push(id, 'failed'));
  if (props.activeNodeId) {
    push(props.activeNodeId, 'running');
  }

  // 2. 判断工作流是否已处于终止态
  const hasReachedEndNode = props.completedNodeIds.some(
    (id) => nodeById.value.get(id)?.type === 'end'
  );
  const isFinished = !props.activeNodeId && (
    hasReachedEndNode ||
    props.completedNodeIds.length > 0 ||
    props.failedNodeIds.length > 0
  );

  // 3. 构建拓扑出边映射，用于执行中的可达性推导
  const outgoingEdges = new Map<string, string[]>();
  (props.definition?.edges || []).forEach((edge) => {
    if (!outgoingEdges.has(edge.source)) outgoingEdges.set(edge.source, []);
    outgoingEdges.get(edge.source)!.push(edge.target);
  });

  const reachableFromActive = new Set<string>();
  if (props.activeNodeId) {
    const queue = [props.activeNodeId];
    while (queue.length > 0) {
      const curr = queue.shift()!;
      const targets = outgoingEdges.get(curr) || [];
      for (const target of targets) {
        if (!reachableFromActive.has(target) && !seen.has(target)) {
          reachableFromActive.add(target);
          queue.push(target);
        }
      }
    }
  }

  // 4. 处理剩余尚未执行的节点
  const remainingNodes = (props.definition?.nodes || [])
    .filter((node) => !seen.has(node.id))
    .sort((a, b) => a.position.y - b.position.y || a.position.x - b.position.x);

  remainingNodes.forEach((node) => {
    let state: StepState = 'idle';
    if (isFinished) {
      // 已经执行完成，未跑过的节点全部属于未命中的分支
      state = 'skipped';
    } else if (props.activeNodeId) {
      // 正在执行中，不在活跃节点下游候选链上的节点属于已跳过分支
      if (!reachableFromActive.has(node.id)) {
        state = 'skipped';
      } else {
        state = 'idle';
      }
    }
    push(node.id, state);
  });

  // 5. 为真实有效链路上的节点赋予连续递增的序号，跳过节点不占位
  let effectiveSeq = 1;
  list.forEach((step) => {
    if (step.state !== 'skipped') {
      step.displayIndex = effectiveSeq++;
    }
  });

  return list;
});

/** 所有节点总数 */
const allStepsCount = computed(() => allSteps.value.length);

/** 过滤出本次实际执行路径上的有效节点（剔除未走的分支节点） */
const executedSteps = computed(() =>
  allSteps.value.filter((step) => step.state !== 'skipped')
);

/** 实际执行路径上的节点数 */
const executedCount = computed(() => executedSteps.value.length);

/** 本次运行被跳过的分支节点总数 */
const skippedCount = computed(
  () => allSteps.value.filter((step) => step.state === 'skipped').length
);

/** 最终渲染的时间线步骤列表（根据视图切换决定是否展示已跳过的分支节点） */
const steps = computed(() => {
  if (viewMode.value === 'executed') {
    return executedSteps.value;
  }
  return allSteps.value;
});

const statusLabel = computed(() => {
  if (props.activeNodeId) return '正在执行';
  if (props.completedNodeIds.length > 0) return '执行完成';
  return '就绪';
});

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

/**
 * 真实有效执行路径的总节点数（分母）：
 * 避免分支工作流中将未激活分支算入总数导致「明明已执行完成但进度只有 6/10」的问题。
 */
const effectiveTotalCount = computed(() => executedCount.value);

const completedCount = computed(
  () => executedSteps.value.filter((step) => step.state === 'success' || step.state === 'failed').length
);

const progressPercent = computed(() => {
  if (effectiveTotalCount.value === 0) return 0;
  return Math.min(100, Math.round((completedCount.value / effectiveTotalCount.value) * 100));
});

// 时间线随执行推进，自动把当前节点滚入视野
watch(
  () => props.activeNodeId,
  async (id) => {
    if (!id || !props.visible) return;
    await nextTick();
    timelineEl.value?.querySelector('.is-running')?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
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
  min-width: 0;
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
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workflow-trace-header-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
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

.count-skipped-tip {
  font-size: 11px;
  color: #94a3b8;
  font-weight: 500;
  margin-left: 4px;
}

.workflow-trace-filter-wrap {
  margin-top: 10px;
}

.workflow-trace-segmented {
  display: flex;
  background: #f1f5f9;
  border-radius: 8px;
  padding: 2.5px;
  gap: 2px;
}

.segmented-btn {
  flex: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 4px 8px;
  font-size: 11px;
  font-weight: 500;
  color: #64748b;
  background: transparent;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.16, 1, 0.3, 1);
  user-select: none;
}

.segmented-btn:hover {
  color: #0f172a;
}

.segmented-btn.is-active {
  background: #ffffff;
  color: #0f172a;
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.08), 0 1px 2px rgba(15, 23, 42, 0.04);
}

.segmented-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 600;
  background: rgba(148, 163, 184, 0.2);
  color: #64748b;
  transition: all 0.2s ease;
}

.segmented-btn.is-active .segmented-badge {
  background: rgba(0, 82, 217, 0.1);
  color: #0052d9;
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
  overflow-y: auto;
  overscroll-behavior: contain;
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

/* ===== 纵向步骤时间线 ===== */
.trace-timeline {
  list-style: none;
  margin: 0;
  padding: 16px 18px 24px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.trace-step {
  position: relative;
  padding-left: 32px;
  /* 节点类型主色，卡片左侧色条与图标统一取色 */
  --trace-accent: #94a3b8;
}

.trace-step--start { --trace-accent: #059669; }
.trace-step--end { --trace-accent: #0d9488; }
.trace-step--llm { --trace-accent: #0052d9; }
.trace-step--llm-decision { --trace-accent: #7c3aed; }
.trace-step--knowledge-retrieval { --trace-accent: #0284c7; }
.trace-step--http-request { --trace-accent: #e11d48; }
.trace-step--tool { --trace-accent: #d97706; }

/* 连接相邻步骤的竖线 */
.trace-step::before {
  content: '';
  position: absolute;
  left: 10px;
  top: 30px;
  height: calc(100% - 20px);
  width: 2px;
  border-radius: 999px;
  background: #e2e8f0;
  transition: background 0.25s ease;
}

.trace-step:last-child::before {
  display: none;
}

.trace-step.is-success::before {
  background: rgba(16, 185, 129, 0.45);
}

.trace-step.is-failed::before {
  background: rgba(239, 68, 68, 0.45);
}

.trace-step.is-skipped::before {
  background: transparent;
  border-left: 2px dashed #cbd5e1;
}

.trace-step-dot {
  position: absolute;
  left: 0;
  top: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #ffffff;
  border: 2px solid #e2e8f0;
  color: #94a3b8;
  z-index: 1;
  transition: all 0.25s ease;
}

.trace-step-index {
  font-size: 10px;
  font-weight: 600;
  line-height: 1;
}

.trace-step.is-success .trace-step-dot {
  border-color: #10b981;
  color: #10b981;
}

.trace-step.is-failed .trace-step-dot {
  border-color: #ef4444;
  color: #ef4444;
}

.trace-step.is-running .trace-step-dot {
  border-color: #0052d9;
  color: #0052d9;
  box-shadow: 0 0 0 3px rgba(0, 82, 217, 0.15);
}

.trace-step-card {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 8px;
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-left: 3px solid var(--trace-accent);
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.04);
  transition: all 0.25s ease;
}

.trace-step.is-idle .trace-step-card {
  background: #fbfcfd;
  box-shadow: none;
}

.trace-step.is-idle .trace-step-name {
  color: #64748b;
}

/* 跳过未执行分支：整体弱化透明度，虚线边框与中性灰点缀，层次极其分明 */
.trace-step.is-skipped {
  opacity: 0.62;
  transition: opacity 0.2s ease;
}

.trace-step.is-skipped:hover {
  opacity: 0.95;
}

.trace-step.is-skipped .trace-step-dot {
  border-color: #cbd5e1;
  border-style: dashed;
  background: #f8fafc;
  color: #94a3b8;
}

.trace-step.is-skipped .trace-step-card {
  background: #f8fafc;
  border: 1px dashed #cbd5e1;
  border-left: 3px solid #cbd5e1;
  box-shadow: none;
  transition: all 0.2s ease;
}

.trace-step.is-skipped:hover .trace-step-card {
  border-color: #94a3b8;
  border-left-color: #94a3b8;
  background: #ffffff;
}

.trace-step.is-skipped .trace-step-icon-wrap {
  background: #f1f5f9;
  color: #94a3b8;
}

.trace-step.is-skipped .trace-step-name {
  color: #64748b;
}

.trace-step.is-running .trace-step-card {
  animation: stepCardGlow 1.8s infinite alternate;
}

@keyframes stepCardGlow {
  from { box-shadow: 0 0 0 2px rgba(0, 82, 217, 0.2), 0 4px 12px rgba(0, 82, 217, 0.1); }
  to { box-shadow: 0 0 0 4px rgba(0, 82, 217, 0.4), 0 8px 24px rgba(0, 82, 217, 0.25); }
}

.trace-step.is-success .trace-step-card {
  background: #fcfdfc;
}

.trace-step.is-failed .trace-step-card {
  background: #fffafa;
  border-color: #fecaca;
  border-left-color: #ef4444;
}

.trace-step-icon-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 6px;
  background: #f1f5f9;
  color: var(--trace-accent);
  flex-shrink: 0;
}

.trace-step.is-idle .trace-step-icon-wrap {
  color: #94a3b8;
}

.trace-step-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.trace-step-name {
  font-size: 12px;
  font-weight: 600;
  color: #1e293b;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.trace-step-type {
  font-size: 10px;
  color: #94a3b8;
}

.trace-step-tag {
  flex-shrink: 0;
  padding: 1px 6px;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 600;
  color: #0052d9;
  background: rgba(0, 82, 217, 0.1);
}

.trace-step-tag.is-failed {
  color: #ef4444;
  background: rgba(239, 68, 68, 0.1);
}

.trace-step-tag.is-skipped {
  color: #64748b;
  background: #f1f5f9;
  border: 1px solid #e2e8f0;
}

.trace-spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
