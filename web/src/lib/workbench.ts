// 销售工作台展示规则（HUI-1893）。队列和金额的真源在 Go 服务端，
// 这里只决定页面怎么把已经分好的事实画出来。

export const NARROW_ACTIONS = ["查看新线索", "记跟进", "安排下一次"] as const;

export const NARROW_BREAKPOINT = 720;

export function narrowWorkbenchFlow(width: number): { stacked: boolean; actions: string[]; hidden: string[] } {
  return {
    stacked: width <= NARROW_BREAKPOINT,
    actions: [...NARROW_ACTIONS],
    hidden: [],
  };
}

export const BUCKETS = [
  "unprocessed",
  "due_today",
  "waiting_reply",
  "stale",
  "assignment_exception",
  "needs_schedule",
  "human_takeover",
] as const;

export type BucketKey = (typeof BUCKETS)[number];

export const BUCKET_LABELS: Record<BucketKey, string> = {
  unprocessed: "未处理线索",
  due_today: "今天应跟进",
  waiting_reply: "等待回复",
  stale: "久未跟进",
  assignment_exception: "分配异常",
  needs_schedule: "待安排",
  human_takeover: "待接管",
};

export interface ReceptionFact {
  takeover?: boolean;
  owner_label?: string;
  label?: string;
  epoch?: number;
  version?: number;
}

export interface DeskItem {
  id: string;
  tenant_id?: string;
  kind: string;
  lead_id?: string;
  opportunity_id?: string;
  session_id?: string;
  assignee?: string;
  reception?: ReceptionFact;
  reason?: string;
  next?: { kind?: string; source?: string; label?: string; at?: string; auto_call?: boolean; auto_message?: boolean; create_order?: boolean };
  source?: { form?: string; activity?: string; channel?: string; at?: string };
  show_service_draft?: boolean;
  service_draft?: { present?: boolean; enabled?: boolean; reason?: string };
  force_opportunity?: boolean;
  statuses?: StatusFacts;
  owner_label?: string;
  assignment_reason?: string;
  allowed_contacts?: string[];
  sync?: { crm?: string };
  outreach_notice?: string;
  model_advice?: string;
}

export interface StatusFacts {
  submitted?: boolean;
  received?: boolean;
  assigned?: boolean;
  followed?: boolean;
  won?: boolean;
  paid?: boolean;
}

export interface MoneyFacts {
  customer_deal_cents: number | null;
  painuo_service_order_cents: number | null;
  platform_tool_spend_cents: number | null;
}

export function emptyBuckets(): Record<BucketKey, DeskItem[]> {
  return {
    unprocessed: [],
    due_today: [],
    waiting_reply: [],
    stale: [],
    assignment_exception: [],
    needs_schedule: [],
    human_takeover: [],
  };
}

// acceptDesk keeps only the current tenant. A missing tenant, or a row from
// another tenant, never stays on screen.
export function acceptDesk(
  tenantId: string | null,
  buckets: Partial<Record<BucketKey, DeskItem[]>> | null,
): Record<BucketKey, DeskItem[]> {
  const out = emptyBuckets();
  if (!tenantId || !buckets) return out;
  for (const key of BUCKETS) {
    out[key] = (buckets[key] ?? []).filter((item) => item.tenant_id === tenantId);
  }
  return out;
}

export function moneyView(money: MoneyFacts): { lines: { key: string; label: string; cents: number | null }[]; total?: undefined } {
  return {
    lines: [
      { key: "customer_deal", label: "客户成交", cents: money.customer_deal_cents },
      { key: "painuo_service", label: "派诺服务订单", cents: money.painuo_service_order_cents },
      { key: "platform_tool", label: "平台工具消费", cents: money.platform_tool_spend_cents },
    ],
  };
}

export function centsText(cents: number | null): string {
  if (cents == null) return "未知";
  return "¥" + (cents / 100).toFixed(2);
}

export function showServiceDraft(category: string): boolean {
  return category === "creative_service";
}

export function scopeCaption(scope?: string): string {
  if (scope === "tenant") return "全租户";
  if (scope === "own") return "我的范围";
  return "";
}

export function salesScopeIsNotOwner(sales?: string, owner?: string): boolean {
  return sales === "own" && owner === "tenant";
}

export function statusLine(facts: StatusFacts): string {
  const parts: string[] = [];
  if (facts.submitted) parts.push("已提交");
  if (facts.received) parts.push("已接收");
  if (facts.assigned) parts.push("已分配");
  if (facts.followed) parts.push("已跟进");
  if (facts.won) parts.push("已成交");
  if (facts.paid) parts.push("已回款");
  return parts.join(" · ");
}

