import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// HUI-2626 finish-r1 Task 7：正式路由红线清零——裸 table（enterprises/intent）
// 与其余工作页的裸 button 全部替换；字段语义与能力零改动。

const NO_BARE_BUTTON = [
  "src/app/(shell)/sop/page.tsx",
  "src/app/(shell)/outbound/page.tsx",
  "src/app/(shell)/channel-interactions/page.tsx",
  "src/app/(shell)/isolation/page.tsx",
  "src/app/(shell)/attribution/page.tsx",
  "src/app/(shell)/subscription/page.tsx",
  "src/app/(shell)/intent/page.tsx",
  "src/app/(shell)/enterprises/page.tsx",
];

describe("formal routes clear bare controls", () => {
  for (const page of NO_BARE_BUTTON) {
    it(`${page} has no bare <button> and no bare <table>`, () => {
      const src = readFileSync(page, "utf8");
      expect(src).not.toContain("<button");
      expect(src).not.toContain("<table");
    });
  }

  it("sample report and enterprise directory become record lists", () => {
    expect(readFileSync("src/app/(shell)/intent/page.tsx", "utf8")).toContain("RecordList");
    expect(readFileSync("src/app/(shell)/enterprises/page.tsx", "utf8")).toContain("RecordList");
  });

  it("keeps exactly one page primary on the pages that declare one", () => {
    for (const page of [
      "src/app/(shell)/sop/page.tsx",
      "src/app/(shell)/outbound/page.tsx",
      "src/app/(shell)/isolation/page.tsx",
      "src/app/(shell)/attribution/page.tsx",
      "src/app/(shell)/intent/page.tsx",
    ]) {
      const src = readFileSync(page, "utf8");
      expect(src.split('data-page-primary="true"').length - 1).toBe(1);
    }
  });

  it("enterprise preview confirm/refuse/delete keep their ids for the flow", () => {
    const src = readFileSync("src/app/(shell)/enterprises/page.tsx", "utf8");
    for (const id of ["import-submit", "filter-submit", "confirm-submit", "refuse-submit", "delete-submit", "preview", "confirm-result"]) {
      expect(src).toContain(id);
    }
  });
});
