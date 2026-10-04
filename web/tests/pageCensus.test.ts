import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { HALF_PRODUCT_MARKERS, pageCensus, scanHalfProduct, surfaceFor, SURFACES } from "@/lib/pageCensus";
import type { HalfProductHit } from "@/lib/pageCensus";
import { routeLedger } from "@/lib/pageMap";

const webDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const appDir = join(webDir, "src/app");
const pageFile = /^page\.(tsx|ts|jsx|js)$/;

function routeFromPageFile(relativeFile: string): string {
  const parts = relativeFile.split("/").filter((part) => part.length > 0);
  parts.pop();
  const visible = parts.filter((part) => !(part.startsWith("(") && part.endsWith(")")));
  return visible.length === 0 ? "/" : `/${visible.join("/")}`;
}

function discoverPageFiles(dir = appDir, relative = ""): Map<string, string> {
  const found = new Map<string, string>();
  for (const name of readdirSync(dir)) {
    if (name.startsWith("_") || name.startsWith("@")) continue;
    const abs = join(dir, name);
    const next = relative ? `${relative}/${name}` : name;
    if (statSync(abs).isDirectory()) {
      for (const [route, file] of discoverPageFiles(abs, next)) {
        if (found.has(route)) throw new Error(`duplicate page file for route ${route}`);
        found.set(route, file);
      }
      continue;
    }
    if (!pageFile.test(name)) continue;
    const route = routeFromPageFile(next);
    if (found.has(route)) throw new Error(`duplicate page file for route ${route}`);
    found.set(route, `src/app/${next}`);
  }
  return found;
}

function position(source: string, index: number): { line: number; column: number } {
  const prior = source.slice(0, index);
  return { line: prior.split("\n").length, column: index - prior.lastIndexOf("\n") };
}

