import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { WHITE_LABEL_CHAIN_UNVERIFIED, brandIsDisplayOnly, exportConfirmHeader, sourceStaysOnTenant } from "../src/lib/crmIsolation";

describe("crm tenant isolation copy", () => {
  it("treats a brand name as display, not as the tenant", () => {
    const text = brandIsDisplayOnly("白标品牌");
    expect(text).toContain("白标品牌");
    expect(text).toContain("不能代替租户");
    expect(text).not.toContain("tenant_id");
  });

  it("asks for a second authorization header before download", () => {
    expect(exportConfirmHeader()).toEqual({ "x-export-confirm": "1" });
  });

  it("keeps source context on the current tenant and does not claim the chain", () => {
    const line = sourceStaysOnTenant("门店活动", "白标品牌", "camp_local");
    expect(line).toContain("门店活动");
    expect(line).toContain("白标品牌");
    expect(line).toContain("camp_local");
    expect(line).toContain("不能改归属");
    expect(WHITE_LABEL_CHAIN_UNVERIFIED).toContain("仍未验证");
    const page = readFileSync("src/app/(shell)/isolation/page.tsx", "utf8");
    const home = readFileSync("src/app/(shell)/page.tsx", "utf8");
    const desk = readFileSync("src/lib/workbench.ts", "utf8");
    for (const banned of ["白标经营链完成", "白标经营链已完成", "自动触达已成功", "销售已收到"]) {
      expect(page).not.toContain(banned);
      expect(home).not.toContain(banned);
    }
    expect(page).toContain("sourceStaysOnTenant");
    expect(page).toContain("WHITE_LABEL_CHAIN_UNVERIFIED");
    expect(page).toContain("租户暂停和品牌暂停分开");
    expect(home).toContain("jointChainLine");
    expect(home).toContain('data-joint-chain="incomplete"');
    expect(home).toContain('href="/isolation"');
    expect(desk).toContain("联合经营链未完成");
    expect(desk).toContain("自动触达已成功");
  });
});
