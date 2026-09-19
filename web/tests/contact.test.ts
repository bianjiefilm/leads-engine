// 客户档案域纯函数测试(HUI-1691 / FEAT-0192):consent 撤销持久语义、
// 权限显隐镜像、手机号搜索守卫(镜像服务端 400)、标签解析与文案守卫
// (无「自动注册/自动开户」措辞,撤销不可恢复)。
import { describe, expect, it } from "vitest";
import {
  COARSE_CONSENT_LABELS,
  canDeleteContact,
  canEditContact,
  canExportContacts,
  consentStatusLabel,
  isPhoneLike,
  marketingSummaryLine,
  parseTags,
  searchGuard,
  type ConsentRow,
} from "../src/lib/contact";

// ---- consent 撤销持久语义 -------------------------------------------------------

describe("consent status wording", () => {
  const base: ConsentRow = {
    id: "cns_1",
    source_submission_ref: "sub-1",
    source_channel: "landing_page",
    notice_version: "v1",
    purpose: "marketing",
    marketing_allowed: true,
    status: "active",
  };

  it("revoked rows read as irreversible, never as recoverable", () => {
    const label = consentStatusLabel({ ...base, status: "revoked", revoked_at: "2026-09-19T00:00:00Z" });
    expect(label).toContain("已撤销");
    expect(label).toContain("不可恢复");
    expect(label).not.toContain("恢复授权");
  });

  it("active rows distinguish marketing allowed vs not", () => {
    expect(consentStatusLabel(base)).toBe("允许营销");
    expect(consentStatusLabel({ ...base, marketing_allowed: false })).toBe("不允许营销");
  });

  it("revoked wins even if the historical flag says allowed", () => {
    const label = consentStatusLabel({ ...base, status: "revoked" });
    expect(label).toContain("不可恢复");
  });

  it("summary line keeps the three numbers apart", () => {
    const line = marketingSummaryLine({ total: 3, marketing_active: 1, revoked: 2 });
    expect(line).toContain("3 个来源授权");
    expect(line).toContain("允许营销 1 个");
    expect(line).toContain("已撤销 2 个");
  });

  it("no wording implies phone-based platform account registration", () => {
    const texts = [...Object.values(COARSE_CONSENT_LABELS), marketingSummaryLine({ total: 1, marketing_active: 0, revoked: 0 })];
    for (const t of texts) {
      expect(t).not.toContain("自动注册");
      expect(t).not.toContain("自动开户");
    }
  });
});

// ---- 权限显隐镜像(服务端仍逐动作重新鉴权) -------------------------------------------

describe("action visibility mirrors the server matrix", () => {
  const own = { assigned_member_id: "mem_sales_a1" };
  const others = { assigned_member_id: "mem_sales_a2" };

  it("edit: owner anywhere, assignee only, disabled never", () => {
    expect(canEditContact({ role: "owner", memberId: "mem_owner", enabled: true }, others)).toBe(true);
    expect(canEditContact({ role: "sales", memberId: "mem_sales_a1", enabled: true }, own)).toBe(true);
    expect(canEditContact({ role: "sales", memberId: "mem_sales_a2", enabled: true }, own)).toBe(false);
    expect(canEditContact({ role: "agent", memberId: "mem_sales_a2", enabled: true }, own)).toBe(false);
    expect(canEditContact({ role: "owner", memberId: "mem_owner", enabled: false }, own)).toBe(false);
  });

  it("export and delete are owner-only", () => {
    expect(canExportContacts({ role: "owner", enabled: true })).toBe(true);
    expect(canExportContacts({ role: "sales", memberId: "m", enabled: true })).toBe(false);
    expect(canExportContacts({ role: "agent", memberId: "m", enabled: true })).toBe(false);
    expect(canDeleteContact({ role: "owner", enabled: false })).toBe(false);
    expect(canDeleteContact({ role: "sales", enabled: true })).toBe(false);
  });
});

// ---- 手机号搜索守卫(镜像服务端 URL 纪律) -------------------------------------------

describe("search guard blocks phone-shaped input", () => {
  it("isPhoneLike mirrors the server rule (7+ consecutive digits)", () => {
    expect(isPhoneLike("13812345678")).toBe(true);
    expect(isPhoneLike("电话1234567")).toBe(true);
    expect(isPhoneLike("vip-12")).toBe(false);
    expect(isPhoneLike("重点客户")).toBe(false);
    expect(isPhoneLike("123456")).toBe(false); // 6 位不算
  });

  it("searchGuard refuses phone numbers in name/tag, passes normal input", () => {
    expect(searchGuard("13812345678", "")).toContain("手机号");
    expect(searchGuard("", "1234567")).toContain("手机号");
    expect(searchGuard("张三", "vip")).toBe("");
  });
});

// ---- 标签解析 -----------------------------------------------------------------

describe("tag parsing", () => {
  it("splits the normalized comma-joined string", () => {
    expect(parseTags("vip,重点,华东")).toEqual(["vip", "重点", "华东"]);
    expect(parseTags("")).toEqual([]);
    expect(parseTags(null)).toEqual([]);
    expect(parseTags("单标签")).toEqual(["单标签"]);
  });
});
