import type {
  WorkflowDefinition,
  WorkflowNode,
  WorkflowEdge,
  WorkflowNodeType,
} from '@/api/agent';

/**
 * 工作流模板是给非技术用户准备的"起跑线"。
 *
 * 空画布要求用户先建立"输入 → 处理 → 输出"的心智模型，这一步最容易劝退。
 * 模板直接给出一个结构完整、可校验通过的图，用户只需要认得每个节点在做什么，
 * 再按提示补齐自己的知识库或接口即可。
 */

/** 模板引用某个"用户必须自己选择"的资源时使用的占位标记。 */
export interface WorkflowTemplateRequirement {
  /** 需要用户补齐的节点 ID。 */
  nodeId: string;
  /** 该节点上需要用户完成的动作，直接展示在预览里。 */
  label: string;
}

export interface WorkflowTemplate {
  /** 稳定标识，用于埋点与"重新选择模板"判断。 */
  id: string;
  /** 卡片标题，用用户能听懂的业务语言，而不是节点术语。 */
  name: string;
  /** 一句话说明这个流程替他做什么。 */
  summary: string;
  /** 展开后的详细说明，说明适用场景。 */
  detail: string;
  /** 关键组成部分，用于卡片上的流程预览。 */
  steps: string[];
  /** 生效前必须由用户补齐的资源。 */
  requirements: WorkflowTemplateRequirement[];
  /** 图标名（tdesign-icons）。 */
  icon: string;
  /** 按需生成定义，保证每次应用都得到一份独立副本。 */
  create: () => WorkflowDefinition;
}

const NODE_TYPE_LABELS: Record<WorkflowNodeType, string> = {
  start: '开始',
  'knowledge-retrieval': '知识库检索',
  llm: '大模型处理',
  'llm-decision': 'LLM 判断',
  'http-request': 'HTTP 请求',
  tool: '工具',
  end: '结束',
};

const BASE_VIEWPORT = { x: 0, y: 0, zoom: 1 };

interface NodeSpec {
  id: string;
  type: WorkflowNodeType;
  name: string;
  x: number;
  y: number;
  config: Record<string, unknown>;
}

interface EdgeSpec {
  id: string;
  source: string;
  target: string;
  source_handle?: string;
  target_handle?: string;
  order?: number;
  is_default?: boolean;
  condition?: WorkflowDefinition['edges'][number]['condition'];
}

function buildNode(spec: NodeSpec): WorkflowNode {
  return {
    id: spec.id,
    type: spec.type,
    name: spec.name,
    position: { x: spec.x, y: spec.y },
    config: spec.config,
  };
}

function buildEdge(spec: EdgeSpec): WorkflowEdge {
  return {
    id: spec.id,
    source: spec.source,
    target: spec.target,
    source_handle: spec.source_handle,
    target_handle: spec.target_handle,
    order: spec.order ?? 0,
    is_default: spec.is_default ?? false,
    ...(spec.condition ? { condition: spec.condition } : {}),
  };
}

function buildDefinition(nodes: NodeSpec[], edges: EdgeSpec[]): WorkflowDefinition {
  return {
    version: 2,
    schema_version: 2,
    nodes: nodes.map(buildNode),
    edges: edges.map(buildEdge),
    viewport: { ...BASE_VIEWPORT },
  };
}

