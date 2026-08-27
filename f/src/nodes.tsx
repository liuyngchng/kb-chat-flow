import React, { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type {
  AgentNodeData, ToolNodeData, VariableNodeData, BranchNodeData, StartNodeData, NoteNodeData,
  LLMNodeData, CodeNodeData, AnswerNodeData, KnowledgeRetrievalNodeData,
  QuestionClassifierNodeData, AssignerNodeData, IfElseNodeData,
} from './types';

// ============================================================
// Font Awesome 图标
// ============================================================

const Fa = ({ icon, style }: { icon: string; style?: React.CSSProperties }) => (
  <i className={`fas ${icon}`} style={{ fontSize: 14, ...style }} />
);

// ============================================================
// 节点卡片统一样式（匹配 g/ 的 .wf-node-card）
// ============================================================

const card: React.CSSProperties = {
  background: '#fff',
  borderRadius: 10,
  border: '2px solid #e0e3e8',
  minWidth: 200,
  boxShadow: '0 2px 6px rgba(0,0,0,0.08)',
  overflow: 'hidden',
  cursor: 'pointer',
  transition: 'border-color 0.2s, box-shadow 0.2s',
};

const header = (grad: string): React.CSSProperties => ({
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  padding: '9px 14px',
  background: `linear-gradient(to right, ${grad})`,
  color: '#fff',
  borderBottom: 'none',
});

const body: React.CSSProperties = {
  padding: '10px 14px',
  display: 'flex',
  flexDirection: 'column',
  gap: 4,
};

const row: React.CSSProperties = {
  fontSize: 11,
  color: '#666',
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  lineHeight: 1.5,
};

const tag = (bg: string, fg: string): React.CSSProperties => ({
  display: 'inline-block',
  padding: '2px 7px',
  borderRadius: 4,
  background: bg,
  color: fg,
  fontSize: 10,
  fontWeight: 500,
  marginTop: 2,
  alignSelf: 'flex-start',
});

const handleStyle = (color: string): React.CSSProperties => ({
  width: 12,
  height: 12,
  border: '2px solid #fff',
  background: color,
});

const mono = (color: string): React.CSSProperties => ({
  ...row,
  fontFamily: 'Consolas, Monaco, monospace',
  fontSize: 12,
  color,
});

// ============================================================
// StartNode
// ============================================================

export const StartNode = memo(function StartNode({ data }: { data: StartNodeData }) {
  const c = '#4b6cb7';
  return (
    <div style={card}>
      <div style={header('#4b6cb7, #182848')}>
        <Fa icon="fa-play" /> <span style={{ fontWeight: 600, fontSize: 13 }}>{data.label}</span>
      </div>
      <div style={body}>
        <span style={row}>用户问题入口</span>
        {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
      </div>
      <Handle type="source" position={Position.Right} style={handleStyle(c)} />
    </div>
  );
});

// ============================================================
// AgentNode
// ============================================================

export const AgentNode = memo(function AgentNode({ data }: { data: AgentNodeData }) {
  const c = '#4b6cb7';
  return (
    <div style={card}>
      <div style={header('#4b6cb7, #182848')}>
        <Fa icon="fa-robot" /> <span style={{ fontWeight: 600, fontSize: 13 }}>{data.label}</span>
      </div>
      <div style={body}>
        {data.outputVar && <span style={row}>输出: {`{{${data.outputVar}}}`}</span>}
        {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
        {data.parallelGroup && <span style={tag('#e8eaf6', '#4b6cb7')}>∥ {data.parallelGroup}</span>}
      </div>
      <Handle type="target" position={Position.Left} style={handleStyle(c)} />
      <Handle type="source" position={Position.Right} style={handleStyle(c)} />
    </div>
  );
});

// ============================================================
// ToolNode
// ============================================================

export const ToolNode = memo(function ToolNode({ data }: { data: ToolNodeData }) {
  const c = '#4b6cb7';
  return (
    <div style={card}>
      <div style={header('#4b6cb7, #182848')}>
        <Fa icon="fa-wrench" /> <span style={{ fontWeight: 600, fontSize: 13 }}>{data.label}</span>
      </div>
      <div style={body}>
        {data.toolName && <span style={row}>工具: {data.toolName}</span>}
        {data.outputVar && <span style={row}>输出: {`{{${data.outputVar}}}`}</span>}
        {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
      </div>
      <Handle type="target" position={Position.Left} style={handleStyle(c)} />
      <Handle type="source" position={Position.Right} style={handleStyle(c)} />
    </div>
  );
});

// ============================================================
// VariableNode
// ============================================================

export const VariableNode = memo(function VariableNode({ data }: { data: VariableNodeData }) {
  const c = '#4b6cb7';
  return (
    <div style={card}>
      <div style={header('#4b6cb7, #182848')}>
        <Fa icon="fa-database" /> <span style={{ fontWeight: 600, fontSize: 13 }}>{data.label}</span>
      </div>
      <div style={body}>
        <code style={mono('#4b6cb7')}>
          {`{{${data.varName}}}`}
        </code>
        {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
      </div>
      <Handle type="source" position={Position.Right} style={handleStyle(c)} />
    </div>
  );
});

// ============================================================
// BranchNode（条件分支 — switch/case）
// ============================================================

export const BranchNode = memo(function BranchNode({ data }: { data: BranchNodeData }) {
  const c = '#4b6cb7';
  return (
    <div style={{
      ...card,
      borderLeft: `6px solid #e67e22`,
    }}>
      <div style={header('#4b6cb7, #182848')}>
        <Fa icon="fa-code-branch" /> <span style={{ fontWeight: 600, fontSize: 13 }}>{data.label}</span>
        <span style={{ marginLeft: 'auto', fontSize: 9, background: 'rgba(255,255,255,0.2)', padding: '2px 7px', borderRadius: 4 }}>
          SWITCH
        </span>
      </div>
      <div style={body}>
        {data.inputVar && <span style={row}>依据: {`{{${data.inputVar}}}`}</span>}
        {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
      </div>
      <Handle type="target" position={Position.Left} style={handleStyle(c)} />
      <Handle type="source" position={Position.Right} style={handleStyle(c)} />
    </div>
  );
});

// ============================================================
// NoteNode（便签/注释节点）
// ============================================================

export const NoteNode = memo(function NoteNode({ data }: { data: NoteNodeData }) {
  const noteColors: Record<string, { bg: string; border: string; text: string }> = {
    yellow: { bg: '#fff9c4', border: '#f9a825', text: '#5d4037' },
    green: { bg: '#c8e6c9', border: '#43a047', text: '#1b5e20' },
    blue: { bg: '#bbdefb', border: '#1e88e5', text: '#0d47a1' },
    pink: { bg: '#f8bbd0', border: '#e91e63', text: '#880e4f' },
    purple: { bg: '#e1bee7', border: '#8e24aa', text: '#4a148c' },
  };
  const c = noteColors[data.color] || noteColors.yellow;

  return (
    <div style={{
      background: c.bg,
      border: `2px solid ${c.border}`,
      borderRadius: 10,
      minWidth: 180,
      maxWidth: 280,
      padding: '12px 16px',
      boxShadow: '0 2px 8px rgba(0,0,0,0.08)',
      cursor: 'pointer',
      fontFamily: 'inherit',
    }}>
      <div style={{ fontWeight: 600, fontSize: 12, color: c.text, marginBottom: 4 }}>
        {data.label}
      </div>
      <div style={{ fontSize: 11, color: c.text, lineHeight: 1.5, whiteSpace: 'pre-wrap' }}>
        {data.content || '（空）'}
      </div>
    </div>
  );
});

// ============================================================
// Dify 节点组件
// ============================================================

/** 通用 Dify 卡片：头部渐变 + 图标 + 标题 + 徽章 */
function DifyCard({ icon, label, badge, grad, children, color }: {
  icon: string; label: string; badge?: string; grad: string;
  children: React.ReactNode; color: string;
}) {
  return (
    <div style={{ ...card, borderLeft: `6px solid ${color}` }}>
      <div style={header(grad)}>
        <Fa icon={icon} /> <span style={{ fontWeight: 600, fontSize: 13 }}>{label}</span>
        {badge && <span style={{ marginLeft: 'auto', fontSize: 9, background: 'rgba(255,255,255,0.2)', padding: '2px 7px', borderRadius: 4 }}>{badge}</span>}
      </div>
      <div style={body}>{children}</div>
      <Handle type="target" position={Position.Left} style={handleStyle(color)} />
      <Handle type="source" position={Position.Right} style={handleStyle(color)} />
    </div>
  );
}

// ---- LLM ----
export const LLMNode = memo(function LLMNode({ data }: { data: LLMNodeData }) {
  const c = '#10b981';
  return (
    <DifyCard icon="fa-brain" label={data.label} badge="LLM" grad="#10b981, #059669" color={c}>
      {data.modelName && <span style={row}>模型: {data.modelName}</span>}
      {data.systemPrompt && <span style={row}>系统提示: {data.systemPrompt.slice(0, 40)}{data.systemPrompt.length > 40 ? '…' : ''}</span>}
      {data.memoryWindow > 0 && <span style={tag('#d1fae5', '#047857')}>记忆 {data.memoryWindow} 轮</span>}
      {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
    </DifyCard>
  );
});

// ---- Code ----
export const CodeNode = memo(function CodeNode({ data }: { data: CodeNodeData }) {
  const c = '#f59e0b';
  return (
    <DifyCard icon="fa-code" label={data.label} badge="CODE" grad="#f59e0b, #d97706" color={c}>
      {data.code && <span style={row}>代码: {data.code.slice(0, 40)}{data.code.length > 40 ? '…' : ''}</span>}
      {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
    </DifyCard>
  );
});

// ---- Answer ----
export const AnswerNode = memo(function AnswerNode({ data }: { data: AnswerNodeData }) {
  const c = '#3b82f6';
  return (
    <DifyCard icon="fa-comment-dots" label={data.label} badge="ANSWER" grad="#3b82f6, #2563eb" color={c}>
      {data.answerText && <span style={row}>回复: {data.answerText.slice(0, 40)}{data.answerText.length > 40 ? '…' : ''}</span>}
      {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
    </DifyCard>
  );
});

// ---- Knowledge Retrieval ----
export const KnowledgeRetrievalNode = memo(function KnowledgeRetrievalNode({ data }: { data: KnowledgeRetrievalNodeData }) {
  const c = '#8b5cf6';
  return (
    <DifyCard icon="fa-book" label={data.label} badge="RETRIEVE" grad="#8b5cf6, #7c3aed" color={c}>
      {data.datasetIds && <span style={row}>知识库: {data.datasetIds.split(',').length} 个</span>}
      <span style={row}>模式: {data.retrievalMode === 'multiple' ? '多路检索' : '单路检索'} · Top{data.topK}</span>
      {data.rerankingEnable && <span style={tag('#ede9fe', '#6d28d9')}>重排: {data.rerankingModel}</span>}
      {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
    </DifyCard>
  );
});

// ---- Question Classifier ----
export const QuestionClassifierNode = memo(function QuestionClassifierNode({ data }: { data: QuestionClassifierNodeData }) {
  const c = '#06b6d4';
  let classNames: string[] = [];
  try {
    classNames = JSON.parse(data.classes).map((c: any) => c.name);
  } catch { /* ignore */ }
  return (
    <DifyCard icon="fa-tags" label={data.label} badge="CLASSIFY" grad="#06b6d4, #0891b2" color={c}>
      {classNames.length > 0 && <span style={row}>分类: {classNames.slice(0, 4).join(' / ')}{classNames.length > 4 ? '…' : ''}</span>}
      {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
    </DifyCard>
  );
});

// ---- Assigner ----
export const AssignerNode = memo(function AssignerNode({ data }: { data: AssignerNodeData }) {
  const c = '#6b7280';
  let count = 0;
  try {
    count = JSON.parse(data.items).length;
  } catch { /* ignore */ }
  return (
    <DifyCard icon="fa-list-check" label={data.label} badge="ASSIGN" grad="#6b7280, #4b5563" color={c}>
      {count > 0 && <span style={row}>赋值项: {count} 个</span>}
      {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
    </DifyCard>
  );
});

// ---- If-Else ----
export const IfElseNode = memo(function IfElseNode({ data }: { data: IfElseNodeData }) {
  const c = '#ef4444';
  let caseCount = 0;
  try {
    caseCount = JSON.parse(data.cases).length;
  } catch { /* ignore */ }
  return (
    <DifyCard icon="fa-code-branch" label={data.label} badge="IF-ELSE" grad="#ef4444, #dc2626" color={c}>
      {caseCount > 0 && <span style={row}>分支数: {caseCount} 个</span>}
      {data.purpose && <span style={{ ...row, color: '#888', fontStyle: 'italic' }}>💡 {data.purpose}</span>}
    </DifyCard>
  );
});

// ============================================================
// 注册表
// ============================================================

export const nodeTypes = {
  start: StartNode,
  agent: AgentNode,
  tool: ToolNode,
  variable: VariableNode,
  branch: BranchNode,
  note: NoteNode,
  // Dify 节点
  llm: LLMNode,
  code: CodeNode,
  answer: AnswerNode,
  'knowledge-retrieval': KnowledgeRetrievalNode,
  'question-classifier': QuestionClassifierNode,
  assigner: AssignerNode,
  'if-else': IfElseNode,
};
