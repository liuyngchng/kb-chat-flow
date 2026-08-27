// ============================================================
// 工作流校验工具
// 确保导出 JSON 的变量名唯一性 + 模板引用一致性
// ============================================================

import type { Node, Edge } from '@xyflow/react';
import { type AppNodeData } from './types';

/** 校验问题 */
export interface ValidationIssue {
  type: 'error' | 'warning' | 'info';
  message: string;
  nodeId: string;
  field?: string;
}

/** 模板变量解析结果 */
export interface TemplateVarInfo {
  name: string;
  defined: boolean;
  definedBy: string | null; // 节点 ID
  definedByLabel: string | null; // 节点名称
}

// ============================================================
// 1. 检测重复的 outputVar
// ============================================================

export function findDuplicateOutputVars(
  nodes: Node[],
): Map<string, string[]> {
  const varMap = new Map<string, string[]>();

  for (const node of nodes) {
    const data = node.data as unknown as AppNodeData;
    let outputVar = '';

    switch (data.nodeType) {
      case 'agent':
      case 'tool':
      case 'llm':
      case 'knowledge-retrieval':
        outputVar = (data as any).outputVar || '';
        break;
    }

    if (outputVar) {
      const existing = varMap.get(outputVar) || [];
      existing.push(node.id);
      varMap.set(outputVar, existing);
    }
  }

  // 只保留有冲突的
  const result = new Map<string, string[]>();
  for (const [name, ids] of varMap) {
    if (ids.length > 1) {
      result.set(name, ids);
    }
  }
  return result;
}

// ============================================================
// 2. 获取某个节点的拓扑上游变量列表
// ============================================================

export function getUpstreamVars(
  nodeId: string,
  nodes: Node[],
  edges: Edge[],
): { name: string; nodeId: string; label: string }[] {
  // 1. 建立邻接表（反向 — 从目标找源）
  const reverseAdj = new Map<string, string[]>();
  for (const edge of edges) {
    const targets = reverseAdj.get(edge.target) || [];
    targets.push(edge.source);
    reverseAdj.set(edge.target, targets);
  }

  // 2. BFS 从当前节点向上游找所有可达节点
  const visited = new Set<string>();
  const queue = [nodeId];
  const upstream: string[] = [];

  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const src of reverseAdj.get(current) || []) {
      if (!visited.has(src)) {
        visited.add(src);
        upstream.push(src);
        queue.push(src);
      }
    }
  }

  // 3. 收集这些节点的 outputVar
  const result: { name: string; nodeId: string; label: string }[] = [];
  for (const id of upstream) {
    const node = nodes.find((n) => n.id === id);
    if (!node) continue;
    const data = node.data as unknown as AppNodeData;
    let outputVar = '';
    switch (data.nodeType) {
      case 'agent':
      case 'tool':
      case 'llm':
      case 'knowledge-retrieval':
        outputVar = (data as any).outputVar || '';
        break;
    }
    if (outputVar) {
      result.push({ name: outputVar, nodeId: id, label: data.label || id });
    }
  }

  return result;
}

// ============================================================
// 3. 解析模板中的变量引用，标记是否已定义
// ============================================================

const varPattern = /\{\{(\w+(?:\.\w+)*)\}\}/g;

export function resolveTemplateVars(
  template: string,
  nodeId: string,
  nodes: Node[],
  edges: Edge[],
): TemplateVarInfo[] {
  const vars: TemplateVarInfo[] = [];
  const seen = new Set<string>();
  const upstreamVars = getUpstreamVars(nodeId, nodes, edges);

  let match: RegExpExecArray | null;
  while ((match = varPattern.exec(template)) !== null) {
    const name = match[1];
    if (seen.has(name)) continue;
    seen.add(name);

    const def = upstreamVars.find((v) => v.name === name);
    vars.push({
      name,
      defined: !!def,
      definedBy: def?.nodeId || null,
      definedByLabel: def?.label || null,
    });
  }

  return vars;
}

// ============================================================
// 4. 全量校验（返回所有问题列表）
// ============================================================

export function validateWorkflow(
  nodes: Node[],
  edges: Edge[],
): ValidationIssue[] {
  const issues: ValidationIssue[] = [];

  // 4a. 重复 outputVar
  const duplicates = findDuplicateOutputVars(nodes);
  for (const [varName, nodeIds] of duplicates) {
    for (const nodeId of nodeIds) {
      const node = nodes.find((n) => n.id === nodeId);
      issues.push({
        type: 'error',
        message: `变量名 "${varName}" 与节点 ${nodeIds.filter((id) => id !== nodeId).join(', ')} 冲突`,
        nodeId,
        field: 'outputVar',
      });
    }
  }

  // 4b. 模板中未定义的变量引用
  for (const node of nodes) {
    const data = node.data as unknown as AppNodeData;
    let template = '';
    if (data.nodeType === 'agent') {
      template = (data as any).inputTemplate || '';
    } else if (data.nodeType === 'tool') {
      template = (data as any).toolParams || '';
    } else if (data.nodeType === 'llm') {
      template = ((data as any).systemPrompt || '') + ' ' + ((data as any).userPrompt || '');
    }

    if (template) {
      const vars = resolveTemplateVars(template, node.id, nodes, edges);
      for (const v of vars) {
        if (!v.defined) {
          issues.push({
            type: 'warning',
            message: `模板中引用了 "{{${v.name}}}"，但上游节点未定义此变量`,
            nodeId: node.id,
            field: 'inputTemplate',
          });
        }
      }
    }
  }

  // 4c. 有输出变量但未被任何下游引用
  for (const node of nodes) {
    const data = node.data as unknown as AppNodeData;
    let outputVar = '';
    if (data.nodeType === 'agent' || data.nodeType === 'tool' || data.nodeType === 'llm' || data.nodeType === 'knowledge-retrieval') {
      outputVar = (data as any).outputVar || '';
    }
    if (!outputVar) continue;

    // 检查是否有下游节点引用了此变量
    const downstreamEdges = edges.filter((e) => e.source === node.id);
    if (downstreamEdges.length === 0) {
      issues.push({
        type: 'info',
        message: `节点输出变量 "${outputVar}" 未被下游节点引用`,
        nodeId: node.id,
        field: 'outputVar',
      });
    }
  }

  return issues;
}

// ============================================================
// 5. 提取模板中的所有 {{变量}}
// ============================================================

export function extractTemplateVars(template: string): string[] {
  const vars: string[] = [];
  const seen = new Set<string>();
  let match: RegExpExecArray | null;
  while ((match = varPattern.exec(template)) !== null) {
    if (!seen.has(match[1])) {
      seen.add(match[1]);
      vars.push(match[1]);
    }
  }
  return vars;
}