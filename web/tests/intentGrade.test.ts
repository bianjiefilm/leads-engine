import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  gradeLabel,
  modelStatusText,
  originLabel,
  outreachControls,
  presentAssessment,
  salesCorrectionActions,
  type GradeInput,
} from "../src/lib/intentGrade";

const high: GradeInput = {
  grade: "high",
  reason: "出现采购、合同或打款等购买承诺。",
  citations: [{ evidence_id: "ev-1", excerpt: "请发合同和报价" }],
  missing_fields: ["buyer"],
  fresh_until: "2026-09-29T08:00:00Z",
  rule_version: "rules-hui-1684-v1",
  model_version: "none",
  calibrated: false,
  confidence: 0.91,
  disclaimer: "规则评分，不是真人成交预测，也不是校准后的成交概率。",
  suggestion: {
    kind: "suggest_follow_up",
    label: "建议销售人工确认下一步。是否可联系由授权和渠道规则决定。",
    auto_call: false,
    auto_sms: false,
    auto_group: false,
    create_order: false,
  },
  human_locked: false,
  stale: false,
};

describe("intent grade presentation", () => {
  it("shows high, medium, low, and insufficient without a close probability", () => {
    expect(gradeLabel("high")).toBe("高");
    expect(gradeLabel("medium")).toBe("中");
    expect(gradeLabel("low")).toBe("低");
    expect(gradeLabel("insufficient")).toBe("信息不足");

    const view = presentAssessment(high);
    expect(view.label).toBe("高");
    expect(view.reason).toContain("采购");
    expect(view.citations).toEqual(["请发合同和报价"]);
    expect(view.missing).toEqual(["买家"]);
    expect(view.freshness).toBe("2026-09-29T08:00:00Z");
    expect(view.version).toBe("rules-hui-1684-v1 / none");
    expect(view.disclaimer).toContain("不是真人成交预测");
    expect(view.suggestion).toContain("建议销售人工确认");
    expect(view.probability).toBeNull();
    expect(view.confidenceText).toBe("");
    expect(view.actions).toEqual(["修正分级", "标记误判"]);
    expect(outreachControls(view)).toEqual([]);
  });

  it("keeps a human lock on screen and never offers call, sms, group, or order", () => {
    const locked = presentAssessment({
      ...high,
      grade: "low",
      reason: "销售已驳回这次分级，AI 不覆盖已确认事实。",
      human_locked: true,
      stale: true,
      stale_reason: "new_message",
      suggestion: {
        kind: "review_only",
        label: "建议只作人工复核。",
        auto_call: true,
        auto_sms: true,
        auto_group: true,
        create_order: true,
      },
    });
    expect(locked.label).toBe("低");
    expect(locked.humanLocked).toBe(true);
    expect(locked.staleText).toContain("新消息");
    expect(locked.actions).toEqual(["修正分级", "标记误判"]);
    expect(outreachControls(locked)).toEqual([]);
    expect(JSON.stringify(locked)).not.toContain("91");
  });

  it("drops an uncalibrated percent and labels fixtures as fixtures", () => {
    const view = presentAssessment({ ...high, calibrated: true, confidence: 0.91 });
    expect(view.confidenceText).toBe("");
    expect(view.probability).toBeNull();
    expect(JSON.stringify(view)).not.toContain("%");
    expect(JSON.stringify(view)).not.toContain("91");
    expect(modelStatusText("recorded")).toBe("真实模型未完成");
    expect(modelStatusText("PASS")).toBe("真实模型未完成");
    expect(modelStatusText()).toBe("真实模型未完成");
    expect(originLabel("fixture")).toBe("夹具");
    expect(originLabel("human")).toBe("人工");
    expect(originLabel("rules")).toBe("规则");
    expect(salesCorrectionActions()).toEqual(["修正分级", "驳回"]);
  });

  it("corrects intent on the opportunity page without outreach controls", () => {
    const panel = readFileSync(new URL("../src/components/IntentOnOpportunity.tsx", import.meta.url), "utf8");
    const page = readFileSync(new URL("../src/app/(shell)/opportunities/[id]/page.tsx", import.meta.url), "utf8");
    const intent = readFileSync(new URL("../src/app/(shell)/intent/page.tsx", import.meta.url), "utf8");
    expect(page).toContain("IntentOnOpportunity");
    expect(page).toContain("probabilityText");
    expect(page).toContain("expectedCloseText");
    for (const banned of ["预计成交", "置信", "shadcn", "外呼", "发私信", "建订单"]) {
      expect(panel).not.toContain(banned);
    }
    expect(panel).toContain("salesCorrectionActions");
    expect(panel).toContain("modelStatusText");
    expect(panel).toContain("outreachButtons");
    expect(intent).toContain("modelStatusText()");
    expect(intent).toContain("originLabel");
    expect(intent).toContain("拒绝联系");
  });
});
