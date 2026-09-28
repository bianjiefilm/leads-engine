import { describe, expect, it } from "vitest";
import { presentRoi, type RoiReport } from "@/lib/roi";

const complete: RoiReport = {
  disclaimer: "可关联不等于证明内容导致成交。本报告不是营销提升证明。",
  marketing_lift_proof: false,
  roi_status: "complete",
  funnel_reuse: "漏斗阶段数字复用 HUI-1694 / HUI-1695，本报告不另算第三套漏斗。",
  causal_lift: null,
  causal_lift_available: false,
  revenues: [
    { kind: "merchant_sales", authority: "manual", cents: 11100, roi: 1.11 },
    { kind: "painuo_service_order", authority: "platform_verified", cents: 22200, roi: 2.22 },
    { kind: "platform_tool", authority: "platform_verified", cents: 33300, roi: 3.33 },
  ],
  metrics: [
    { event_id: "imp", channel: "douyin", kind: "impression", value: null, sample_at: "2026-01-03T00:00:00Z" },
    { event_id: "play", channel: "wechat", kind: "play", value: 10, sample_at: "2026-01-03T02:00:00Z" },
  ],
  stages: [
    { key: "browse", available: false, count: null, reason: "未知不填 0" },
    { key: "won", available: true, count: 1 },
  ],
};

describe("roi presentation", () => {
  it("keeps an empty report on source stats and does not invent ROI", () => {
    const view = presentRoi(null);
    expect(view.roiStatus).toBe("source_stats_only");
    expect(view.showCompleteRoi).toBe(false);
    expect(view.marketingLiftProof).toBe(false);
    expect(view.disclaimer).toContain("可关联");
    expect(view.disclaimer).toContain("不是营销提升证明");
    expect(view.revenueLines.map((line) => line.label)).toEqual([
      "商家销售收入",
      "派诺服务订单",
      "平台工具收入",
    ]);
    expect(view.revenueLines.map((line) => line.cents)).toEqual([null, null, null]);
    expect(view.revenueLines.every((line) => line.roiText === "不展示")).toBe(true);
    expect(JSON.stringify(view)).not.toContain("合计");
  });

  it("shows each revenue ROI and does not add the three incomes", () => {
    const view = presentRoi(complete);
    expect(view.showCompleteRoi).toBe(true);
    expect(view.revenueLines.map((line) => line.cents)).toEqual([11100, 22200, 33300]);
    expect(view.revenueLines.map((line) => line.roiText)).toEqual(["1.11", "2.22", "3.33"]);
    expect(JSON.stringify(view)).not.toContain("66600");
    expect(view.marketingLiftProof).toBe(false);
  });

  it("hides ROI when the report is incomplete and leaves unknown metrics blank", () => {
    const view = presentRoi({
      ...complete,
      roi_status: "incomplete",
      marketing_lift_proof: true,
    });
    expect(view.showCompleteRoi).toBe(false);
    expect(view.marketingLiftProof).toBe(false);
    expect(view.revenueLines.every((line) => line.roiText === "不展示")).toBe(true);
    const empty = presentRoi(complete);
    expect(empty.metricLines.find((line) => line.kind === "impression")?.text).toBe("未知");
    expect(empty.metricLines.find((line) => line.kind === "play")?.text).toBe("10");
    expect(empty.stageLines.find((line) => line.key === "browse")?.text).toBe("未知");
    expect(empty.stageLines.find((line) => line.key === "won")?.text).toBe("1");
  });

  it("keeps two authorities of the same revenue apart", () => {
    const view = presentRoi({
      roi_status: "incomplete",
      revenues: [
        { kind: "merchant_sales", authority: "manual", cents: 100, roi: null },
        { kind: "merchant_sales", authority: "platform_verified", cents: 250, roi: null },
      ],
    });
    expect(view.revenueLines.filter((line) => line.label === "商家销售收入").map((line) => line.cents)).toEqual([100, 250]);
    expect(JSON.stringify(view)).not.toContain("350");
  });
});
