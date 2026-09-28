// 销售工作台展示规则（HUI-1893）。队列和金额的真源在 Go 服务端，
// 这里只决定页面怎么把已经分好的事实画出来。

export const NARROW_ACTIONS = ["查看新线索", "记跟进", "安排下一次"] as const;

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

export interface DeskItem {
  id: string;
  tenant_id?: string;
  kind: string;
  lead_id?: string;
  opportunity_id?: string;
  session_id?: string;
  reason?: string;
  next?: { kind?: string; source?: string; label?: string; at?: string; auto_call?: boolean; auto_message?: boolean; create_order?: boolean };
  source?: { form?: string; activity?: string; channel?: string; at?: string };
  show_service_draft?: boolean;
  force_opportunity?: boolean;
  statuses?: StatusFacts;
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
