import { describe, expect, it } from "vitest";
import { leadHref, marketingLabels, presentCapability, receiptLine, rowActions } from "../src/lib/channelInteraction";

const secret = "客户私信全文不应进地址";

describe("authorized channel interactions", () => {
  it("stays unverified even when a payload names a live platform", () => {
    const view = presentCapability({ verification: "verified", live_providers: ["douyin", "wechat"] });
    expect(view.verified).toBe(false);
    expect(view.label).toBe("未验证");
    expect(view.providers).toEqual([]);
    expect(view.note).toContain("没有真实渠道凭证");
  });

  it("links by id and does not offer outreach for a high score or a missing phone", () => {
    expect(leadHref("lead_1")).toBe("/leads/lead_1");
    expect(leadHref("lead_1")).not.toContain(secret);
    const actions = rowActions({
      candidate_id: "clc_1",
      candidate_status: "open",
      score: 99,
      text: secret,
      phone: "",
      phone_marketing: true,
      sms_marketing: true,
      auto_reach: true,
    });
    expect(actions.confirm).toBe(true);
    expect(actions.reach).toEqual([]);
    expect(actions.href).toBe("/channel-interactions");
    expect(actions.href).not.toContain(secret);
    expect(marketingLabels({ phone: "", phone_marketing: true, sms_marketing: true })).toEqual([]);
    expect(receiptLine({ score: 99, receipt_platform: true, receipt_persisted: true, receipt_human: false })).toBe(
      "平台事件已接收 · 获客已落库",
    );
  });
});
