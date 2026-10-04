import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import {
  campaignChoice,
  consentChoices,
  contactChoices,
  outboundSubmit,
  sopSubmit,
} from "../src/lib/scopePick";

const contact = { id: "ct-1", name: "李想", campaign_id: "cmp-west" };
const consent = {
  id: "cns-1",
  purpose: "marketing",
  source_channel: "voice",
  marketing_allowed: true,
  revoked_at: null,
};

describe("scope pick", () => {
  it("keeps only contacts that have both an id and a name", () => {
    expect(contactChoices([
      contact,
      { id: "", name: "空号" },
      { id: "ct-2", name: "" },
      { id: "ct-3" },
      { name: "没有号" },
      { id: "  ", name: "空白" },
      null,
      "ct-4",
    ])).toEqual([{ id: "ct-1", name: "李想", campaign_id: "cmp-west" }]);
    expect(contactChoices(null)).toEqual([]);
  });

  it("keeps only consents that are still allowed to market", () => {
    expect(consentChoices([
      consent,
      { id: "", purpose: "marketing", marketing_allowed: true, revoked_at: null },
      { id: "cns-off", purpose: "marketing", marketing_allowed: false, revoked_at: null },
      { id: "cns-revoked", purpose: "marketing", marketing_allowed: true, revoked_at: "2026-10-01T00:00:00Z" },
      { id: "cns-blank", purpose: "marketing", marketing_allowed: true, revoked_at: "  " },
      { consent_status: "granted", marketing_allowed: true },
    ]).map((item) => item.id)).toEqual(["cns-1", "cns-blank"]);
  });

  it("does not submit when there is no contact", () => {
    expect(sopSubmit({
      contact: null,
      purpose: "follow_up",
      channel: "sms",
      recipient: "13800001111",
      body: "内部提醒",
    })).toBeNull();
    expect(sopSubmit({
      contact: { id: "", name: "李想" },
      purpose: "follow_up",
      channel: "sms",
      recipient: "13800001111",
      body: "内部提醒",
    })).toBeNull();
    expect(outboundSubmit({ contact: null, newKey: () => "task-1" })).toBeNull();
    expect(outboundSubmit({ contact: { id: "ct-1", name: "" }, newKey: () => "task-1" })).toBeNull();
  });

  it("does not submit marketing without a real consent, and never sends an empty consent id", () => {
    const missing = sopSubmit({
      contact,
      purpose: "marketing",
      consent: null,
      channel: "sms",
      recipient: "13800001111",
      body: "营销草稿",
    });
    const blank = sopSubmit({
      contact,
      purpose: "marketing",
      consent: { id: "", marketing_allowed: true, revoked_at: null },
      channel: "email",
      recipient: "a@example.com",
      body: "营销草稿",
    });
    const statusOnly = sopSubmit({
      contact: { ...contact, consent_status: "granted" },
      purpose: "marketing",
      consent: { id: "granted", marketing_allowed: false },
      channel: "wecom",
      recipient: "wecom-user",
      body: "营销草稿",
    });
    expect(missing).toBeNull();
    expect(blank).toBeNull();
    expect(statusOnly).toBeNull();
    for (const payload of [missing, blank, statusOnly]) {
      expect(payload).not.toEqual(expect.objectContaining({ consent_id: "" }));
      expect(JSON.stringify(payload ?? {})).not.toContain("consent_id");
    }
  });

  it("omits consent_id for follow-up even when a consent is selected", () => {
    const payload = sopSubmit({
      contact: { ...contact, consent_status: "granted" },
      purpose: "follow_up",
      consent,
      channel: "sms",
      recipient: "13800001111",
      body: "下次再联系",
    });
    expect(payload).toEqual({
      contact_id: "ct-1",
      channel: "sms",
      recipient: "13800001111",
      purpose: "follow_up",
      content_version: 1,
      budget_cents: 0,
      body: "下次再联系",
    });
    expect(payload).not.toHaveProperty("consent_id");
  });

  it("includes the selected consent id for marketing", () => {
    expect(sopSubmit({
      contact,
      purpose: "marketing",
      consent,
      channel: "sms",
      recipient: "13800001111",
      body: "营销草稿",
    })).toEqual({
      contact_id: "ct-1",
      channel: "sms",
      recipient: "13800001111",
      purpose: "marketing",
      content_version: 1,
      budget_cents: 0,
      body: "营销草稿",
      consent_id: "cns-1",
    });
  });

  it("does not dial without a campaign id", () => {
    const newKey = vi.fn(() => "task-should-not-run");
    expect(campaignChoice({ id: "ct-1", name: "李想" })).toBeNull();
    expect(campaignChoice({ campaign_id: "  " })).toBeNull();
    expect(outboundSubmit({
      contact: { id: "ct-1", name: "李想" },
      consent,
      newKey,
    })).toBeNull();
    expect(newKey).not.toHaveBeenCalled();
  });

  it("uses the contact campaign id and a caller-supplied task key", () => {
    const payload = outboundSubmit({
      contact,
      newKey: () => "task-fixed",
    });
    expect(payload?.campaign_id).toBe("cmp-west");
    expect(payload?.campaign_id).not.toBe("camp-a");
    expect(payload).toEqual({
      task_key: "task-fixed",
      contact_id: "ct-1",
      campaign_id: "cmp-west",
      mode: "isolation",
      budget_cents: 0,
      op: "dial",
    });
    expect(payload).not.toHaveProperty("consent_id");
  });

  it("adds consent_id only when the selected consent is still allowed", () => {
    const allowed = outboundSubmit({
      contact,
      consent,
      newKey: () => "task-fixed",
    });
    const revoked = outboundSubmit({
      contact,
      consent: { ...consent, revoked_at: "2026-10-01" },
      newKey: () => "task-fixed",
    });
    expect(allowed?.consent_id).toBe("cns-1");
    expect(revoked).not.toHaveProperty("consent_id");
    expect(outboundSubmit({ contact, newKey: () => "  " })).toBeNull();
  });

  it("does not keep draft-id or camp-a inputs in the sop or outbound pages", () => {
    const sop = readFileSync(new URL("../src/app/(shell)/sop/page.tsx", import.meta.url), "utf8");
    const outbound = readFileSync(new URL("../src/app/(shell)/outbound/page.tsx", import.meta.url), "utf8");
    expect(sop).not.toContain("草稿 id");
    expect(sop).not.toContain("camp-a");
    expect(outbound).not.toContain("草稿 id");
    expect(outbound).not.toContain("camp-a");
    expect(sop).toContain("还没有可选的客户");
    expect(sop).toContain("还没有可选择的授权");
    expect(sop).toContain("不会自动发短信、邮件或企微，也不会扣费");
    expect(sop).not.toContain("请输入租户");
    expect(outbound).toContain("还没有可选的客户");
    expect(outbound).toContain("还没有可选的活动");
    expect(outbound).toContain("还没有可选择的授权");
    expect(outbound).toContain("不会自动发送");
    expect(outbound).toContain("crypto.randomUUID");
    expect(outbound).not.toContain("请输入租户");
  });

  it("only reads the today home source and still finds the today-next marker", () => {
    const today = readFileSync(new URL("../src/app/(shell)/page.tsx", import.meta.url), "utf8");
    expect(today).toContain("data-today-home");
    expect(today).toContain("today-next");
    expect(today).toContain('data-today-home={HOME_SURFACE}');
  });
});
