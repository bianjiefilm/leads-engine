import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  MissingRegionError,
  UnknownPatternError,
  catalog,
  patternIDs,
  requiredRegions,
  routeLedger,
  validateDeclaration,
} from "@/lib/pageMap";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "../src/app");
const pageFile = /^page\.(tsx|ts|jsx|js)$/;

function routeFromPageFile(relativeFile: string): string {
  const parts = relativeFile.split("/").filter((part) => part.length > 0);
  parts.pop();
  const visible = parts.filter((part) => !(part.startsWith("(") && part.endsWith(")")));
  return visible.length === 0 ? "/" : `/${visible.join("/")}`;
}

function discoverUserRoutes(dir = appDir, relative = ""): string[] {
  const found: string[] = [];
  for (const name of readdirSync(dir)) {
    if (name.startsWith("_") || name.startsWith("@")) continue;
    const abs = join(dir, name);
    const next = relative ? `${relative}/${name}` : name;
    if (statSync(abs).isDirectory()) {
      found.push(...discoverUserRoutes(abs, next));
      continue;
    }
    if (!pageFile.test(name)) continue;
    found.push(routeFromPageFile(next));
  }
  return found;
}

describe("frozen pattern ids", () => {
  it("keeps the fourteen ids in ticket order", () => {
    const want = [
      "portal",
      "login",
      "workspace",
      "list",
      "detail",
      "creation",
      "media_result",
      "compare",
      "review",
      "billing",
      "settings",
      "recovery",
      "public_consumer",
      "editor_shell",
    ];
    const got = patternIDs();
    expect(got).toEqual(want);
    expect(got).toHaveLength(14);
    expect(new Set(got).size).toBe(14);
    got[0] = "mutated";
    expect(patternIDs()[0]).toBe("portal");
  });

  it("requires primary_action, status, and empty, in that order", () => {
    const got = requiredRegions();
    expect(got).toEqual(["primary_action", "status", "empty"]);
    got[0] = "mutated";
    expect(requiredRegions()[0]).toBe("primary_action");
  });

  it("gives every frozen id a declaration that carries the three regions", () => {
    const ids = patternIDs();
    const rows = catalog();
    expect(rows.map((row) => row.id)).toEqual(ids);
    for (const row of rows) validateDeclaration(row);
    rows[0].regions[0] = "mutated";
    expect(catalog()[0].regions[0]).toBe("primary_action");
  });
});

describe("declaration checks", () => {
  it("fails when any required region is missing", () => {
    const required = requiredRegions();
    for (const id of patternIDs()) {
      for (const drop of required) {
        const regions = required.filter((region) => region !== drop);
        expect(() => validateDeclaration({ id, regions })).toThrow(MissingRegionError);
        try {
          validateDeclaration({ id, regions });
        } catch (err) {
          expect(err).toBeInstanceOf(MissingRegionError);
          expect(err).not.toBeInstanceOf(UnknownPatternError);
          const missing = err as MissingRegionError;
          expect(missing.id).toBe(id);
          expect(missing.regions).toEqual([drop]);
        }
      }
      expect(() => validateDeclaration({ id, regions: [] })).toThrow(MissingRegionError);
      try {
        validateDeclaration({ id, regions: [] });
      } catch (err) {
        expect((err as MissingRegionError).regions).toEqual(required);
      }
    }
  });

  it("fails closed on an unknown id and does not call it a missing region", () => {
    const regions = requiredRegions();
    for (const id of ["", "Portal", "portal-landing", "goboost", "editor-shell", "media-result"]) {
      expect(() => validateDeclaration({ id, regions })).toThrow(UnknownPatternError);
      try {
        validateDeclaration({ id, regions });
      } catch (err) {
        expect(err).toBeInstanceOf(UnknownPatternError);
        expect(err).not.toBeInstanceOf(MissingRegionError);
        expect((err as UnknownPatternError).id).toBe(id);
      }
    }
  });

  it("compares region names exactly", () => {
    expect(() =>
      validateDeclaration({
        id: "portal",
        regions: [" primary_action", "Status", "empty"],
      }),
    ).toThrow(MissingRegionError);
    try {
      validateDeclaration({
        id: "portal",
        regions: [" primary_action", "Status", "empty"],
      });
    } catch (err) {
      expect((err as MissingRegionError).regions).toEqual(["primary_action", "status"]);
    }
  });

  it("ignores extra region names", () => {
    expect(() =>
      validateDeclaration({
        id: "detail",
        regions: [...requiredRegions(), "header"],
      }),
    ).not.toThrow();
  });
});

describe("route ledger", () => {
  it("matches the user pages on disk, one entry each", () => {
    const disk = discoverUserRoutes();
    const book = routeLedger();
    const paths = book.map((entry) => entry.path);

    expect(new Set(disk).size).toBe(disk.length);
    expect(new Set(paths).size).toBe(paths.length);
    expect(paths.slice().sort()).toEqual(disk.slice().sort());

    for (const entry of book) {
      expect(entry.path.startsWith("/")).toBe(true);
      expect(entry.path.includes("(")).toBe(false);
      for (const region of requiredRegions()) {
        expect(entry.regions).toContain(region);
      }
      validateDeclaration(entry);
    }

    book[0].regions[0] = "mutated";
    expect(routeLedger()[0].regions).toEqual(["primary_action", "status", "empty"]);
  });

  it("does not treat the api handler as a user page", () => {
    expect(discoverUserRoutes()).not.toContain("/api/[...path]");
    expect(routeLedger().map((entry) => entry.path)).not.toContain("/api/[...path]");
  });

  it("does not import an external page-pattern package", () => {
    const src = readFileSync(new URL("../src/lib/pageMap.ts", import.meta.url), "utf8");
    expect(src).not.toMatch(/from\s+["'][^"']*public-ai/);
    expect(src).not.toMatch(/import\s*\(\s*["'][^"']*public-ai/);
  });
});
