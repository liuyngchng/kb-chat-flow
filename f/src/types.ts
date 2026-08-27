// ============================================================
// 工作流设计工具的 TypeScript 类型定义
// 专注头脑风暴 + 自描述导出，不依赖后端
// 支持原生节点 + Dify 平台节点
// ============================================================

// ============================================================
// 节点类型
// ============================================================

export type NodeType = 'start' | 'agent' | 'tool' | 'variable' | 'branch' | 'note'
  // Dify 原生节点类型
  | 'llm' | 'code' | 'answer' | 'knowledge-retrieval' | 'question-classifier' | 'assigner' | 'if-else';

// ============================================================
// React Flow 内部节点 data 类型
// 每种节点都带 purpose 字段，便于 AI 理解
// ============================================================

export interface StartNodeData {
  nodeType: 'start';
  label: string;
  purpose: string;
}

export interface AgentNodeData {
  nodeType: 'agent';
  label: string;
  purpose: string;
  agentName: string;
  inputTemplate: string;
  outputVar: string;
  parallelGroup: string;
}

export interface ToolNodeData {
  nodeType: 'tool';
  label: string;
  purpose: string;
  toolName: string;
  toolParams: string;
  outputVar: string;
  parallelGroup: string;
}

export interface VariableNodeData {
  nodeType: 'variable';
  label: string;
  purpose: string;
  varName: string;
  varDesc: string;
}

export interface BranchNodeData {
  nodeType: 'branch';
  label: string;
  purpose: string;
  /** 分支依据的变量名，如 intent / user_query */
  inputVar: string;
}

export interface NoteNodeData {
  nodeType: 'note';
  label: string;
  purpose: string;
  content: string;
  color: string;
}

// ============================================================
// Dify 原生节点类型
// ============================================================

export interface LLMNodeData {
  nodeType: 'llm';
  label: string;
  purpose: string;
  /** 模型名称，如 deepseek-chat */
  modelName: string;
  /** 模型提供商，如 langgenius/deepseek/deepseek */
  modelProvider: string;
  /** 温度等参数 JSON */
  completionParams: string;
  /** system prompt 文本 */
  systemPrompt: string;
  /** user prompt 文本 */
  userPrompt: string;
  /** 上下文变量选择器（JSON 字符串） */
  contextVar: string;
  /** 记忆窗口大小，0=不启用 */
  memoryWindow: number;
  /** 输出变量名 */
  outputVar: string;
  /** 并行组 */
  parallelGroup: string;
}

export interface CodeNodeData {
  nodeType: 'code';
  label: string;
  purpose: string;
  /** Python 代码 */
  code: string;
  /** 输入变量定义（JSON 字符串） */
  inputVars: string;
  /** 输出变量定义（JSON 字符串） */
  outputs: string;
  /** 并行组 */
  parallelGroup: string;
}

export interface AnswerNodeData {
  nodeType: 'answer';
  label: string;
  purpose: string;
  /** 回复文本内容 */
  answerText: string;
  /** 并行组 */
  parallelGroup: string;
}

export interface KnowledgeRetrievalNodeData {
  nodeType: 'knowledge-retrieval';
  label: string;
  purpose: string;
  /** 知识库 dataset IDs（逗号分隔） */
  datasetIds: string;
  /** 查询变量选择器 */
  queryVar: string;
  /** 检索模式: single | multiple */
  retrievalMode: string;
  /** Top K */
  topK: number;
  /** 重排序模型 */
  rerankingModel: string;
  /** 重排序是否启用 */
  rerankingEnable: boolean;
  /** 分数阈值 */
  scoreThreshold: string;
  /** 输出变量名 */
  outputVar: string;
  /** 并行组 */
  parallelGroup: string;
}

export interface QuestionClassifierNodeData {
  nodeType: 'question-classifier';
  label: string;
  purpose: string;
  /** 分类定义（JSON 字符串: [{id, name}]） */
  classes: string;
  /** 分类指令 */
  instructions: string;
  /** 查询变量选择器 */
  queryVar: string;
  /** 模型名称 */
  modelName: string;
  /** 模型提供商 */
  modelProvider: string;
  /** 记忆是否启用 */
  memoryEnabled: boolean;
  /** 并行组 */
  parallelGroup: string;
}

