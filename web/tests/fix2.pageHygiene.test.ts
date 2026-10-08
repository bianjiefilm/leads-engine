import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// HUI-2626 fix2（gate-r2 回流修复）页面卫生守卫。
// 口径对齐 2625 detector（public-ai internal/uifinish，只读复扫）：
// 六条受检路由的页面源文件不得出现手填租户/内部 id 子串、裸 JSON 序列化/
// 裸后端错误串、原生控件与 table/form/button 起始标签；且必须静态声明
// loading/empty/error 三态（data-state），error 分支带下一步动作。

const APP = join(__dirname, "..", "src", "app");

// 路由表与 2625 gate-r2 rescan 的六条受检路由一致（/ 出现两次，去重后五个源文件）。
const ROUTE_SOURCES: Array<{ route: string; file: string }> = [
  { route: "/", file: join(APP, "(shell)", "page.tsx") },
  { route: "/leads", file: join(APP, "(shell)", "leads", "page.tsx") },
  { route: "/leads/[id]", file: join(APP, "(shell)", "leads", "[id]", "page.tsx") },
  { route: "/reception", file: join(APP, "reception", "page.tsx") },
  { route: "/opportunities/[id]", file: join(APP, "(shell)", "opportunities", "[id]", "page.tsx") },
];

// 与 detector idRes/rawErrorRes 同口径。
const FORBIDDEN_SUBSTRINGS: Array<[string, RegExp]> = [
  ["手填 tenant id", /tenant[_ -]?id/i],
  ["手填 internal id", /internal[_ -]?id/i],
  ["裸租户 ID 文案", /租户\s*ID/],
  ["裸内部 ID 文案", /内部\s*ID/],
  ["JSON.stringify", /JSON\.stringify/],
  ["statusText", /\bstatusText\b/],
  ["裸 HTTP 错误串", /\binternal server error\b|\bbad gateway\b|\bbad request\b|http\/1\.[01]/i],
];

const NATIVE_TAG = /<(input|select|textarea|button|form|table)[\s>]/;

function sourceOf(file: string): string {
  return readFileSync(file, "utf8");
}

describe("fix2 页面卫生守卫（六条受检路由）", () => {
  for (const { route, file } of ROUTE_SOURCES) {
    it(`${route} 不含手填租户/内部 id 子串与裸 JSON/HTTP 错误串`, () => {
      const src = sourceOf(file);
      for (const [label, pattern] of FORBIDDEN_SUBSTRINGS) {
        const hit = pattern.exec(src);
        expect(hit === null, `${label} 命中于 ${hit?.[0] ?? ""}`).toBe(true);
      }
    });

    it(`${route} 不含原生控件与 table/form/button 起始标签`, () => {
      const src = sourceOf(file);
      const hit = NATIVE_TAG.exec(src);
      expect(hit === null, `原生标签命中于 ${hit?.[0] ?? ""}`).toBe(true);
    });

    it(`${route} 静态声明 loading/empty/error 三态`, () => {
      const src = sourceOf(file);
      expect(src).toMatch(/data-state="loading"/);
      expect(src).toMatch(/data-state="empty"/);
      expect(src).toMatch(/data-state="error"/);
    });
  }

  it("错误态分支携带下一步动作（error data-state 块内含 action 传参）", () => {
    for (const { route, file } of ROUTE_SOURCES) {
      const src = sourceOf(file);
      const errorBlock = /data-state="error"[\s\S]{0,1200}?<\/div>|<main[^>]*data-state="error"[^>]*>/m.exec(src);
      expect(errorBlock, `${route} 缺 error 三态块`).toBeTruthy();
      const scope = src.slice(Math.max(0, (errorBlock?.index ?? 0) - 200), (errorBlock?.index ?? 0) + 1200);
      expect(/action=/.test(scope), `${route} error 块附近无 action 下一步动作`).toBe(true);
    }
  });
});
