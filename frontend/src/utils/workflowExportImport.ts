import type { WorkflowDefinition, WorkflowNode, WorkflowEdge } from '@/api/agent';

/** 工作流导出包标准化格式规范 */
export interface WorkflowExportPackage {
  $schema?: string;
  version: '1.0';
  type: 'weknora_workflow';
  exported_at: string;
  metadata: {
    name: string;
    description?: string;
    version?: number;
    schema_version: number;
  };
  workflow: WorkflowDefinition;
}

/** 导入解析成功返回的数据结构 */
export interface ParsedWorkflowData {
  name: string;
  description: string;
  workflow: WorkflowDefinition;
}

/** 解析结果响应 */
export type WorkflowParseResult =
  | { success: true; data: ParsedWorkflowData }
  | { success: false; error: string };

/** 文件大小限制：5MB */
const MAX_FILE_SIZE_BYTES = 5 * 1024 * 1024;

/**
 * 清理文件名中不合法的文件系统字符
 */
function sanitizeFileName(name: string): string {
  return (name || '未命名工作流')
    .replace(/[\\/:*?"<>|]/g, '_')
    .replace(/\s+/g, '_')
    .slice(0, 50);
}

/**
 * 格式化当前时间为适合文件名的字符串：YYYYMMDD_HHmm
 */
function formatTimestamp(date: Date = new Date()): string {
  const pad = (num: number) => String(num).padStart(2, '0');
  const year = date.getFullYear();
  const month = pad(date.getMonth() + 1);
  const day = pad(date.getDate());
  const hours = pad(date.getHours());
  const minutes = pad(date.getMinutes());
  return `${year}${month}${day}_${hours}${minutes}`;
}

/**
 * 将工作流数据打包并触发浏览器文件下载
 *
 * @param options.name 工作流名称
 * @param options.description 工作流描述（可选）
 * @param options.workflow 核心节点与边连线定义
 */
export function exportWorkflowPackage(options: {
  name: string;
  description?: string;
  workflow: WorkflowDefinition;
}): void {
  const { name, description = '', workflow } = options;

  const exportPackage: WorkflowExportPackage = {
    $schema: 'https://weknora.tencent.com/schemas/workflow-v1.json',
    version: '1.0',
    type: 'weknora_workflow',
    exported_at: new Date().toISOString(),
    metadata: {
      name: name.trim() || '工作流',
      description: description.trim(),
      version: 2,
      schema_version: 2,
    },
    workflow: {
      version: 2,
      schema_version: 2,
      nodes: workflow.nodes || [],
      edges: workflow.edges || [],
      viewport: workflow.viewport || { x: 0, y: 0, zoom: 1 },
    },
  };

  const jsonString = JSON.stringify(exportPackage, null, 2);
  const blob = new Blob([jsonString], { type: 'application/json;charset=utf-8' });
  const url = URL.createObjectURL(blob);

  const cleanName = sanitizeFileName(name);
  const filename = `工作流_${cleanName}_${formatTimestamp()}.json`;

  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();

  // 延迟清理 DOM 和 Object URL
  setTimeout(() => {
    document.body.removeChild(link);
    URL.revokeObjectURL(url);
  }, 100);
}

/**
 * 读取用户选择的 JSON 文本文件，带体积安全限制
 *
 * @param file 选中的 File 对象
 * @returns 包含文件文本的 Promise
 */
export function readJSONFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!file) {
      reject(new Error('未选择任何文件'));
      return;
    }

    if (!file.name.toLowerCase().endsWith('.json')) {
      reject(new Error('仅支持导入 .json 格式的工作流配置文件'));
      return;
    }

    if (file.size > MAX_FILE_SIZE_BYTES) {
      reject(new Error('文件体积过大，请导入 5MB 以内的 JSON 文件'));
      return;
    }

    const reader = new FileReader();
    reader.onload = (e) => {
      const content = e.target?.result;
      if (typeof content === 'string') {
        resolve(content);
      } else {
        reject(new Error('文件内容读取失败'));
      }
    };
    reader.onerror = () => {
      reject(new Error('读取文件过程中发生错误'));
    };
    reader.readAsText(file, 'utf-8');
  });
}

