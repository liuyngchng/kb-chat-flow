import type { Node, Edge } from '@xyflow/react';
import type { AppNodeData, DesignDoc, DesignDocNode, DesignDocEdge } from './types';
import { load as yamlLoad } from 'js-yaml';

/**
 * 将 React Flow 的 nodes + edges 导出为自描述 DesignDoc 格式。
 * 此格式面向 AI 和人类阅读，不依赖任何后端类型。
 */
export function exportToDesignDoc(
  name: string,
  description: string,
  purpose: string,
  tags: string[],
  nodes: Node[],
  edges: Edge[],
): DesignDoc {
  const docNodes: DesignDocNode[] = [];
  const docEdges: DesignDocEdge[] = [];

  // 1. 转换节点
  for (const node of nodes) {
    const data = node.data as unknown as AppNodeData;
    const base = {
      id: node.id,
      type: data.nodeType,
      label: data.label || '',
      purpose: (data as any).purpose || '',
    };

    switch (data.nodeType) {
      case 'agent':
        docNodes.push({
          ...base,
          agentName: data.agentName || '',
          inputTemplate: data.inputTemplate || '',
          outputVar: data.outputVar || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'tool':
        docNodes.push({
          ...base,
          toolName: data.toolName || '',
          toolParams: data.toolParams || '',
          outputVar: data.outputVar || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'variable':
        docNodes.push({
          ...base,
          varName: data.varName || '',
          varDesc: data.varDesc || '',
        });
        break;
      case 'branch':
        docNodes.push({
          ...base,
          branchInputVar: data.inputVar || '',
        });
        break;
      case 'note':
        docNodes.push({
          ...base,
          content: data.content || '',
          color: data.color || 'yellow',
        });
        break;
      case 'start':
        docNodes.push({ ...base });
        break;
      // ---- Dify 节点 ----
      case 'llm':
        docNodes.push({
          ...base,
          modelName: data.modelName || '',
          modelProvider: data.modelProvider || '',
          completionParams: data.completionParams || '',
          systemPrompt: data.systemPrompt || '',
          userPrompt: data.userPrompt || '',
          contextVar: data.contextVar || '',
          memoryWindow: data.memoryWindow || 0,
          outputVar: data.outputVar || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'code':
        docNodes.push({
          ...base,
          code: data.code || '',
          inputVars: data.inputVars || '',
          outputs: data.outputs || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'answer':
        docNodes.push({
          ...base,
          answerText: data.answerText || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'knowledge-retrieval':
        docNodes.push({
          ...base,
          datasetIds: data.datasetIds || '',
          queryVar: data.queryVar || '',
          retrievalMode: data.retrievalMode || 'multiple',
          topK: data.topK || 3,
          rerankingModel: data.rerankingModel || '',
          rerankingEnable: data.rerankingEnable || false,
          scoreThreshold: data.scoreThreshold || '',
          outputVar: data.outputVar || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'question-classifier':
        docNodes.push({
          ...base,
          classes: data.classes || '',
          instructions: data.instructions || '',
          queryVar: data.queryVar || '',
          modelName: data.modelName || '',
          modelProvider: data.modelProvider || '',
          memoryEnabled: data.memoryEnabled || false,
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'assigner':
        docNodes.push({
          ...base,
          items: data.items || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
      case 'if-else':
        docNodes.push({
          ...base,
          cases: data.cases || '',
          parallelGroup: data.parallelGroup || '',
        });
        break;
    }
  }

  // 2. 转换边 — condition 来自用户编辑的边 label
  for (const edge of edges) {
    docEdges.push({
      from: edge.source,
      to: edge.target,
      condition: typeof edge.label === 'string' && edge.label ? edge.label : undefined,
      sourceHandle: (edge as any).sourceHandle || undefined,
    });
  }

  // 3. 计算顶层摘要
  const summaryInputs = getSummaryInputs(docNodes, docEdges);
  const summaryOutputs = getSummaryOutputs(docNodes, docEdges);

  return {
    _schema: 'workflow-design-doc/v2',
    name: name || '未命名工作流',
    description: description || '',
    purpose: purpose || '',
    metadata: {
      createdAt: new Date().toISOString(),
      tags: tags || [],
    },
    _summary: {
      inputs: summaryInputs,
      outputs: summaryOutputs,
    },
    nodes: docNodes,
    edges: docEdges,
  };
}

/**
 * 从 DesignDoc 格式还原为 React Flow nodes + edges
 */
export function importFromDesignDoc(
  doc: DesignDoc,
): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  for (const dn of doc.nodes) {
    const pos = { x: 50, y: 100 + nodes.length * 80 };
    let data: Record<string, unknown>;

    switch (dn.type) {
      case 'start':
        data = { nodeType: 'start', label: dn.label || '开始', purpose: dn.purpose || '' };
        break;
      case 'agent':
        data = {
          nodeType: 'agent', label: dn.label || 'AI Agent', purpose: dn.purpose || '',
          agentName: dn.agentName || '', inputTemplate: dn.inputTemplate || '',
          outputVar: dn.outputVar || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'tool':
        data = {
          nodeType: 'tool', label: dn.label || '工具', purpose: dn.purpose || '',
          toolName: dn.toolName || '', toolParams: dn.toolParams || '',
          outputVar: dn.outputVar || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'variable':
        data = {
          nodeType: 'variable', label: dn.label || '变量', purpose: dn.purpose || '',
          varName: dn.varName || '', varDesc: dn.varDesc || '',
        };
        break;
      case 'branch':
        data = {
          nodeType: 'branch', label: dn.label || '条件分支', purpose: dn.purpose || '',
          inputVar: dn.branchInputVar || '',
        };
        break;
      case 'note':
        data = {
          nodeType: 'note', label: dn.label || '便签', purpose: dn.purpose || '',
          content: dn.content || '', color: dn.color || 'yellow',
        };
        break;
      // ---- Dify 节点 ----
      case 'llm':
        data = {
          nodeType: 'llm', label: dn.label || 'LLM', purpose: dn.purpose || '',
          modelName: dn.modelName || '', modelProvider: dn.modelProvider || '',
          completionParams: dn.completionParams || '',
          systemPrompt: dn.systemPrompt || '', userPrompt: dn.userPrompt || '',
          contextVar: dn.contextVar || '', memoryWindow: dn.memoryWindow || 0,
          outputVar: dn.outputVar || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'code':
        data = {
          nodeType: 'code', label: dn.label || '代码', purpose: dn.purpose || '',
          code: dn.code || '', inputVars: dn.inputVars || '',
          outputs: dn.outputs || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'answer':
        data = {
          nodeType: 'answer', label: dn.label || '回复', purpose: dn.purpose || '',
          answerText: dn.answerText || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'knowledge-retrieval':
        data = {
          nodeType: 'knowledge-retrieval', label: dn.label || '知识检索', purpose: dn.purpose || '',
          datasetIds: dn.datasetIds || '', queryVar: dn.queryVar || '',
          retrievalMode: dn.retrievalMode || 'multiple', topK: dn.topK || 3,
          rerankingModel: dn.rerankingModel || '', rerankingEnable: dn.rerankingEnable || false,
          scoreThreshold: dn.scoreThreshold || '',
          outputVar: dn.outputVar || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'question-classifier':
        data = {
          nodeType: 'question-classifier', label: dn.label || '问题分类器', purpose: dn.purpose || '',
          classes: dn.classes || '', instructions: dn.instructions || '',
          queryVar: dn.queryVar || '', modelName: dn.modelName || '',
          modelProvider: dn.modelProvider || '', memoryEnabled: dn.memoryEnabled || false,
          parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'assigner':
        data = {
          nodeType: 'assigner', label: dn.label || '变量赋值', purpose: dn.purpose || '',
          items: dn.items || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      case 'if-else':
        data = {
          nodeType: 'if-else', label: dn.label || '条件分支', purpose: dn.purpose || '',
          cases: dn.cases || '', parallelGroup: dn.parallelGroup || '',
        };
        break;
      default:
        data = { nodeType: 'start', label: '未识别', purpose: '' };
    }

    nodes.push({
      id: dn.id,
      type: dn.type,
      position: { x: pos.x, y: pos.y },
      data,
    });
  }

  for (const de of doc.edges) {
    edges.push({
      id: `e_${de.from}_${de.to}`,
      source: de.from,
      target: de.to,
      label: de.condition || de.label || '',
    } as Edge);
  }

  return { nodes, edges };
}

// ============================================================
// Dify DSL YAML 导入
// ============================================================

/** 安全将 Dify 的选择器（可能是数组或字符串）转为点分隔字符串 */
function selectorToStr(v: unknown): string {
  if (Array.isArray(v)) return v.join('.');
  if (typeof v === 'string') return v;
  return '';
}

/**
 * 解析 Dify 导出的 YAML/DSL 文件，返回 React Flow 节点和边
 */
export function importFromDifyYaml(yamlText: string): {
  nodes: Node[];
  edges: Edge[];
  workflowName: string;
  workflowDesc: string;
  workflowPurpose: string;
  workflowTags: string[];
} {
  const doc = yamlLoad(yamlText) as any;

  const app = doc.app || {};
  const workflow = doc.workflow || {};
  const graph = workflow.graph || {};
  const rawNodes: any[] = graph.nodes || [];
  const rawEdges: any[] = graph.edges || [];

  const workflowName = app.name || '';
  const workflowDesc = app.description || '';
  const workflowPurpose = '';
  const workflowTags: string[] = [];

  const nodes: Node[] = [];
  const edges: Edge[] = [];

  // 1. 转换节点
  for (const rn of rawNodes) {
    const data = rn.data || {};
    const nodeType = data.type as string;
    const id = rn.id as string;
    const title = data.title || '';
    const desc = data.desc || '';

    // Dify 坐标
    const pos = rn.position || { x: 0, y: 0 };

    let nodeData: Record<string, unknown>;

    switch (nodeType) {
      case 'start':
        nodeData = {
          nodeType: 'start',
          label: title || '开始',
          purpose: desc || '',
        };
        break;

      case 'llm': {
        const model = data.model || {};
        const promptTemplates: any[] = data.prompt_template || [];
        const systemPrompt = promptTemplates.find((p: any) => p.role === 'system')?.text || '';
        const userPrompt = promptTemplates.find((p: any) => p.role === 'user')?.text || '';
        const contextVar = selectorToStr(data.context?.variable_selector);
        const memoryWindow = data.memory?.window?.size || 0;

        nodeData = {
          nodeType: 'llm',
          label: title || 'LLM',
          purpose: desc || '',
          modelName: model.name || '',
          modelProvider: model.provider || '',
          completionParams: JSON.stringify(model.completion_params || {}, null, 2),
          systemPrompt,
          userPrompt,
          contextVar,
          memoryWindow,
          outputVar: '',
          parallelGroup: '',
        };
        break;
      }

      case 'code': {
        const codeText = data.code || '';
        const variables: any[] = data.variables || [];
        const outputDefs = data.outputs || {};

        nodeData = {
          nodeType: 'code',
          label: title || '代码',
          purpose: desc || '',
          code: codeText,
          inputVars: JSON.stringify(variables.map((v: any) => ({
            variable: v.variable,
            valueSelector: selectorToStr(v.value_selector),
          })), null, 2),
          outputs: JSON.stringify(outputDefs, null, 2),
          parallelGroup: '',
        };
        break;
      }

      case 'answer': {
        nodeData = {
          nodeType: 'answer',
          label: title || '回复',
          purpose: desc || '',
          answerText: data.answer || '',
          parallelGroup: '',
        };
        break;
      }

      case 'knowledge-retrieval': {
        const datasetIds = (data.dataset_ids || []).join(', ');
        const queryVar = selectorToStr(data.query_variable_selector);
        const retrievalMode = data.retrieval_mode || 'multiple';
        const topK = data.multiple_retrieval_config?.top_k || 3;
        const rerankingModel = data.multiple_retrieval_config?.reranking_model?.model || '';
        const rerankingEnable = data.multiple_retrieval_config?.reranking_enable || false;
        const scoreThreshold = data.multiple_retrieval_config?.score_threshold;

        nodeData = {
          nodeType: 'knowledge-retrieval',
          label: title || '知识检索',
          purpose: desc || '',
          datasetIds,
          queryVar,
          retrievalMode,
          topK,
          rerankingModel,
          rerankingEnable,
          scoreThreshold: scoreThreshold != null ? String(scoreThreshold) : '',
          outputVar: '',
          parallelGroup: '',
        };
        break;
      }

      case 'question-classifier': {
        const classes = (data.classes || []).map((c: any) => ({
          id: c.id,
          name: c.name,
        }));

        nodeData = {
          nodeType: 'question-classifier',
          label: title || '问题分类器',
          purpose: desc || '',
          classes: JSON.stringify(classes, null, 2),
          instructions: data.instructions || '',
          queryVar: selectorToStr(data.query_variable_selector) || 'sys.query',
          modelName: data.model?.name || '',
          modelProvider: data.model?.provider || '',
          memoryEnabled: data.memory?.enabled || false,
          parallelGroup: '',
        };
        break;
      }

      case 'assigner': {
        const items = (data.items || []).map((item: any) => ({
          inputType: item.input_type,
          operation: item.operation,
          value: selectorToStr(item.value),
          variableSelector: selectorToStr(item.variable_selector),
        }));

        nodeData = {
          nodeType: 'assigner',
          label: title || '变量赋值',
          purpose: desc || '',
          items: JSON.stringify(items, null, 2),
          parallelGroup: '',
        };
        break;
      }

      case 'if-else': {
        const cases = (data.cases || []).map((c: any) => ({
          caseId: c.case_id,
          conditions: (c.conditions || []).map((cond: any) => ({
            comparisonOperator: cond.comparison_operator,
            value: cond.value,
            varType: cond.varType,
            variableSelector: selectorToStr(cond.variable_selector),
          })),
          logicalOperator: c.logical_operator || 'or',
        }));

        nodeData = {
          nodeType: 'if-else',
          label: title || '条件分支',
          purpose: desc || '',
          cases: JSON.stringify(cases, null, 2),
          parallelGroup: '',
        };
        break;
      }

      default: {
        // 未知类型，跳过
        continue;
      }
    }

    nodes.push({
      id,
      type: nodeType,
      position: { x: pos.x, y: pos.y },
      data: nodeData,
    });
  }

  // 2. 转换边
  // 注意：Dify 的 sourceHandle（如 "true"/"false"/"price"）与我们的节点 handle ID 不匹配，
  // 因此不设置 sourceHandle，只保留 label 用于显示分支条件
  for (const re of rawEdges) {
    edges.push({
      id: re.id || `e_${re.source}_${re.target}`,
      source: re.source,
      target: re.target,
      label: re.sourceHandle || undefined,
    } as Edge);
  }

  return { nodes, edges, workflowName, workflowDesc, workflowPurpose, workflowTags };
}

/**
 * 判断文件内容是否为 Dify DSL YAML 格式
 */
export function isDifyYaml(text: string): boolean {
  try {
    const doc = yamlLoad(text) as any;
    return !!(doc && doc.kind === 'app' && doc.workflow?.graph);
  } catch {
    return false;
  }
}

/**
 * 导出为 Markdown / Mermaid 流程图
 */
export function exportToMarkdown(doc: DesignDoc): string {
  const lines: string[] = [];

  lines.push(`# 工作流设计文档: ${doc.name}`);
  lines.push('');
  lines.push(`> ${doc.description}`);
  lines.push('');
  if (doc.purpose) {
    lines.push(`**设计意图:** ${doc.purpose}`);
    lines.push('');
  }
  if (doc.metadata.tags.length > 0) {
    lines.push(`**标签:** ${doc.metadata.tags.join(', ')}`);
    lines.push('');
  }
  if (doc._summary) {
    if (doc._summary.inputs.length > 0) {
      lines.push(`**输入:** ${doc._summary.inputs.join(', ')}`);
    }
    if (doc._summary.outputs.length > 0) {
      lines.push(`**输出:** ${doc._summary.outputs.join(', ')}`);
    }
    lines.push('');
  }

  // Mermaid 流程图
  lines.push('```mermaid');
  lines.push('flowchart TD');
  for (const node of doc.nodes) {
    if (node.type === 'note') continue; // 便签不画入流程图
    const safeLabel = node.label.replace(/[^a-zA-Z一-鿿0-9_\- ]/g, '');
    const icon = nodeTypeIcon(node.type);
    lines.push(`  ${node.id}["${icon} ${safeLabel}"]`);
  }
  for (const edge of doc.edges) {
    const cond = edge.condition || '';
    if (cond) {
      lines.push(`  ${edge.from} -->|${cond}| ${edge.to}`);
    } else {
      lines.push(`  ${edge.from} --> ${edge.to}`);
    }
  }
  lines.push('```');
  lines.push('');

  // 节点详情
  lines.push('## 节点列表');
  lines.push('');
  for (const node of doc.nodes) {
    lines.push(`### ${node.label} (\`${node.id}\`)`);
    lines.push('');
    lines.push(`- **类型:** ${node.type}`);
    if (node.purpose) lines.push(`- **用途:** ${node.purpose}`);
    if (node.outputVar) lines.push(`- **输出变量:** \`{{${node.outputVar}}}\``);
    if (node.branchInputVar) lines.push(`- **分支依据:** \`{{${node.branchInputVar}}}\``);
    if (node.parallelGroup) lines.push(`- **并行组:** ${node.parallelGroup}`);
    if (node.agentName) lines.push(`- **Agent:** ${node.agentName}`);
    if (node.toolName) lines.push(`- **工具:** ${node.toolName}`);
    if (node.inputTemplate) lines.push(`- **输入模板:** \`${node.inputTemplate}\``);
    if (node.toolParams) lines.push(`- **参数:** \`${node.toolParams}\``);
    if (node.varName) lines.push(`- **变量:** \`{{${node.varName}}}\``);
    if (node.content) lines.push(`- **内容:** ${node.content}`);
    // Dify 节点详情
    if (node.modelName) lines.push(`- **模型:** ${node.modelName}`);
    if (node.systemPrompt) lines.push(`- **System Prompt:** \`${node.systemPrompt.slice(0, 200)}...\``);
    if (node.code) lines.push(`- **代码:** \`\`\`python\n${node.code.slice(0, 500)}\n\`\`\``);
    if (node.answerText) lines.push(`- **回复:** ${node.answerText.slice(0, 200)}`);
    if (node.datasetIds) lines.push(`- **知识库:** ${node.datasetIds}`);
    if (node.classes) lines.push(`- **分类:** ${node.classes}`);
    if (node.instructions) lines.push(`- **指令:** ${node.instructions.slice(0, 300)}`);
    if (node.items) lines.push(`- **赋值项:** ${node.items}`);
    if (node.cases) lines.push(`- **条件:** ${node.cases}`);
    lines.push('');
  }

  return lines.join('\n');
}

/**
 * 导出为 AI Prompt 文本 — 一段连贯的自然语言描述，直接喂给 AI
 */
export function exportToAIPrompt(doc: DesignDoc): string {
  const lines: string[] = [];

  lines.push(`# 工作流设计说明`);
  lines.push('');
  lines.push(`名称: ${doc.name}`);
  if (doc.description) lines.push(`描述: ${doc.description}`);
  if (doc.purpose) lines.push(`设计意图: ${doc.purpose}`);
  lines.push('');

  // 顶层 I/O 摘要
  const inputs = collectInputNames(doc);
  const outputs = collectOutputNames(doc);
  if (inputs.length > 0) lines.push(`工作流入参: ${inputs.join(', ')}`);
  if (outputs.length > 0) lines.push(`工作流出参: ${outputs.join(', ')}`);
  lines.push('');

  // 流程描述
  lines.push('## 流程结构');
  lines.push('');

  const startNode = doc.nodes.find((n) => n.type === 'start');
  const startLabel = startNode?.label || '开始';
  lines.push(`流程从「${startLabel}」进入，节点按顺序/分支流转：`);
  lines.push('');

  // 用边的顺序来描述流程，真实序号 + 缩进区分层级
  let currentId = startNode?.id;
  if (!currentId && doc.nodes.length > 0) currentId = doc.nodes[0].id;

  const visited = new Set<string>();
  let counter = 0;

  function describeNode(nodeId: string, indent: number) {
    if (visited.has(nodeId)) return;
    visited.add(nodeId);

    const node = doc.nodes.find((n) => n.id === nodeId);
    if (!node || node.type === 'note' || node.type === 'start') return;

    const prefix = '  '.repeat(indent);
    counter++;
    lines.push(`${prefix}${counter}. **${node.label}** (${typeLabel(node.type)})`);
    if (node.purpose) lines.push(`${prefix}    - 用途: ${node.purpose}`);
    if (node.agentName) lines.push(`${prefix}    - Agent: ${node.agentName}`);
    if (node.toolName) lines.push(`${prefix}    - 工具: ${node.toolName}`);
    if (node.modelName) lines.push(`${prefix}    - 模型: ${node.modelName}`);
    if (node.inputTemplate) lines.push(`${prefix}    - 输入模板: ${node.inputTemplate}`);
    if (node.toolParams) lines.push(`${prefix}    - 参数模板: ${node.toolParams}`);
    if (node.outputVar) lines.push(`${prefix}    - 输出变量: \`{{${node.outputVar}}}\``);
    if (node.branchInputVar) lines.push(`${prefix}    - 分支依据: \`{{${node.branchInputVar}}}\``);
    if (node.parallelGroup) lines.push(`${prefix}    - 并行组: ${node.parallelGroup}`);
    if (node.code) lines.push(`${prefix}    - 代码: (Python 代码块)`);
    if (node.answerText) lines.push(`${prefix}    - 回复: ${node.answerText.slice(0, 150)}`);
    if (node.datasetIds) lines.push(`${prefix}    - 知识库: ${node.datasetIds}`);
    if (node.classes) {
      try {
        const cls = JSON.parse(node.classes);
        lines.push(`${prefix}    - 分类: ${cls.map((c: any) => c.name).join(', ')}`);
      } catch {
        lines.push(`${prefix}    - 分类: ${node.classes}`);
      }
    }
    if (node.cases) lines.push(`${prefix}    - 条件: (见 JSON)`);
    if (node.items) lines.push(`${prefix}    - 赋值: (见 JSON)`);

    if (node.type === 'branch' || node.type === 'if-else' || node.type === 'question-classifier') {
      const outEdges = doc.edges.filter((e) => e.from === nodeId);
      if (outEdges.length > 0) {
        lines.push(`${prefix}    - 分支规则:`);
        for (const edge of outEdges) {
          const target = doc.nodes.find((n) => n.id === edge.to);
          const cond = edge.condition || edge.sourceHandle || 'default';
          lines.push(`${prefix}      · 当 \`${cond}\` → 流向「${target?.label || edge.to}」`);
        }
        // 每个分支的下游独立描述（下一层级缩进）
        for (const edge of outEdges) {
          describeNode(edge.to, indent + 1);
        }
      }
      return;
    }

    // 非分支节点：继续描述下游（同层级）
    const outEdges = doc.edges.filter((e) => e.from === nodeId);
    for (const edge of outEdges) {
      describeNode(edge.to, indent);
    }
  }

  // 从 start 节点的下游开始
  const startEdges = doc.edges.filter((e) => e.from === currentId);
  for (const edge of startEdges) {
    describeNode(edge.to, 0);
  }

  // 万一有遗漏的孤立节点
  for (const node of doc.nodes) {
    if (!visited.has(node.id) && node.type !== 'start' && node.type !== 'note') {
      describeNode(node.id, 0);
    }
  }

  lines.push('');
  lines.push('## 实现要求');
  lines.push('');
  lines.push('请根据以上设计，生成完整的代码实现。要求：');
  lines.push('');
  lines.push('1. 每个节点的 `purpose` 字段说明了设计意图，请据此实现对应逻辑');
  lines.push('2. 分支节点根据 `condition` 做条件判断，`default` 为兜底分支');
  lines.push('3. 节点通过 `{{变量名}}` 引用上游输出，变量名需保持一致');
  lines.push('4. 便签节点仅用于说明，不参与流程逻辑');
  lines.push('5. 输出语言和框架由你自行决定，保持代码可读性');

  return lines.join('\n');
}

/**
 * 下载文件
 */
export function downloadJSON(data: unknown, filename: string) {
  const json = JSON.stringify(data, null, 2);
  const blob = new Blob([json], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename.endsWith('.json') ? filename : `${filename}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

export function downloadText(text: string, filename: string) {
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

// ============================================================
// 内部辅助
// ============================================================

function getSummaryInputs(nodes: DesignDocNode[], edges: DesignDocEdge[]): string[] {
  // 没有上游边的节点的输入模板中的变量 = 工作流入参
  const referencedIds = new Set(edges.map((e) => e.to));
  const results = new Set<string>();
  const pattern = /\{\{(\w+(?:\.\w+)*)\}\}/g;

  for (const node of nodes) {
    if (!referencedIds.has(node.id)) continue; // 非头节点
    const tmpl = node.inputTemplate || node.toolParams || '';
    let match: RegExpExecArray | null;
    while ((match = pattern.exec(tmpl)) !== null) {
      results.add(match[1]);
    }
  }
  // 排除内部定义的 outputVar
  for (const node of nodes) {
    if (node.outputVar) results.delete(node.outputVar);
  }
  return Array.from(results);
}

function getSummaryOutputs(nodes: DesignDocNode[], edges: DesignDocEdge[]): string[] {
  // 没有下游边的节点的 outputVar = 工作流出参
  const referencedIds = new Set(edges.map((e) => e.from));
  const results: string[] = [];
  for (const node of nodes) {
    if (referencedIds.has(node.id)) continue; // 有下游的不是最终节点
    if (node.outputVar) results.push(node.outputVar);
  }
  return results;
}

function collectInputNames(doc: DesignDoc): string[] {
  return doc._summary?.inputs || [];
}

function collectOutputNames(doc: DesignDoc): string[] {
  return doc._summary?.outputs || [];
}

function typeLabel(type: string): string {
  switch (type) {
    case 'agent': return 'AI 处理';
    case 'tool': return '工具调用';
    case 'branch': return '条件分支';
    case 'variable': return '变量声明';
    case 'llm': return 'LLM 调用';
    case 'code': return '代码执行';
    case 'answer': return '回复输出';
    case 'knowledge-retrieval': return '知识检索';
    case 'question-classifier': return '问题分类';
    case 'assigner': return '变量赋值';
    case 'if-else': return '条件分支';
    default: return type;
  }
}

export function nodeTypeIcon(type: string): string {
  switch (type) {
    case 'start': return '▶';
    case 'agent': return '🤖';
    case 'tool': return '🔧';
    case 'variable': return '📦';
    case 'branch': return '🔀';
    case 'note': return '📝';
    case 'llm': return '🧠';
    case 'code': return '🐍';
    case 'answer': return '💬';
    case 'knowledge-retrieval': return '📚';
    case 'question-classifier': return '🏷️';
    case 'assigner': return '📋';
    case 'if-else': return '🔀';
    default: return '●';
  }
}