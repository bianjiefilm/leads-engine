import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { failureText } from "@/lib/productShell";
import { receptionDeskLoad } from "@/app/reception/desk-load";

const CLOSED = "接待功能没有打开，现在不能载入会话。";
const UNAVAILABLE = "服务暂时没有响应，请稍后再试。";
const LEAK = /parse|JSON|Unexpected|page not found/i;

describe("reception desk load when the route is not JSON", () => {
  it("shows the feature-closed sentence for a plain-text 404", () => {
    const raw = "404 page not found";
    let parser = "";
    try {
      JSON.parse(raw);
    } catch (e) {
      parser = (e as Error).message;
    }
    expect(parser).toMatch(/Unexpected/);

    const loaded = receptionDeskLoad(404, raw);
    expect(loaded).toEqual({ ok: false, message: CLOSED });
    if (!loaded.ok) {
      expect(loaded.message).not.toMatch(LEAK);
      expect(loaded.message).not.toContain(parser);
    }
    const withNewline = receptionDeskLoad(404, "404 page not found\n");
    expect(withNewline).toEqual({ ok: false, message: CLOSED });
  });

  it("uses the existing unavailable sentence for other non-JSON failures", () => {
    for (const [status, raw] of [
      [500, "Internal Server Error"],
      [502, ""],
      [403, "Forbidden"],
      [200, "not-json"],
    ] as const) {
      const loaded = receptionDeskLoad(status, raw);
      expect(loaded).toEqual({ ok: false, message: UNAVAILABLE });
      if (!loaded.ok) expect(loaded.message).not.toMatch(LEAK);
    }
  });

  it("keeps JSON failures on failureText and leaves a JSON desk body intact", () => {
    const chinese = { message: "活动还没有门店，不能提交。" };
    expect(receptionDeskLoad(400, JSON.stringify(chinese))).toEqual({
      ok: false,
      message: failureText(chinese, 400),
    });

    const missing = { error: "record not found" };
    expect(receptionDeskLoad(404, JSON.stringify(missing))).toEqual({
      ok: false,
      message: failureText(missing, 404),
    });
    expect(failureText(missing, 404)).toBe("这条记录不存在，或不在当前工作范围。");

    const down = { message: "internal server error" };
    expect(receptionDeskLoad(500, JSON.stringify(down))).toEqual({
      ok: false,
      message: failureText(down, 500),
    });

    const desk = { items: [{ session_id: "rcs_1" }] };
    expect(receptionDeskLoad(200, JSON.stringify(desk))).toEqual({ ok: true, body: desk });
  });

  it("loads the desk page through receptionDeskLoad instead of res.json()", () => {
    const page = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src/app/reception/page.tsx"), "utf8");
    expect(page).toContain("receptionDeskLoad");
    expect(page).not.toContain("res.json()");
    expect(page).toContain("acceptDeskPayload");
    expect(page).toContain('title="接待没有载入"');
  });
});
