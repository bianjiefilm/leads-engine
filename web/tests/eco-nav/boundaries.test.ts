import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "../..");

function source(path: string): string {
  return readFileSync(join(root, path), "utf8");
}

describe("EcoTopNav boundaries", () => {
  it("keeps the public form and the root layout free of EcoTopNav", () => {
    expect(source("src/app/f/[id]/page.tsx")).not.toMatch(/EcoTopNav/);
    expect(source("src/app/layout.tsx")).not.toMatch(/EcoTopNav/);
    expect(source("src/app/(shell)/layout.tsx")).toMatch(/EcoTopNav|CrmShell/);
  });

  it("does not import the service-draft handoff path from the nav", () => {
    const nav = source("src/components/eco-nav/EcoTopNav.tsx");
    const shell = source("src/components/eco-nav/CrmShell.tsx");
    expect(nav + shell).not.toMatch(/serviceDraft|service-draft|handoffsender/);
  });

  it("does not hard-code ecosystem hosts in the nav implementation", () => {
    const files = [
      "src/lib/eco-nav/model.ts",
      "src/lib/eco-nav/fixture.ts",
      "src/lib/eco-nav/load.ts",
      "src/components/eco-nav/EcoTopNav.tsx",
      "src/components/eco-nav/CrmShell.tsx",
    ];
    for (const file of files) {
      const text = source(file);
      expect(text).not.toMatch(/[a-z0-9-]+\.(example|huigoo|invalid)\b/);
      expect(text).not.toMatch(/launch_url/);
    }
  });
});
