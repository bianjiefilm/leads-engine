import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// HUI-2621 T 面：root layout importer 契约 + alias 休眠守卫。
// 合同 importer 锚点 = web/src/app/layout.tsx（profile-scopes.json leads-web.importers）。
const WEB = resolve(__dirname, "..");
const LAYOUT = join(WEB, "src/app/layout.tsx");

function walkSources(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (path.includes(`${"vendor"}${"/"}painuo`)) continue; // vendored 树本身不含 importer
    const s = statSync(path);
    if (s.isDirectory()) walkSources(path, out);
    else if (/\.(ts|tsx)$/.test(name)) out.push(path);
  }
  return out;
}

describe("painuo tokens importer contract (HUI-2621 T-face)", () => {
  it("R2: root layout imports the generated tokens.css", () => {
    const layout = readFileSync(LAYOUT, "utf8");
    expect(layout).toContain("../styles/generated/painuo/v1/tokens.css");
  });

  it("R2: root layout imports the vendored renderer styles.css", () => {
    const layout = readFileSync(LAYOUT, "utf8");
    expect(layout).toContain("../vendor/painuo/react/v1/src/styles.css");
  });

  it("R2: alias bridge stays dormant — aliases.css/scss are on disk but imported nowhere", () => {
    // 休眠守卫：落盘不 import。激活属 2626 决策（contrast 角色迁移前置）。
    const offenders: string[] = [];
    for (const path of walkSources(join(WEB, "src"))) {
      const text = readFileSync(path, "utf8");
      if (/styles\/generated\/painuo\/v1\/aliases\.(css|scss)/.test(text)) offenders.push(path);
    }
    expect(offenders).toEqual([]);
  });
});
