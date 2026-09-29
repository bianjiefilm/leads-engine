// 意向分级在页面上怎么说。分数来自服务端规则，这里不计算成交概率，
// 也不提供外呼、短信、拉群或建单。

export interface GradeCitation {
  evidence_id: string;
  excerpt: string;
}

export interface GradeSuggestion {
  kind: string;
  label: string;
  auto_call?: boolean;
  auto_sms?: boolean;
  auto_group?: boolean;
  create_order?: boolean;
}

export interface GradeInput {
  grade: string;
  reason: string;
  citations: GradeCitation[];
  missing_fields: string[];
  fresh_until: string;
  rule_version: string;
  model_version: string;
  calibrated: boolean;
  confidence?: number;
  disclaimer: string;
  suggestion: GradeSuggestion;
  human_locked: boolean;
  stale: boolean;
  stale_reason?: string;
}

export interface GradeView {
  label: string;
  reason: string;
  citations: string[];
  missing: string[];
  freshness: string;
  version: string;
  disclaimer: string;
  suggestion: string;
  probability: null;
  confidenceText: string;
  actions: Array<"修正分级" | "标记误判">;
  humanLocked: boolean;
  staleText: string;
}

const MISSING_LABELS: Record<string, string> = {
  buyer: "买家",
  need: "需求",
  timeline: "时间",
  quantity_or_budget: "数量或预算",
};

const STALE_LABELS: Record<string, string> = {
  new_message: "新消息",
  retraction: "撤回",
  human_correction: "人工更正",
  expired: "已过时效",
};

export function gradeLabel(grade: string): string {
  switch (grade) {
    case "high":
      return "高";
    case "medium":
      return "中";
    case "low":
      return "低";
    case "insufficient":
      return "信息不足";
    default:
      return "未分级";
  }
}

export function presentAssessment(input: GradeInput): GradeView {
  const staleName = input.stale_reason ? STALE_LABELS[input.stale_reason] : "";
  return {
    label: gradeLabel(input.grade),
    reason: input.reason,
    citations: input.citations.map((item) => item.excerpt).filter((item) => item !== ""),
    missing: input.missing_fields.map((field) => MISSING_LABELS[field] ?? field),
    freshness: input.fresh_until,
    version: `${input.rule_version} / ${input.model_version}`,
    disclaimer: input.disclaimer,
    suggestion: input.suggestion.label,
    probability: null,
    confidenceText: "",
    actions: ["修正分级", "标记误判"],
    humanLocked: input.human_locked,
    staleText: input.stale ? `旧评分已过时：${staleName || "需重算"}` : "",
  };
}

// 分数不能长出触达按钮。调用方即使传来 auto_*，这里也返回空。
export function outreachControls(_view: GradeView): string[] {
  return [];
}

// 权限位即使为真，页面也不渲染触达按钮。
export function outreachButtons(_permissions?: {
  call?: boolean;
  direct_message?: boolean;
  create_order?: boolean;
}): string[] {
  return [];
}

export function outreachGranted(permissions?: {
  call?: boolean;
  direct_message?: boolean;
  create_order?: boolean;
} | null): boolean {
  return Boolean(permissions?.call || permissions?.direct_message || permissions?.create_order);
}

// 没有模型凭证时，任何回执都不是完成。
export function modelStatusText(_verdict?: string): string {
  return "真实模型未完成";
}

export function originLabel(origin: string): string {
  switch (origin) {
    case "fixture":
      return "夹具";
    case "human":
      return "人工";
    default:
      return "规则";
  }
}

export function salesCorrectionActions(): Array<"修正分级" | "驳回"> {
  return ["修正分级", "驳回"];
}
