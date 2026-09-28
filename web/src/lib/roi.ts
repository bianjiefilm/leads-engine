// 来源与费用怎么展示（HUI-1696）。数字由服务端复算，这里只决定画法。
// 三类收入分开。未知指标保持未知。没有完整事实就不画 ROI。

export const ROI_DISCLAIMER =
  "可关联不等于证明内容导致成交。没有实验引用就不写因果提升。本报告不是营销提升证明。";

export const REVENUE_KINDS = [
  { key: "merchant_sales", label: "商家销售收入" },
  { key: "painuo_service_order", label: "派诺服务订单" },
  { key: "platform_tool", label: "平台工具收入" },
] as const;

export interface RoiMoneyLine {
  kind: string;
  authority?: string;
  cents: number | null;
  roi?: number | null;
}

export interface RoiMetric {
  event_id?: string;
  channel?: string;
  kind: string;
  value: number | null;
  sample_at?: string;
}

export interface RoiStage {
  key: string;
  available?: boolean;
  count: number | null;
  reason?: string;
}

export interface RoiReport {
  disclaimer?: string;
  marketing_lift_proof?: boolean;
  roi_status?: string;
  funnel_reuse?: string;
  causal_lift?: number | null;
  causal_lift_available?: boolean;
  causal_reason?: string;
  attribution_rule?: string;
  unassociated_ratio?: number | null;
  revenues?: RoiMoneyLine[];
  costs?: { kind: string; authority?: string; cents: number | null }[];
  metrics?: RoiMetric[];
  stages?: RoiStage[];
  source_chain?: { id: string; kind: string; gap?: boolean; ref?: string }[];
  production_cents?: number | null;
}

export interface RevenueViewLine {
  key: string;
  label: string;
  authority: string;
  cents: number | null;
  roiText: string;
}

export interface RoiView {
  disclaimer: string;
  marketingLiftProof: false;
  roiStatus: string;
  showCompleteRoi: boolean;
  funnelReuse: string;
  revenueLines: RevenueViewLine[];
  metricLines: { key: string; kind: string; channel: string; text: string }[];
  stageLines: { key: string; text: string }[];
}

export function presentRoi(report: RoiReport | null): RoiView {
  const status = report?.roi_status ?? "source_stats_only";
  const showCompleteRoi = status === "complete";
  const revenueLines: RevenueViewLine[] = [];
  for (const kind of REVENUE_KINDS) {
    const matches = (report?.revenues ?? []).filter((line) => line.kind === kind.key);
    if (matches.length === 0) {
      revenueLines.push({ key: kind.key, label: kind.label, authority: "", cents: null, roiText: "不展示" });
      continue;
    }
    matches.forEach((line, index) => {
      const roi = showCompleteRoi ? line.roi ?? null : null;
      revenueLines.push({
        key: matches.length === 1 ? kind.key : `${kind.key}-${index}`,
        label: kind.label,
        authority: line.authority ?? "",
        cents: line.cents ?? null,
        roiText: roi == null ? "不展示" : String(roi),
      });
    });
  }
  return {
    disclaimer: report?.disclaimer?.trim() || ROI_DISCLAIMER,
    marketingLiftProof: false,
    roiStatus: status,
    showCompleteRoi,
    funnelReuse: report?.funnel_reuse ?? "漏斗阶段数字复用 HUI-1694 / HUI-1695，本报告不另算第三套漏斗。",
    revenueLines,
    metricLines: (report?.metrics ?? []).map((line, index) => ({
      key: line.event_id || `${line.kind}-${index}`,
      kind: line.kind,
      channel: line.channel ?? "",
      text: line.value == null ? "未知" : String(line.value),
    })),
    stageLines: (report?.stages ?? []).map((line) => ({
      key: line.key,
      text: line.available && line.count != null ? String(line.count) : "未知",
    })),
  };
}

export function centsText(cents: number | null): string {
  if (cents == null) return "未知";
  return "¥" + (cents / 100).toFixed(2);
}

export const STATUS_LABELS: Record<string, string> = {
  source_stats_only: "只有来源统计",
  incomplete: "数据不完整",
  complete: "按收入口径分开的 ROI",
};
