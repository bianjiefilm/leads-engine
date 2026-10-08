import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// HUI-2626 finish-r1 Task 6：Today/Next 与详情路由页的裸 button 全部换成
// vendored renderer Button（红线：正式路由无裸 button）。业务行为零改动。

const PAGES = [
  "src/app/(shell)/page.tsx",
  "src/app/(shell)/leads/[id]/page.tsx",
  "src/app/(shell)/contacts/[id]/page.tsx",
  "src/app/(shell)/opportunities/[id]/page.tsx",
];

describe("today + detail routes use renderer buttons", () => {
  for (const page of PAGES) {
    it(`${page} has no bare <button>`, () => {
      const src = readFileSync(page, "utf8");
      expect(src).not.toContain("<button");
      expect(src).toContain("Button");
    });
  }

  it("today keeps one page primary and the joint-chain copy contract", () => {
    const src = readFileSync("src/app/(shell)/page.tsx", "utf8");
    expect(src.split('data-page-primary="true"').length - 1).toBe(1);
    expect(src).toContain("jointChainLine");
    expect(src).toContain('data-joint-chain="incomplete"');
    expect(src).toContain('href="/isolation"');
  });

  it("detail routes keep their single page primary markers", () => {
    expect(readFileSync("src/app/(shell)/leads/[id]/page.tsx", "utf8").split('data-page-primary="true"').length - 1).toBe(1);
    expect(readFileSync("src/app/(shell)/contacts/[id]/page.tsx", "utf8").split('data-page-primary="true"').length - 1).toBe(1);
    expect(readFileSync("src/app/(shell)/opportunities/[id]/page.tsx", "utf8").split('data-page-primary="true"').length - 1).toBe(1);
  });

  it("danger actions use the danger variant (stop-marketing, delete profile, revoke handoff)", () => {
    expect(readFileSync("src/app/(shell)/contacts/[id]/page.tsx", "utf8")).toContain('variant="danger"');
    expect(readFileSync("src/app/(shell)/opportunities/[id]/page.tsx", "utf8")).toContain('variant="danger"');
  });
});
