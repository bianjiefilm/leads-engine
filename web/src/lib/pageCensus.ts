// 账上用户路由的页面普查。只记账，不改页面。
// 半成品标记只扫这条路由自己的 page 文件，不扫它引用的组件。

import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { routeLedger } from "./pageMap";

export const SURFACES = ["portal", "work", "editor", "public", "admin"] as const;

export type Surface = (typeof SURFACES)[number];

export const NOT_MEASURED = "not_measured" as const;

export const HALF_PRODUCT_MARKERS = [
  "TODO",
  "FIXME",
  "inline style",
  "接口说明",
  "架构说明",
  "raw button",
  "raw input",
  "raw table",
  "bare hex",
] as const;

export type HalfProductMarker = (typeof HALF_PRODUCT_MARKERS)[number];

export interface HalfProductHit {
  marker: HalfProductMarker;
  line: number;
  column: number;
}

export interface PageCensusEntry {
  path: string;
  id: string;
  surface: Surface;
  stack: "next";
  token_source: typeof NOT_MEASURED;
  states: typeof NOT_MEASURED;
  responsive: typeof NOT_MEASURED;
  accessibility: typeof NOT_MEASURED;
  screenshot: typeof NOT_MEASURED;
  page_file: string;
  half_product: HalfProductHit[];
}

const webDir = join(dirname(fileURLToPath(import.meta.url)), "../..");
const appDir = join(webDir, "src/app");
const pageFile = /^page\.(tsx|ts|jsx|js)$/;

// 路径里出现 /admin 时记 admin，先于模式编号。
export function surfaceFor(path: string, id: string): Surface {
  if (path.includes("/admin")) return "admin";
  if (id === "public_consumer") return "public";
  if (id === "editor_shell") return "editor";
  if (id === "portal" || id === "login") return "portal";
  return "work";
}

function position(source: string, index: number): { line: number; column: number } {
  const prior = source.slice(0, index);
  return { line: prior.split("\n").length, column: index - prior.lastIndexOf("\n") };
}

export function scanHalfProduct(source: string): HalfProductHit[] {
  const rules: { marker: HalfProductMarker; re: RegExp }[] = [
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

export function pageCensus(): PageCensusEntry[] {
  const files = discoverPageFiles();
  return routeLedger().map((entry) => {
    const page_file = files.get(entry.path);
    if (page_file === undefined) throw new Error(`missing page file for route ${entry.path}`);
    const hits = scanHalfProduct(readFileSync(join(webDir, page_file), "utf8"));
    return {
      path: entry.path,
      id: entry.id,
      surface: surfaceFor(entry.path, entry.id),
      stack: "next",
      token_source: NOT_MEASURED,
      states: NOT_MEASURED,
      responsive: NOT_MEASURED,
      accessibility: NOT_MEASURED,
      screenshot: NOT_MEASURED,
      page_file,
      half_product: hits,
    };
  });
}
