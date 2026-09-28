import { describe, expect, it } from "vitest";
import { presentSOP, recordLabel, unattendedOpen } from "../src/lib/sopReach";

describe("sop reach display", () => {
  it("keeps unattended closed when only a balance or a score is present", () => {
    expect(unattendedOpen({ level: "unattended", explicit_unattended: false, balance_cents: 50000, ai_score: 99 })).toBe(false);
    expect(unattendedOpen({ level: "remind_draft_confirm", explicit_unattended: false })).toBe(false);
    expect(unattendedOpen({ level: "unattended", explicit_unattended: true })).toBe(true);
  });

  it("does not treat a claimed live channel as delivered", () => {
    const view = presentSOP({
      verification: "verified",
      live_channels: ["sms", "wecom"],
      unattended: true,
      explicit_unattended: false,
      level: "unattended",
    });
    expect(view.verified).toBe(false);
    expect(view.liveChannels).toEqual([]);
    expect(view.unattended).toBe(false);
    expect(view.note).toContain("未送达");
  });

  it("labels reminder, draft, submission, delivery, and reply separately", () => {
    expect(recordLabel("internal_reminder", "recorded")).toBe("内部提醒");
    expect(recordLabel("pending_draft", "recorded")).toBe("待发送草稿");
    expect(recordLabel("channel_submission", "pending_send")).toBe("待发送");
    expect(recordLabel("channel_delivery", "undelivered")).toBe("未送达");
    expect(recordLabel("channel_delivery", "delivered")).toBe("未送达");
    expect(recordLabel("user_reply", "recorded")).toBe("用户回复");
  });
});