export interface AssignerNodeData {
  nodeType: 'assigner';
  label: string;
  purpose: string;
  /** 赋值项列表（JSON 字符串） */
  items: string;
  /** 并行组 */
  parallelGroup: string;
}

/** if-else 分支条件 */
export interface IfElseCase {
  caseId: string;
  conditions: {
    comparisonOperator: string;
    value: string;
    varType: string;
    variableSelector: string[];
  }[];
  logicalOperator: 'and' | 'or';
}

export interface IfElseNodeData {
  nodeType: 'if-else';
  label: string;
  purpose: string;
  /** 条件分支列表（JSON 字符串） */
  cases: string;
  /** 并行组 */
  parallelGroup: string;
}

/** 所有节点 data 的联合类型 */
export type AppNodeData =
  | StartNodeData
  | AgentNodeData
  | ToolNodeData
  | VariableNodeData
  | BranchNodeData
  | NoteNodeData
  // Dify 节点
  | LLMNodeData
  | CodeNodeData
  | AnswerNodeData
  | KnowledgeRetrievalNodeData
  | QuestionClassifierNodeData
  | AssignerNodeData
  | IfElseNodeData;

// ============================================================
// 自描述导出格式 — 用于导出 JSON，AI 可直接读懂
// ============================================================

/** 导出格式中的单个节点 */
export interface DesignDocNode {
  id: string;
  type: NodeType;
  label: string;
  purpose: string;

  // Agent 相关
  agentName?: string;
  inputTemplate?: string;
  outputVar?: string;
  parallelGroup?: string;

  // Tool 相关
  toolName?: string;
  toolParams?: string;

  // Variable 相关
  varName?: string;
  varDesc?: string;

  // Branch 相关（条件分支）
  branchInputVar?: string;

  // Note 相关
  content?: string;
  color?: string;

  // ---- Dify 节点字段 ----

  // LLM 相关
  modelName?: string;
  modelProvider?: string;
  completionParams?: string;
  systemPrompt?: string;
  userPrompt?: string;
  contextVar?: string;
  memoryWindow?: number;

  // Code 相关
  code?: string;
  inputVars?: string;
  outputs?: string;

  // Answer 相关
  answerText?: string;

  // Knowledge Retrieval 相关
  datasetIds?: string;
  queryVar?: string;
  retrievalMode?: string;
  topK?: number;
  rerankingModel?: string;
  rerankingEnable?: boolean;
  scoreThreshold?: string;

  // Question Classifier 相关
  classes?: string;
  instructions?: string;
  memoryEnabled?: boolean;

  // Assigner 相关
  items?: string;

  // If-Else 相关
  cases?: string;
}

/** 导出格式中的边 */
export interface DesignDocEdge {
  from: string;
  to: string;
  /** 分支条件：匹配时走此边，default 表示兜底，无值表示无条件 */
  condition?: string;
  /** Dify 的 sourceHandle */
  sourceHandle?: string;
  /** @deprecated 使用 condition */
  label?: string;
}

/** 自描述工作流设计文档 */
export interface DesignDoc {
  /** schema 版本，AI 可据此判断格式 */
  _schema: 'workflow-design-doc/v1' | 'workflow-design-doc/v2';
  /** 工作流名称 */
  name: string;
  /** 简短描述 */
  description: string;
  /** 设计意图说明 — AI 读这个就知道整体要做什么 */
  purpose: string;
  /** 元数据 */
  metadata: {
    createdAt: string;
    tags: string[];
  };
  /** 顶层 I/O 摘要（AI 友好） */
  _summary?: {
    /** 工作流入参列表 */
    inputs: string[];
    /** 工作流出参列表 */
    outputs: string[];
  };
  /** 节点列表 */
  nodes: DesignDocNode[];
  /** 边列表 */
  edges: DesignDocEdge[];
}

// ============================================================
// 注意：变量节点可引用上游节点的 outputVar，
// 模板中使用 {{变量名}} 引用。
// 本工具不预设任何系统变量，变量名由用户自由定义。
// ============================================================