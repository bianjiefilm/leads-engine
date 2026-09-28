// HUI-1681 授权互动的页面模型。
// 没有真实渠道凭证时，展示层不接受“已验证 / 已支持某平台”的说法。
// 地址只带记录 id。分数和缺失的手机号都不产生触达动作。

export interface CapabilityInput {
  verification?: string;
  live_providers?: string[];
  note?: string;
}

export interface CapabilityView {
  verified: false;
  label: "未验证";
  providers: [];
  note: string;
}

export interface InteractionRow {
  candidate_id?: string;
  candidate_status?: string;
  score?: number;
  text?: string;
  phone?: string;
  phone_marketing?: boolean;
  sms_marketing?: boolean;
  auto_reach?: boolean;
  receipt_platform?: boolean;
  receipt_persisted?: boolean;
  receipt_human?: boolean;
}

const UNVERIFIED_NOTE = "没有真实渠道凭证。这里只登记授权连接器适配，不代表已支持任何平台。";

export function presentCapability(_input: CapabilityInput | null | undefined): CapabilityView {
  return {
    verified: false,
    label: "未验证",
    providers: [],
    note: UNVERIFIED_NOTE,
  };
}

export function leadHref(leadId: string): string {
  return `/leads/${leadId}`;
}

export function rowActions(row: InteractionRow): { confirm: boolean; reach: []; href: "/channel-interactions" } {
  return {
    confirm: row.candidate_status === "open" && Boolean(row.candidate_id),
    reach: [],
    href: "/channel-interactions",
  };
}

export function marketingLabels(row: Pick<InteractionRow, "phone" | "phone_marketing" | "sms_marketing">): string[] {
  if (!row.phone?.trim()) return [];
  const labels: string[] = [];
  if (row.phone_marketing) labels.push("电话");
  if (row.sms_marketing) labels.push("短信");
  return labels;
}

export function receiptLine(row: Pick<InteractionRow, "receipt_platform" | "receipt_persisted" | "receipt_human">): string {
  const parts: string[] = [];
  if (row.receipt_platform) parts.push("平台事件已接收");
  if (row.receipt_persisted) parts.push("获客已落库");
  if (row.receipt_human) parts.push("人工已处理");
  return parts.join(" · ");
}