// 与普查分开写的一次重扫。命中顺序按源码位置。
function rescan(source: string): HalfProductHit[] {
  const rules: { marker: HalfProductHit["marker"]; re: RegExp }[] = [
    { marker: "TODO", re: /(?<![A-Za-z0-9_])TODO(?![A-Za-z0-9_])/g },
    { marker: "FIXME", re: /(?<![A-Za-z0-9_])FIXME(?![A-Za-z0-9_])/g },
    { marker: "inline style", re: /style\s*=\s*(?:\{\{|["'{])/g },
    { marker: "接口说明", re: /接口说明/g },
    { marker: "架构说明", re: /架构说明/g },
    // 大小写敏感。<Button 不算。# 后恰好 3 或 6 位十六进制才算，多一位或少一位都不计。
    { marker: "raw button", re: /(?<![A-Za-z0-9_])<button/g },
    { marker: "raw input", re: /(?<![A-Za-z0-9_])<input/g },
    { marker: "raw table", re: /(?<![A-Za-z0-9_])<table/g },
    { marker: "bare hex", re: /(?<![A-Za-z0-9_])#(?:[0-9A-Fa-f]{6}|[0-9A-Fa-f]{3})(?![0-9A-Fa-f])/g },
  ];
  const found: { index: number; hit: HalfProductHit }[] = [];
  for (const rule of rules) {
    for (const match of source.matchAll(rule.re)) {
      const index = match.index ?? 0;
      found.push({ index, hit: { marker: rule.marker, ...position(source, index) } });
    }
  }
  found.sort((a, b) => a.index - b.index);
  return found.map((item) => item.hit);
}

function expectedSurface(path: string, id: string): string {
  if (path.includes("/admin")) return "admin";
  if (id === "public_consumer") return "public";
  if (id === "editor_shell") return "editor";
  if (id === "portal" || id === "login") return "portal";
  return "work";
}

describe("page census", () => {
  it("records exactly one row for each ledger route", () => {
    const book = routeLedger();
    const rows = pageCensus();
    const paths = rows.map((row) => row.path);

    expect(paths).toEqual(book.map((entry) => entry.path));
    expect(new Set(paths).size).toBe(paths.length);
    expect(rows).toHaveLength(book.length);

    const files = discoverPageFiles();
    expect([...files.keys()].sort()).toEqual([...paths].sort());
    for (const row of rows) {
      const entry = book.find((item) => item.path === row.path);
      expect(entry).toBeDefined();
      expect(row.id).toBe(entry?.id);
      expect(row.page_file).toBe(files.get(row.path));
      expect(row.page_file.endsWith("/page.tsx") || row.page_file.endsWith("/page.ts")).toBe(true);
    }
    expect(paths).not.toContain("/api/[...path]");

    rows[0].path = "/mutated";
    rows[0].half_product.push({ marker: "TODO", line: 1, column: 1 });
    expect(pageCensus()[0].path).toBe(book[0].path);
    expect(pageCensus()[0].half_product).not.toEqual(
      expect.arrayContaining([{ marker: "TODO", line: 1, column: 1 }]),
    );
  });

  it("classifies surface only as portal, work, editor, public, or admin", () => {
    const book = new Map(routeLedger().map((entry) => [entry.path, entry.id]));
    for (const row of pageCensus()) {
      expect(SURFACES).toContain(row.surface);
      expect(row.surface).toBe(expectedSurface(row.path, book.get(row.path) ?? ""));
    }

    const cases: [string, string, string][] = [
      ["/admin", "list", "admin"],
      ["/admin/users", "public_consumer", "admin"],
      ["/team/admin/roles", "editor_shell", "admin"],
      ["/administrator", "login", "admin"],
      ["/f/[id]", "public_consumer", "public"],
      ["/r/[id]", "public_consumer", "public"],
      ["/edit", "editor_shell", "editor"],
      ["/", "portal", "portal"],
      ["/sign-in", "login", "portal"],
      ["/leads", "list", "work"],
      ["/isolation", "settings", "work"],
      ["/", "workspace", "work"],
    ];
    for (const [path, id, surface] of cases) {
      expect(expectedSurface(path, id)).toBe(surface);
      expect(surfaceFor(path, id)).toBe(surface);
    }
  });

  it("writes stack next and leaves measurements unread", () => {
    for (const row of pageCensus()) {
      expect(row.stack).toBe("next");
      expect(row.token_source).toBe("not_measured");
      expect(row.states).toBe("not_measured");
      expect(row.responsive).toBe("not_measured");
      expect(row.accessibility).toBe("not_measured");
      expect(row.screenshot).toBe("not_measured");
    }
  });

  it("records half-product hits that match a fresh scan of that page file", () => {
    expect(HALF_PRODUCT_MARKERS).toEqual([
      "TODO",
      "FIXME",
      "inline style",
      "接口说明",
      "架构说明",
      "raw button",
      "raw input",
      "raw table",
      "bare hex",
    ]);
    const fixture = [
      "// TODO: keep",
      "  FIXME style={{ color: 1 }} style=\"x\" 接口说明",
      "架构说明",
      "TODOS FIXMEIT todo <style jsx></style> stylesheet={{",
      "style={color} style='x'",
      "<button <Button x<button _<button </button>",
      "<input <Input My<input </input>",
      "<table <Table a<table </table>",
      "#fff #112233 #FFF #aabbcc #fff; #112233;",
      "#abcd #12345 #1234567 #12 #ggg #fff0",
      "color#fff _#112233 a#abc",
      "<buttoned <inputType <tableView",
    ].join("\n");
    const fixtureHits: HalfProductHit[] = [
      { marker: "TODO", line: 1, column: 4 },
      { marker: "FIXME", line: 2, column: 3 },
      { marker: "inline style", line: 2, column: 9 },
      { marker: "inline style", line: 2, column: 30 },
      { marker: "接口说明", line: 2, column: 40 },
      { marker: "架构说明", line: 3, column: 1 },
      { marker: "inline style", line: 5, column: 1 },
      { marker: "inline style", line: 5, column: 15 },
      { marker: "raw button", line: 6, column: 1 },
      { marker: "raw input", line: 7, column: 1 },
      { marker: "raw table", line: 8, column: 1 },
      { marker: "bare hex", line: 9, column: 1 },
      { marker: "bare hex", line: 9, column: 6 },
      { marker: "bare hex", line: 9, column: 14 },
      { marker: "bare hex", line: 9, column: 19 },
      { marker: "bare hex", line: 9, column: 27 },
      { marker: "bare hex", line: 9, column: 33 },
      { marker: "raw button", line: 12, column: 1 },
      { marker: "raw input", line: 12, column: 11 },
      { marker: "raw table", line: 12, column: 22 },
    ];
    expect(rescan(fixture)).toEqual(fixtureHits);
    expect(scanHalfProduct(fixture)).toEqual(fixtureHits);
    expect(rescan("接口 说明\n架构 说明")).toEqual([]);

    const rows = pageCensus();
    for (const row of rows) {
      const source = readFileSync(join(webDir, row.page_file), "utf8");
      const hits = rescan(source);
      expect(row.half_product).toEqual(hits);
      expect(scanHalfProduct(source)).toEqual(hits);
      expect(row.half_product).not.toBe(scanHalfProduct(source));
    }

    const form = rows.find((row) => row.path === "/f/[id]");
    expect(form?.half_product).toEqual([
      { marker: "inline style", line: 155, column: 29 },
      { marker: "raw input", line: 158, column: 13 },
      { marker: "inline style", line: 169, column: 15 },
      { marker: "raw input", line: 174, column: 11 },
      { marker: "inline style", line: 178, column: 22 },
      { marker: "bare hex", line: 178, column: 39 },
      { marker: "inline style", line: 182, column: 27 },
      { marker: "bare hex", line: 182, column: 44 },
      { marker: "raw button", line: 183, column: 9 },
    ]);
    expect(rows.filter((row) => row.half_product.length > 0).map((row) => row.path)).toEqual([
      "/",
      "/leads/[id]",
      "/contacts",
      "/contacts/[id]",
      "/opportunities",
      "/opportunities/[id]",
      "/reception",
      "/sop",
      "/outbound",
      "/attribution",
      "/subscription",
      "/intent",
      "/enterprises",
      "/channel-interactions",
      "/isolation",
      "/f/[id]",
      "/r/[id]",
    ]);
    expect(rows.filter((row) => row.half_product.length === 0).map((row) => row.path)).toEqual(["/leads"]);
  });

  it("does not name forbidden packages", () => {
    const names = ["public" + "-" + "ai", "sh" + "adcn"];
    const files = [
      new URL("../src/lib/pageCensus.ts", import.meta.url),
      new URL("./pageCensus.test.ts", import.meta.url),
    ];
    for (const file of files) {
      const src = readFileSync(file, "utf8");
      for (const name of names) expect(src.includes(name)).toBe(false);
    }
  });
});