export function sourceText(source?: DeskItem["source"]): string {
  if (!source) return "来源未记录";
  const parts = [source.form, source.activity, source.channel, source.at].filter((part) => !!part);
  return parts.length > 0 ? parts.join(" · ") : "来源未记录";
}

export function syncLine(sync?: { crm?: string } | null): string {
  if (sync?.crm === "received") return "CRM已接收";
  return "同步：未知";
}

export function deskState(statuses?: StatusFacts | null, sync?: { crm?: string } | null): string {
  const followed = statuses?.followed === true;
  const line = statusLine({ ...(statuses ?? {}), followed });
  if (followed) return line || "尚无权威状态";
  // An unknown CRM sync is not a follow-up, so it cannot add 「已跟进」.
  if (sync?.crm === "unknown" || !followed) {
    const parts = line.split(" · ").filter((part) => part !== "已跟进" && part !== "");
    return parts.join(" · ") || "尚无权威状态";
  }
  return line || "尚无权威状态";
}

export const JOINT_CHAIN_INCOMPLETE = "联合经营链未完成";

function refusedChainLabel(label: string): boolean {
  return (
    label === "已跟进" ||
    label.includes("自动触达已成功") ||
    label.includes("销售已收到") ||
    label.includes("白标经营链")
  );
}

export function jointChainLine(chain?: { status?: string; label?: string } | null): string {
  if (chain?.status === "complete" && chain.label && !refusedChainLabel(chain.label)) {
    return chain.label;
  }
  return JOINT_CHAIN_INCOMPLETE;
}

export function visibleOutreach(notice?: string): string {
  if (!notice || notice.includes("自动触达已成功") || notice.includes("销售已收到")) return "";
  return notice;
}

export interface RenderedLead {
  id: string;
  source: string;
  owner: string;
  state: string;
  next: string;
  sync: string;
  chain: string;
  outreachSubmitted: false;
}

export function renderLeadFacts(
  item: DeskItem,
  chain?: { status?: string; label?: string } | null,
): RenderedLead {
  return {
    id: item.id,
    source: sourceText(item.source),
    owner: ownerLine(item.owner_label, item.assignment_reason),
    state: deskState(item.statuses, item.sync),
    next: nextLine(item.next),
    sync: syncLine(item.sync),
    chain: jointChainLine(chain),
    outreachSubmitted: false,
  };
}

export function refreshedLead(before: { id: string }, after: { id: string }): { id: string } | null {
  if (!before.id || before.id !== after.id) return null;
  return after;
}

export function allowedContactText(channels?: string[]): string {
  if (!channels || channels.length === 0) return "无";
  const labels: Record<string, string> = { sms: "短信", phone: "电话", channel: "渠道内回复" };
  return channels.map((channel) => labels[channel] ?? channel).join("、");
}

export function modelAdviceLine(): string {
  return "真实模型未完成";
}

export function billingCaption(): string {
  return "不向用户报价";
}

export function ownerLine(label?: string, reason?: string): string {
  if (reason) return reason;
  return label || "未记录";
}

export function nextLine(next?: { source?: string; label?: string }): string {
  const label = next?.label || "安排下一次";
  if (next?.source === "manual") return "手工安排 · " + label;
  return "不是模型输出 · " + label;
}

export function serviceDraftControl(draft?: { present?: boolean; enabled?: boolean; reason?: string } | null): {
  present: boolean;
  enabled: boolean;
  reason: string;
} {
  if (!draft?.present) {
    return { present: false, enabled: false, reason: draft?.reason ?? "" };
  }
  return { present: true, enabled: !!draft.enabled, reason: draft.reason ?? "" };
}

export function serviceDraftClick(): { submitted: false; outreach: false; message: string } {
  return { submitted: false, outreach: false, message: "本轮不提交草稿" };
}

// receptionFactLine is the only reception sentence on the sales desk.
// A missing holder, epoch, or version is not filled in. A member id is not a name.
export function receptionFactLine(item: DeskItem): string {
  if (item.kind !== "reception") return "";
  const fact = item.reception;
  if (!fact || fact.epoch == null || fact.version == null || fact.epoch < 1 || fact.version < 1 || !fact.label) {
    return "接待事实未记录";
  }
  if (fact.takeover) {
    const owner = (fact.owner_label ?? "").trim();
    if (!owner || owner === item.assignee || owner.startsWith("mem_") || fact.label !== "人工接管") {
      return "接待事实未记录";
    }
    return `人工接管 · 当前负责人 ${owner} · 轮次 ${fact.epoch} · 版本 ${fact.version}`;
  }
  if (fact.label !== "尚未接管") return "接待事实未记录";
  return `尚未接管 · 轮次 ${fact.epoch} · 版本 ${fact.version}`;
}