export const WORKFLOW_TEMPLATES: WorkflowTemplate[] = [
  {
    id: 'kb-qa',
    name: '知识库问答',
    summary: '用户提问后先查知识库，再由模型基于检索结果作答。',
    detail:
      '最常用的入门流程：回答严格来自你选定的知识库，适合产品手册、客服话术、规章制度这类“答案已经在资料里”的场景。',
    steps: ['接收提问', '检索知识库', '生成回答'],
    requirements: [
      { nodeId: 'retrieval-1', label: '选择要检索的知识库' },
    ],
    icon: 'search',
    create: () =>
      buildDefinition(
        [
          { id: 'start', type: 'start', name: '接收提问', x: 40, y: 180, config: {} },
          {
            id: 'retrieval-1',
            type: 'knowledge-retrieval',
            name: '检索知识库',
            x: 300,
            y: 180,
            config: {
              knowledge_base_ids: [],
              query_template: '{{input.query}}',
              top_k: 5,
            },
          },
          {
            id: 'end',
            type: 'end',
            name: '生成回答',
            x: 560,
            y: 180,
            config: { text_template: '{{nodes.retrieval-1.text}}' },
          },
        ],
        [
          { id: 'start-retrieval', source: 'start', target: 'retrieval-1' },
          { id: 'retrieval-end', source: 'retrieval-1', target: 'end' },
        ],
      ),
  },
  {
    id: 'web-augmented-qa',
    name: '联网补充问答',
    summary: '知识库没有明确答案时，再联网搜索一次。',
    detail:
      '在知识库问答基础上多一步联网检索，适合“资料常更新、需要补充最新公开信息”的场景。回答会同时带上知识库引用和网页来源。',
    steps: ['接收提问', '联网搜索', '生成回答'],
    requirements: [
      { nodeId: 'search-1', label: '确认已开启联网搜索能力' },
    ],
    icon: 'internet',
    create: () =>
      buildDefinition(
        [
          { id: 'start', type: 'start', name: '接收提问', x: 40, y: 180, config: {} },
          {
            id: 'search-1',
            type: 'tool',
            name: '联网搜索',
            x: 300,
            y: 180,
            config: {
              kind: 'builtin',
              tool_name: 'web_search',
              arguments: { query: '{{input.query}}' },
            },
          },
          {
            id: 'end',
            type: 'end',
            name: '生成回答',
            x: 560,
            y: 180,
            config: { text_template: '{{nodes.search-1.text}}' },
          },
        ],
        [
          { id: 'start-search', source: 'start', target: 'search-1' },
          { id: 'search-end', source: 'search-1', target: 'end' },
        ],
      ),
  },
  {
    id: 'decision-routing',
    name: '自动判断分流',
    summary: '让模型给出一个结论，并按结论走不同的回复。',
    detail:
      '模型只负责从你给定的候选标签里挑一个，再由不同分支给出对应回复。适合内容审核、工单归类、意图分流这类需要“先判断再处理”的场景。',
    steps: ['接收提问', '模型判断', '按结论回复'],
    requirements: [
      { nodeId: 'decision-1', label: '确认候选标签符合你的业务' },
    ],
    icon: 'control-platform',
    create: () =>
      buildDefinition(
        [
          { id: 'start', type: 'start', name: '接收提问', x: 40, y: 200, config: {} },
          {
            id: 'decision-1',
            type: 'llm-decision',
            name: '模型判断',
            x: 300,
            y: 200,
            config: {
              prompt: '请判断下面的内容属于哪一类，只返回候选标签之一：\n{{input.query}}',
              choices: ['通过', '需要人工复核'],
            },
          },
          {
            id: 'end',
            type: 'end',
            name: '按结论回复',
            x: 560,
            y: 200,
            config: {
              text_template: '判断结果：{{nodes.decision-1.data.choice}}',
            },
          },
        ],
        [
          { id: 'start-decision', source: 'start', target: 'decision-1' },
          { id: 'decision-end', source: 'decision-1', target: 'end' },
        ],
      ),
  },
];

/** 节点类型 → 画布上展示的中文名，供模板预览和节点默认名称复用。 */
export function nodeTypeLabel(type: WorkflowNodeType): string {
  return NODE_TYPE_LABELS[type] || type;
}

/**
 * 判断当前定义是否还是"未动过的空白流程"。
 *
 * 只有空白流程才应该被模板直接覆盖，避免抹掉用户已经画好的内容。
 * 这里按"恰好一个开始 + 一个结束"判断，不依赖节点的具体 ID。
 *
 * @param definition 当前画布定义。
 * @returns 仅由默认开始/结束节点组成时返回 true。
 */
export function isUntouchedDefinition(definition?: WorkflowDefinition | null): boolean {
  const nodes = definition?.nodes || [];
  if (nodes.length !== 2) return false;
  return (
    nodes.filter((node) => node.type === 'start').length === 1 &&
    nodes.filter((node) => node.type === 'end').length === 1
  );
}
