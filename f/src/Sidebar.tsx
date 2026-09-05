import React, { type DragEvent } from 'react';

// ============================================================
// 节点类型定义
// ============================================================

const nodeTypes = [
  { type: 'start', label: '开始', icon: 'fa-play', color: '#4b6cb7', desc: '工作流入口' },
  { type: 'agent', label: 'AI Agent', icon: 'fa-robot', color: '#4b6cb7', desc: 'AI 智能体节点' },
  { type: 'tool', label: '工具', icon: 'fa-wrench', color: '#4b6cb7', desc: '自定义工具调用' },
  { type: 'branch', label: '条件分支', icon: 'fa-code-branch', color: '#e67e22', desc: '按变量值分流（类似 switch/case）' },
  { type: 'note', label: '便签', icon: 'fa-sticky-note', color: '#f9a825', desc: '注释/便签，仅供说明' },
  // Dify 节点
  { type: 'llm', label: 'LLM 调用', icon: 'fa-brain', color: '#10b981', desc: '调用大语言模型' },
  { type: 'code', label: '代码执行', icon: 'fa-code', color: '#f59e0b', desc: '执行 Python 代码' },
  { type: 'answer', label: '回复输出', icon: 'fa-comment-dots', color: '#3b82f6', desc: '输出回复内容' },
  { type: 'knowledge-retrieval', label: '知识检索', icon: 'fa-book', color: '#8b5cf6', desc: '从知识库检索相关内容' },
  { type: 'question-classifier', label: '问题分类器', icon: 'fa-tags', color: '#06b6d4', desc: '对用户问题进行分类' },
  { type: 'assigner', label: '变量赋值', icon: 'fa-list-check', color: '#6b7280', desc: '为会话变量赋值' },
  { type: 'if-else', label: 'IF/ELSE 分支', icon: 'fa-code-branch', color: '#ef4444', desc: '条件判断分支' },
] as const;

// ============================================================
// 样式（匹配 g/ 的 cfg-sidebar 风格）
// ============================================================

const sidebarStyle: React.CSSProperties = {
  width: 220,
  background: 'linear-gradient(180deg, #f8f9fb 0%, #eef1f6 100%)',
  borderRight: '1px solid #e0e3e8',
  display: 'flex',
  flexDirection: 'column',
  overflow: 'hidden',
};

const titleBlock: React.CSSProperties = {
  padding: '16px 16px 14px',
  display: 'flex',
  alignItems: 'center',
  gap: 8,
};

const titleText: React.CSSProperties = {
  fontSize: 14,
  fontWeight: 700,
  color: '#333',
};

const scroller: React.CSSProperties = {
  flex: 1,
  overflowY: 'auto',
  padding: '8px 10px',
};

const groupLabel: React.CSSProperties = {
  fontSize: 10,
  fontWeight: 700,
  color: '#999',
  textTransform: 'uppercase',
  letterSpacing: '0.08em',
  margin: '12px 6px 6px',
};

const item: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  padding: '10px 14px',
  marginBottom: 2,
  borderRadius: 8,
  cursor: 'grab',
  fontSize: 13,
  color: '#555',
  fontWeight: 500,
  transition: 'all 0.12s ease',
  userSelect: 'none',
  borderLeft: '3px solid transparent',
};

const iconBox = (color: string): React.CSSProperties => ({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 28,
  height: 28,
  borderRadius: 7,
  background: `${color}15`,
  color: color,
  flexShrink: 0,
  fontSize: 13,
});

const hintStyle: React.CSSProperties = {
  fontSize: 10,
  color: '#999',
  padding: '12px 16px',
  borderTop: '1px solid #dce2ec',
  lineHeight: 1.6,
};

// ============================================================
// 组件
// ============================================================

export function Sidebar() {
  const onDragStart = (event: DragEvent<HTMLDivElement>, nodeType: string) => {
    event.dataTransfer.setData('application/reactflow-type', nodeType);
    event.dataTransfer.effectAllowed = 'move';
  };

  const groups = [
    { label: '基础', types: ['start'] },
    { label: '处理节点', types: ['agent', 'tool'] },
    { label: '数据 & 路由', types: ['branch'] },
    { label: '注释', types: ['note'] },
    { label: 'Dify 节点', types: ['llm', 'code', 'answer', 'knowledge-retrieval', 'question-classifier', 'assigner', 'if-else'] },
  ];

  return (
    <div style={sidebarStyle}>
      <div style={titleBlock}>
        <i className="fas fa-project-diagram" style={{ color: '#4b6cb7', fontSize: 16 }} />
        <span style={titleText}>节点面板</span>
      </div>

      <div style={scroller}>
        {groups.map((group) => {
          const items = nodeTypes.filter((n) => group.types.includes(n.type));
          return (
            <React.Fragment key={group.label}>
              <div style={groupLabel}>{group.label}</div>
              {items.map((n) => (
                <div
                  key={n.type}
                  className="sidebar-item"
                  style={item}
                  draggable
                  onDragStart={(e) => onDragStart(e, n.type)}
                  title={n.desc}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background = 'rgba(75,108,183,0.06)';
                    e.currentTarget.style.color = '#4b6cb7';
                    e.currentTarget.style.borderLeftColor = '#4b6cb7';
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background = 'transparent';
                    e.currentTarget.style.color = '#555';
                    e.currentTarget.style.borderLeftColor = 'transparent';
                  }}
                >
                  <div style={iconBox(n.color)}>
                    <i className={`fas ${n.icon}`} />
                  </div>
                  <span>{n.label}</span>
                </div>
              ))}
            </React.Fragment>
          );
        })}
      </div>

      <div style={hintStyle}>
        拖拽节点到画布上，连接创建流程。
      </div>
    </div>
  );
}