/**
 * 校验并解析工作流 JSON 字符串
 *
 * 容错支持：
 * 1. 标准导出的 WorkflowExportPackage（含 metadata 和 workflow）
 * 2. 包含 config.workflow 的 Agent 导出格式
 * 3. 纯 WorkflowDefinition（直接包含 nodes 和 edges 数组）
 *
 * @param rawText JSON 文本
 * @returns 解析结果，若成功返回归一化数据，若失败返回详细错误信息
 */
export function parseWorkflowJSON(rawText: string): WorkflowParseResult {
  if (!rawText || !rawText.trim()) {
    return { success: false, error: '导入的文件内容为空' };
  }

  let parsed: any;
  try {
    parsed = JSON.parse(rawText);
  } catch (err: any) {
    return { success: false, error: `文件内容不是合法的 JSON 格式：${err?.message || '解析错误'}` };
  }

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return { success: false, error: '工作流文件格式错误，顶层应为 JSON 对象' };
  }

  let workflowCandidate: any = null;
  let candidateName = '';
  let candidateDesc = '';

  // 1. 标准包格式
  if (parsed.workflow && typeof parsed.workflow === 'object' && !Array.isArray(parsed.workflow)) {
    workflowCandidate = parsed.workflow;
    candidateName = parsed.metadata?.name || parsed.name || '';
    candidateDesc = parsed.metadata?.description || parsed.description || '';
  }
  // 2. 完整 Agent 格式 (含有 config.workflow)
  else if (parsed.config?.workflow && typeof parsed.config.workflow === 'object') {
    workflowCandidate = parsed.config.workflow;
    candidateName = parsed.name || '';
    candidateDesc = parsed.description || '';
  }
  // 3. 纯 WorkflowDefinition 格式 (直接是 { nodes, edges })
  else if (Array.isArray(parsed.nodes) && Array.isArray(parsed.edges)) {
    workflowCandidate = parsed;
    candidateName = parsed.name || '';
    candidateDesc = parsed.description || '';
  } else {
    return {
      success: false,
      error: '无法识别工作流配置：未找到有效的节点（nodes）或连线（edges）数据',
    };
  }

  // 深度结构校验
  if (!Array.isArray(workflowCandidate.nodes) || workflowCandidate.nodes.length === 0) {
    return { success: false, error: '工作流节点列表（nodes）不能为空' };
  }

  if (!Array.isArray(workflowCandidate.edges)) {
    return { success: false, error: '工作流连线列表（edges）格式错误，必须为数组' };
  }

  const nodeMap = new Map<string, WorkflowNode>();
  let startCount = 0;
  let endCount = 0;
  const schemaVersion = Number(workflowCandidate.schema_version || workflowCandidate.version || 1);
  const isLegacySchema = !Number.isFinite(schemaVersion) || schemaVersion < 2;
  const outgoingCount = new Map<string, number>();
  for (const edge of workflowCandidate.edges) {
    if (edge && typeof edge.source === 'string') {
      outgoingCount.set(edge.source, (outgoingCount.get(edge.source) || 0) + 1);
    }
  }

  for (let i = 0; i < workflowCandidate.nodes.length; i++) {
    const node = workflowCandidate.nodes[i];
    if (!node || typeof node !== 'object') {
      return { success: false, error: `第 ${i + 1} 个节点数据无效` };
    }

    if (!node.id || typeof node.id !== 'string') {
      return { success: false, error: `第 ${i + 1} 个节点缺少合法的 ID` };
    }

    if (nodeMap.has(node.id)) {
      return { success: false, error: `存在重复的节点 ID：“${node.id}”` };
    }

    if (!node.type || typeof node.type !== 'string') {
      return { success: false, error: `节点“${node.id}”缺少类型属性` };
    }

    if (node.type === 'start') {
      startCount++;
    } else if (node.type === 'end') {
      endCount++;
    }

    const normalizedPosition = {
      x: Number(node.position?.x) || 100 + i * 180,
      y: Number(node.position?.y) || 200,
    };
    const branchMode = node.branch_mode === 'all_match' || node.branch_mode === 'first_match'
      ? node.branch_mode
      : isLegacySchema && (outgoingCount.get(String(node.id)) || 0) > 1
        ? 'all_match'
        : 'first_match';

    const normalizedNode: WorkflowNode = {
      id: String(node.id),
      type: node.type,
      name: node.name || (node.type === 'start' ? '开始' : node.type === 'end' ? '结束' : '未命名节点'),
      branch_mode: branchMode,
      position: normalizedPosition,
      config: typeof node.config === 'object' && node.config !== null ? node.config : {},
    };

    nodeMap.set(normalizedNode.id, normalizedNode);
  }

  if (startCount !== 1) {
    return { success: false, error: `工作流必须包含且仅能包含 1 个开始（start）节点，当前包含 ${startCount} 个` };
  }

  if (endCount < 1) {
    return { success: false, error: '工作流至少需要包含 1 个结束（end）节点' };
  }

  const normalizedEdges: WorkflowEdge[] = [];
  const edgeIdSet = new Set<string>();
  const orderBySource = new Map<string, number>();

  for (let j = 0; j < workflowCandidate.edges.length; j++) {
    const edge = workflowCandidate.edges[j];
    if (!edge || typeof edge !== 'object') {
      return { success: false, error: `第 ${j + 1} 条连线数据无效` };
    }

    const edgeId = edge.id || `edge_${edge.source || j}_${edge.target || j}_${j}`;
    if (edgeIdSet.has(edgeId)) {
      return { success: false, error: `存在重复的连线 ID：“${edgeId}”` };
    }
    edgeIdSet.add(edgeId);

    if (!edge.source || !edge.target) {
      return { success: false, error: `连线“${edgeId}”缺少起点（source）或终点（target）` };
    }

    if (!nodeMap.has(edge.source)) {
      return { success: false, error: `连线“${edgeId}”引用的起点节点“${edge.source}”不存在` };
    }

    if (!nodeMap.has(edge.target)) {
      return { success: false, error: `连线“${edgeId}”引用的终点节点“${edge.target}”不存在` };
    }

    if (edge.source === edge.target) {
      return { success: false, error: `连线“${edgeId}”指向自身，不允许自环连线` };
    }

    const sourceOrder = orderBySource.get(edge.source) || 0;
    const hasExplicitOrder = edge.order !== undefined && edge.order !== null && edge.order !== '' && Number.isFinite(Number(edge.order));
    const order = isLegacySchema || !hasExplicitOrder ? sourceOrder : Number(edge.order);
    orderBySource.set(edge.source, sourceOrder + 1);

    normalizedEdges.push({
      id: edgeId,
      source: edge.source,
      target: edge.target,
      source_handle: edge.source_handle || edge.sourceHandle || undefined,
      target_handle: edge.target_handle || edge.targetHandle || undefined,
      order,
      is_default: Boolean(edge.is_default ?? edge.data?.is_default ?? false),
      condition: edge.condition || edge.data?.condition || undefined,
    });
  }

  const normalizedWorkflow: WorkflowDefinition = {
    version: 2,
    schema_version: 2,
    nodes: Array.from(nodeMap.values()),
    edges: normalizedEdges,
    viewport: {
      x: Number(workflowCandidate.viewport?.x) || 0,
      y: Number(workflowCandidate.viewport?.y) || 0,
      zoom: Number(workflowCandidate.viewport?.zoom) || 1,
    },
  };

  return {
    success: true,
    data: {
      name: candidateName,
      description: candidateDesc,
      workflow: normalizedWorkflow,
    },
  };
}
