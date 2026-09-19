// 商机域纯函数(HUI-1693 / FEAT-0194)。页面组件只做渲染;所有业务判断的
// 真源在 Go 服务端,这里的函数仅镜像服务端规则用于展示(按钮显隐/文案),
// 服务端仍对每个动作重新鉴权。
//
// 语义红线(FEAT-0194 分域补充):
//   - won 显示为「人工标记成交」——用户可见文案绝不出现「已收款/已支付」;
//   - 金额缺失(NULL)显示「未知」,绝不当 0,不补造数据;
//   - 商家经营销售与创意服务从标签到统计完全隔离,不做跨类别合计。

export const OPPORTUNITY_STAGES = [
  "open",
  "qualified",
  "proposal",
  "negotiation",
  "won",
  "closed_lost",
] as const;

export type OpportunityStage = (typeof OPPORTUNITY_STAGES)[number];

export const STAGE_LABELS: Record<OpportunityStage, string> = {
  open: "初步接洽",
  qualified: "已确认意向",
  proposal: "已出方案",
  negotiation: "商务谈判",
  won: "人工标记成交", // 红线:仅人工标记,非收款事实,成交结果待核实
  closed_lost: "已关闭(未成交)",
};

export const BUSINESS_CATEGORIES = ["merchant_customer", "creative_service"] as const;

export type BusinessCategory = (typeof BUSINESS_CATEGORIES)[number];

export const CATEGORY_LABELS: Record<BusinessCategory, string> = {
  merchant_customer: "商家经营销售",
  creative_service: "创意服务(设计/视频等)",
};

export function isBusinessCategory(v: string | null | undefined): v is BusinessCategory {
  return v === "merchant_customer" || v === "creative_service";
}

export interface CallerView {
  role?: string;
  memberId?: string;
  enabled?: boolean;
}

export interface StageRecord {
  assigned_member_id?: string;
}

// canTransitionStage 镜像服务端 authz.ActionUpdate 矩阵:
//   owner:本租户任意记录;sales/agent:仅自己名下;disabled 一律不可。
// 最终裁决在服务端(非 assignee 会被 404 掩码),这里只用于按钮显隐。
export function canTransitionStage(caller: CallerView, record: StageRecord): boolean {
  if (!caller || caller.enabled === false) return false;
  if (caller.role === "owner") return true;
  if (caller.role === "sales" || caller.role === "agent") {
    return !!caller.memberId && caller.memberId === record.assigned_member_id;
  }
  return false;
}

export const UNKNOWN_TEXT = "未知";

export interface AmountView {
  text: string;
  unknown: boolean;
  sourceLabel: string;
}

// formatCents renders 分 as ¥元;仅供已核实金额使用。
export function formatCents(cents: number): string {
  return "¥" + (cents / 100).toFixed(2);
}

// amountView:NULL 金额或来源 unknown 一律「未知」(进未知桶,不当 0);
// manual 金额带「人工录入」来源标识,便于区分可核实金额。
export function amountView(
  amountCents: number | null | undefined,
  amountSource: string | null | undefined,
): AmountView {
  if (amountCents === null || amountCents === undefined || amountSource !== "manual") {
    return { text: UNKNOWN_TEXT, unknown: true, sourceLabel: "无金额(未知)" };
  }
  return { text: formatCents(amountCents), unknown: false, sourceLabel: "人工录入" };
}

export function probabilityText(p: number | null | undefined): string {
  if (p === null || p === undefined) return UNKNOWN_TEXT;
  return `成交概率 ${p}%`;
}

export function expectedCloseText(v: string | null | undefined): string {
  if (!v) return UNKNOWN_TEXT;
  return `预计成交 ${v.slice(0, 10)}`;
}

// stageActionsFor lists the stage buttons for a record. allowed=false (无权限
// 或非 assignee)返回空数组 —— 权限外用户不渲染任何阶段转换按钮。
export function stageActionsFor(
  current: OpportunityStage,
  allowed: boolean,
): { to: OpportunityStage; label: string }[] {
  if (!allowed) return [];
  return OPPORTUNITY_STAGES.filter((s) => s !== current).map((to) => ({
    to,
    label: `转为「${STAGE_LABELS[to]}」`,
  }));
}

export interface StageEvent {
  from_stage: string;
  to_stage: string;
  changed_by: string;
  changed_at: string;
  note: string;
}

// timelineLabel renders one audited event; from_stage '' is the creation row.
export function timelineLabel(e: StageEvent): string {
  const from = e.from_stage === "" ? "创建" : STAGE_LABELS[e.from_stage as OpportunityStage] ?? e.from_stage;
  const to = STAGE_LABELS[e.to_stage as OpportunityStage] ?? e.to_stage;
  const note = e.note ? `(备注:${e.note})` : "";
  return `${from} → ${to} ${note}`.trim();
}

export interface OpportunityStatsBody {
  business_category: string;
  total: number;
  funnel: Partial<Record<OpportunityStage, number>>;
  amounts: { known_total_cents: number; known_count: number; unknown_count: number };
}

// statsLine:单类别统计的一行说明;金额只有「可核实合计」,未知数量单独展示,
// 绝不把未知金额折算进合计,也绝不跨类别相加。
export function statsLine(s: OpportunityStatsBody): string {
  const cat = isBusinessCategory(s.business_category)
    ? CATEGORY_LABELS[s.business_category]
    : s.business_category;
  const unknown = s.amounts.unknown_count;
  const unknownPart = unknown > 0 ? `,另有 ${unknown} 单金额未知(不计入合计)` : "";
  return `${cat}:${s.total} 单,可核实金额合计 ${formatCents(s.amounts.known_total_cents)}${unknownPart}`;
}
