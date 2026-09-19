// 商机域纯函数测试:标签语义红线(won=人工标记成交,无「已收款/已支付」)、
// 金额「未知」语义(NULL 不当 0)、按钮权限显隐镜像、单类别统计口径。
import { describe, expect, it } from "vitest";
import {
  BUSINESS_CATEGORIES,
  CATEGORY_LABELS,
  OPPORTUNITY_STAGES,
  STAGE_LABELS,
  amountView,
  canTransitionStage,
  expectedCloseText,
  probabilityText,
  stageActionsFor,
  statsLine,
  timelineLabel,
  type OpportunityStatsBody,
} from "../src/lib/opportunity";

// ---- 语义红线:文案守卫 --------------------------------------------------------

describe("wording guard (no payment semantics)", () => {
  const userFacing: string[] = [
    ...OPPORTUNITY_STAGES,
    ...Object.values(STAGE_LABELS),
    ...Object.values(CATEGORY_LABELS),
    stageActionsFor("open", true).map((a) => a.label).join("|"),
    timelineLabel({ from_stage: "negotiation", to_stage: "won", changed_by: "m", changed_at: "t", note: "" }),
  ];

  it("never presents won as a payment fact", () => {
    expect(STAGE_LABELS.won).toBe("人工标记成交");
  });

  it("no user-facing label contains payment wording", () => {
    for (const s of userFacing) {
      expect(s).not.toContain("已收款");
      expect(s).not.toContain("已支付");
      expect(s).not.toContain("收款");
      expect(s).not.toContain("支付");
    }
  });
});

// ---- 类别隔离 -----------------------------------------------------------------

describe("category isolation", () => {
  it("exposes exactly the two categories, no merged view", () => {
    expect([...BUSINESS_CATEGORIES]).toEqual(["merchant_customer", "creative_service"]);
    expect(CATEGORY_LABELS.merchant_customer).toBe("商家经营销售");
    expect(CATEGORY_LABELS.creative_service).toContain("创意服务");
  });

  it("statsLine never sums across categories and keeps unknown out of the total", () => {
    const s: OpportunityStatsBody = {
      business_category: "merchant_customer",
      total: 2,
      funnel: { open: 2 },
      amounts: { known_total_cents: 10000, known_count: 1, unknown_count: 1 },
    };
    const line = statsLine(s);
    expect(line).toContain("商家经营销售");
    expect(line).toContain("¥100.00");
    expect(line).toContain("1 单金额未知");
    expect(line).not.toContain("¥101.00"); // 未知绝不当 0 计入,更不许补造
  });
});

// ---- 金额未知语义 ---------------------------------------------------------------

describe("amount view", () => {
  it("renders NULL/unknown amounts as 未知, never 0", () => {
    for (const v of [amountView(null, "unknown"), amountView(undefined, "manual"), amountView(500, "unknown")]) {
      expect(v.text).toBe("未知");
      expect(v.unknown).toBe(true);
    }
  });

  it("renders manual amounts with a source label", () => {
    const v = amountView(123456, "manual");
    expect(v.text).toBe("¥1234.56");
    expect(v.unknown).toBe(false);
    expect(v.sourceLabel).toBe("人工录入");
  });

  it("probability and expected close fall back to 未知", () => {
    expect(probabilityText(null)).toBe("未知");
    expect(probabilityText(40)).toBe("成交概率 40%");
    expect(expectedCloseText(null)).toBe("未知");
    expect(expectedCloseText("2026-10-01T00:00:00Z")).toBe("预计成交 2026-10-01");
  });
});

// ---- 按钮权限显隐(镜像服务端矩阵) ---------------------------------------------

describe("stage action visibility mirrors the server matrix", () => {
  const record = { assigned_member_id: "mem_sales_a1" };

  it("owner always may transition", () => {
    expect(canTransitionStage({ role: "owner", memberId: "mem_owner", enabled: true }, {})).toBe(true);
    expect(stageActionsFor("open", true).length).toBe(OPPORTUNITY_STAGES.length - 1);
  });

  it("assignee sales may transition; non-assignee gets no buttons", () => {
    expect(canTransitionStage({ role: "sales", memberId: "mem_sales_a1", enabled: true }, record)).toBe(true);
    expect(canTransitionStage({ role: "sales", memberId: "mem_sales_a2", enabled: true }, record)).toBe(false);
    expect(canTransitionStage({ role: "agent", memberId: "mem_sales_a2", enabled: true }, record)).toBe(false);
    expect(stageActionsFor("open", false)).toEqual([]);
  });

  it("disabled members get no buttons", () => {
    expect(canTransitionStage({ role: "owner", memberId: "mem_owner", enabled: false }, record)).toBe(false);
  });

  it("actions exclude the current stage and use guarded labels", () => {
    const actions = stageActionsFor("won", true).map((a) => a.to);
    expect(actions).not.toContain("won");
    expect(actions).toContain("open"); // 重开也是普通转换
    expect(actions).toContain("closed_lost");
  });
});

// ---- 时间线 ---------------------------------------------------------------------

describe("timeline label", () => {
  it("renders the creation row and transitions", () => {
    expect(
      timelineLabel({ from_stage: "", to_stage: "open", changed_by: "m1", changed_at: "t", note: "created" }),
    ).toContain("创建 → 初步接洽");
    expect(
      timelineLabel({ from_stage: "open", to_stage: "qualified", changed_by: "m1", changed_at: "t", note: "ok" }),
    ).toContain("初步接洽 → 已确认意向");
    expect(
      timelineLabel({ from_stage: "open", to_stage: "qualified", changed_by: "m1", changed_at: "t", note: "ok" }),
    ).toContain("备注:ok");
  });
});
