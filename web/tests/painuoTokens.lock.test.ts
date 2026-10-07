import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// HUI-2621 T 面：semantic tokens 消费锁与产物字节校验。
// producer 钉 public-ai@01e6765（Root r1 裁决：origin/main 前进后 renderer 子树有变更，按裁决退回设计钉）。
const GENERATED = resolve(__dirname, "../src/styles/generated/painuo/v1");
const LOCK_PATH = join(GENERATED, "painuo-tokens.lock.json");
const PRODUCER_COMMIT = "01e67653b0e394f9a6d4f74497945c33681a6bc2";
const OWNED_FILES = [
  "tokens.css",
  "tokens.scss",
  "resolved.json",
  "token-table.md",
  "lint-allowlist.json",
  "aliases.css",
  "aliases.scss",
];

function sha256(data: string | Buffer): string {
  return createHash("sha256").update(data).digest("hex");
}

type TokenLock = {
  schemaVersion: string;
  tokenVersion: string;
  sourceCommit: string;
  sourceCanonicalSha256: string;
  consumerId: string;
  defaultSurface: string;
  defaultTheme: string;
  files: Record<string, string>;
  aliasesDeprecatedUntil: Record<string, unknown>;
};

function readLock(): TokenLock {
  return JSON.parse(readFileSync(LOCK_PATH, "utf8")) as TokenLock;
}

describe("painuo tokens consumer lock (HUI-2621 T-face)", () => {
  it("R1: lock exists, is schema painuo-token-lock/v1 and pins producer HEAD 01e6765", () => {
    const lock = readLock();
    expect(lock.schemaVersion).toBe("painuo-token-lock/v1");
    expect(lock.consumerId).toBe("leads-web");
    expect(lock.defaultSurface).toBe("work.light");
    expect(lock.defaultTheme).toBe("light");
    expect(lock.sourceCommit).toBe(PRODUCER_COMMIT);
    expect(lock.sourceCanonicalSha256).toMatch(/^[0-9a-f]{64}$/);
  });

  it("R1: lock.files covers exactly the 7 owned artifacts and every file matches byte for byte", () => {
    const lock = readLock();
    const expected = OWNED_FILES.map((name) => `web/src/styles/generated/painuo/v1/${name}`).sort();
    expect(Object.keys(lock.files).sort()).toEqual(expected);
    for (const [target, digest] of Object.entries(lock.files)) {
      const name = target.slice("web/src/styles/generated/painuo/v1/".length);
      expect(sha256(readFileSync(join(GENERATED, name))), name).toBe(digest);
    }
  });

  it("R1: all 8 artifacts (7 owned + lock) are present on disk", () => {
    for (const name of [...OWNED_FILES, "painuo-tokens.lock.json"]) {
      expect(() => readFileSync(join(GENERATED, name)), name).not.toThrow();
    }
  });

  it("R1: alias bridge files stay deprecated-pending and version stays draft-honest", () => {
    const lock = readLock();
    expect(lock.tokenVersion).toBe("1.0.0");
    expect(lock.aliasesDeprecatedUntil).toHaveProperty("v1FirstReleasedAt", null);
  });

  it("R7: tampering one byte in any owned artifact makes the lock verification fail", () => {
    const lock = readLock();
    const target = `web/src/styles/generated/painuo/v1/tokens.css`;
    const original = readFileSync(join(GENERATED, "tokens.css"));
    const tampered = Buffer.from(original);
    tampered[0] = tampered[0] === 0x2f /* '/' */ ? 0x2e /* '.' */ : 0x2f;
    const dir = join(tmpdir(), `painuo-tamper-probe-${Date.now()}-${process.pid}`);
    mkdirSync(dir, { recursive: true });
    try {
      writeFileSync(join(dir, "tokens.css"), tampered);
      const actual = sha256(readFileSync(join(dir, "tokens.css")));
      expect(actual).not.toBe(lock.files[target]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
