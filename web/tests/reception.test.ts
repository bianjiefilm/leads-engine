import { describe, expect, it } from "vitest";
import {
  approveCall,
  channelsDeliverable,
  closeCall,
  followUpCall,
  followUpSubmitAllowed,
  humanReplyCall,
  leadCall,
  leadPurposeAllowed,
  leadSubmitAllowed,
  newStaffToken,
  newVisitorKey,
  nextFollowUpRFC3339,
  releaseCall,
  replyApprovable,
  replySendable,
  sendCall,
  assistCall,
  takeoverCall,
} from "../src/lib/reception";

describe("reception presentation gate", () => {
  it("allows text and audio only for the current approved reply", () => {
    expect(channelsDeliverable(2, 2, "approved")).toEqual({ text: true, audio: true });
    expect(channelsDeliverable(1, 2, "approved")).toEqual({ text: false, audio: false });
    expect(channelsDeliverable(2, 2, "sent")).toEqual({ text: false, audio: false });
    expect(channelsDeliverable(2, 2, "superseded")).toEqual({ text: false, audio: false });
  });

  it("does not turn after-sales into a lead", () => {
    expect(leadPurposeAllowed("sales_followup", true)).toBe(true);
    expect(leadPurposeAllowed("after_sales", true)).toBe(false);
    expect(leadPurposeAllowed("general_qa", true)).toBe(false);
    expect(leadPurposeAllowed("sales_followup", false)).toBe(false);
  });

  it("mints a visitor key that is not a principal id", () => {
    const key = newVisitorKey();
    expect(key.startsWith("usr_")).toBe(false);
    expect(key.length).toBeGreaterThanOrEqual(16);
  });
});

describe("reception desk operations", () => {
  const sessionId = "rcs_abc123";

  it("sends takeover and release with the server epoch", () => {
    expect(takeoverCall(sessionId, 2)).toEqual({
      method: "POST",
      path: "/api/reception/sessions/rcs_abc123/takeover",
      body: { epoch: 2 },
    });
    expect(releaseCall(sessionId, 3)).toEqual({
      method: "POST",
      path: "/api/reception/sessions/rcs_abc123/release",
      body: { epoch: 3 },
    });
    expect(takeoverCall(sessionId, 0)).toBeNull();
    expect(releaseCall("", 1)).toBeNull();
    expect(assistCall(sessionId, 1)).toEqual({
      method: "POST",
      path: "/api/reception/sessions/rcs_abc123/assist",
      body: { epoch: 1 },
    });
    expect(assistCall(sessionId, 0)).toBeNull();
  });

  it("closes without a lead body", () => {
    expect(closeCall(sessionId)).toEqual({
      method: "POST",
      path: "/api/reception/sessions/rcs_abc123/close",
      body: {},
    });
  });

  it("refuses to send an older unsent reply", () => {
    expect(replySendable(2, 2, "approved")).toBe(true);
    expect(replySendable(1, 2, "approved")).toBe(false);
    expect(replySendable(2, 2, "generated")).toBe(false);
    expect(replySendable(2, 2, "sent")).toBe(false);
    expect(sendCall(sessionId, "rrp_1", "rcpt-1")).toEqual({
      method: "POST",
      path: "/api/reception/sessions/rcs_abc123/replies/rrp_1/send",
      body: { receipt_id: "rcpt-1" },
    });
    expect(sendCall(sessionId, "rrp_1", "有空格")).toBeNull();
  });

  it("approves only the current assist draft", () => {
    expect(replyApprovable("assist", 2, 2, "generated")).toBe(true);
    expect(replyApprovable("human", 2, 2, "generated")).toBe(false);
    expect(replyApprovable("assist", 1, 2, "generated")).toBe(false);
    expect(approveCall(sessionId, "rrp_1")?.path).toBe("/api/reception/sessions/rcs_abc123/replies/rrp_1/approve");
  });

  it("builds a human reply only when the text and token are present", () => {
    expect(humanReplyCall(sessionId, "reply-1", " 人工已接手 ")).toEqual({
      method: "POST",
      path: "/api/reception/sessions/rcs_abc123/replies",
      body: { client_reply_id: "reply-1", body: "人工已接手" },
    });
    expect(humanReplyCall(sessionId, "reply-1", "  ")).toBeNull();
    expect(newStaffToken("reply-")).toMatch(/^reply-[0-9a-f]{16}$/);
  });

  it("requires every authorization field before a lead", () => {
    const ready = {
      purpose: "sales_followup",
      allowContact: true,
      contactName: "访客甲",
      phone: "13800138000",
      noticeVersion: "reception-notice-v1",
    };
    expect(leadSubmitAllowed(ready)).toBe(true);
    expect(leadSubmitAllowed({ ...ready, purpose: "after_sales" })).toBe(false);
    expect(leadSubmitAllowed({ ...ready, purpose: "general_qa" })).toBe(false);
    expect(leadSubmitAllowed({ ...ready, allowContact: false })).toBe(false);
    expect(leadSubmitAllowed({ ...ready, contactName: " " })).toBe(false);
    expect(leadSubmitAllowed({ ...ready, phone: "" })).toBe(false);
    expect(leadSubmitAllowed({ ...ready, noticeVersion: "" })).toBe(false);
    expect(leadCall(sessionId, ready)?.body).toEqual({
      purpose: "sales_followup",
      allow_contact: true,
      contact_name: "访客甲",
      phone: "13800138000",
      notice_version: "reception-notice-v1",
      marketing_allowed: false,
    });
    expect(leadCall(sessionId, { ...ready, purpose: "after_sales" })).toBeNull();
  });

  it("follows up only after an authorized lead", () => {
    expect(followUpSubmitAllowed(undefined, "下午回访")).toBe(false);
    expect(followUpSubmitAllowed("", "下午回访")).toBe(false);
    expect(followUpSubmitAllowed("led_1", " ")).toBe(false);
    expect(followUpCall(sessionId, "led_1", "下午回访", "2026-09-29T07:00:00Z")).toEqual({
      method: "POST",
      path: "/api/reception/sessions/rcs_abc123/follow-up",
      body: { note: "下午回访", next_follow_up_at: "2026-09-29T07:00:00Z" },
    });
    expect(followUpCall(sessionId, "", "下午回访")).toBeNull();
    expect(nextFollowUpRFC3339("")).toBeUndefined();
    expect(nextFollowUpRFC3339("not-a-time")).toBeNull();
    expect(nextFollowUpRFC3339("2026-09-29T07:00:00Z")).toBe("2026-09-29T07:00:00Z");
  });
});
