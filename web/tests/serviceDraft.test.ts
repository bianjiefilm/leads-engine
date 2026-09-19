// 服务需求草稿交接(HUI-1749)纯函数测试:按钮显隐双侧断言(UI 侧)、
// 缺失标记(绝不猜测)、状态文案守卫(投递成功 ≠ 成交,无「已成交」)、
// 撤销/重试动作镜像服务端守卫。
import { describe, expect, it } from "vitest";
import {
  REVOKE_ACCEPTED_COPY,
  canCreateServiceDraft,
  handoffStatusLine,
  missingText,
  serviceDraftActions,
  type ServiceDraftHandoff,
} from "../src/lib/serviceDraft";

const owner = { role: "owner", memberId: "m-owner", enabled: true };
const salesAssignee = { role: "sales", memberId: "m-sales", enabled: true };
const salesOther = { role: "sales", memberId: "m-other", enabled: true };
const disabled = { role: "sales", memberId: "m-sales", enabled: false };

describe("button visibility (UI side of the double-sided assertion)", () => {
  const creative = { business_category: "creative_service", assigned_member_id: "m-sales" };
  const merchant = { business_category: "merchant_customer", assigned_member_id: "m-sales" };

  it("visible for creative_service when permitted (owner or assignee)", () => {
    expect(canCreateServiceDraft(owner, creative)).toBe(true);
    expect(canCreateServiceDraft(salesAssignee, creative)).toBe(true);
  });

  it("NEVER visible for merchant_customer — action does not exist", () => {
    expect(canCreateServiceDraft(owner, merchant)).toBe(false);
    expect(canCreateServiceDraft(salesAssignee, merchant)).toBe(false);
  });

  it("hidden for non-assignee sales and disabled accounts", () => {
    expect(canCreateServiceDraft(salesOther, creative)).toBe(false);
    expect(canCreateServiceDraft(disabled, creative)).toBe(false);
    expect(canCreateServiceDraft({} as never, creative)).toBe(false);
  });
});

describe("missing marks (never guessed, never filled)", () => {
  it("lists every absent field with a label", () => {
    const line = missingText(["budget_cents", "deadline", "assets"]);
    expect(line).toContain("缺失");
    expect(line).toContain("预算");
    expect(line).toContain("截止日期");
    expect(line).toContain("品牌/素材资产");
  });

  it("says none when everything was confirmed", () => {
    expect(missingText([])).toBe("无缺失");
  });
});

describe("status wording guard (delivery success is not a deal)", () => {
  const base: ServiceDraftHandoff = {
    handoff_id: "handoff-aa",
    source_version: 1,
    local_status: "delivered",
    draft_ref: "demand:1",
    target_app: "orders",
    target_status: "draft",
    target_dirty: false,
    target_revoked: false,
    confirmed_at: "2026-09-19T00:00:00Z",
    delivered_at: "2026-09-19T00:00:01Z",
    revoked_at: null,
    note: "",
  };

  const lines = [
    handoffStatusLine(base),
    handoffStatusLine({ ...base, local_status: "delivery_failed", draft_ref: "" }),
    handoffStatusLine({ ...base, local_status: "revoked" }),
    handoffStatusLine({ ...base, local_status: "confirmed", draft_ref: "" }),
    REVOKE_ACCEPTED_COPY,
  ];

  it("delivered line states the projection is not a deal fact", () => {
    expect(handoffStatusLine(base)).toContain("不代表成交");
    expect(handoffStatusLine(base)).toContain("商机状态以 CRM 为准");
  });

  it("no user-facing status copy contains 已成交", () => {
    for (const s of lines) {
      expect(s).not.toContain("已成交");
    }
  });

  it("failed delivery keeps the snapshot message (facts not lost)", () => {
    expect(handoffStatusLine({ ...base, local_status: "delivery_failed" })).toContain("可重试");
  });
});

describe("action matrix mirrors the server guards", () => {
  const base: ServiceDraftHandoff = {
    handoff_id: "handoff-aa",
    source_version: 1,
    local_status: "delivered",
    draft_ref: "demand:1",
    target_app: "orders",
    target_status: "draft",
    target_dirty: false,
    target_revoked: false,
    confirmed_at: "t",
    delivered_at: null,
    revoked_at: null,
    note: "",
  };

  it("target draft: retry + revoke + refresh available", () => {
    const a = serviceDraftActions(base);
    expect(a.canRetry).toBe(true);
    expect(a.canRevoke).toBe(true);
    expect(a.canRefresh).toBe(true);
  });

  it("target accepted: revoke disappears (change via the target flow)", () => {
    const a = serviceDraftActions({ ...base, target_status: "accepted" });
    expect(a.canRevoke).toBe(false);
    expect(a.canRetry).toBe(true);
  });

  it("revoked: everything disappears; a fresh confirmation starts a new version", () => {
    const a = serviceDraftActions({ ...base, local_status: "revoked" });
    expect(a).toEqual({ canRetry: false, canRevoke: false, canRefresh: false });
  });

  it("never-received snapshot (delivery_failed) is still revocable", () => {
    const a = serviceDraftActions({ ...base, local_status: "delivery_failed", target_status: "" });
    expect(a.canRevoke).toBe(true);
  });
});
