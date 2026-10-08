import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// HUI-2626 finish-r1：opportunities 列表成品化——类别 tab 走 vendored SegmentedControl，
// 行内抽屉读真实 /api/opportunities/{id}；统计与漏斗口径不动。

describe("opportunities page finish shape", () => {
  const src = readFileSync("src/app/(shell)/opportunities/page.tsx", "utf8");

  it("replaces bare category tab buttons with the renderer SegmentedControl", () => {
    expect(src).toContain("SegmentedControl");
    expect(src).not.toContain("<button");
    expect(src).not.toContain('className="tab');
  });

  it("wires the shared detail drawer against the real opportunity API", () => {
    expect(src).toContain("DetailDrawer");
    expect(src).toContain("/api/opportunities/${");
  });

  it("keeps exactly one page primary control and the stats line", () => {
    expect(src.split('data-page-primary="true"').length - 1).toBe(1);
    expect(src).toContain("statsLine");
  });

  it("has no bare table element", () => {
    expect(src).not.toContain("<table");
  });
});
