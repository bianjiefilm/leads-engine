import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// HUI-2622 R 面：vendored renderer 源逐字节身份 + 消费者锁 pin。
// 仿 GoBoost fixture renderer.lock/fixture-source.lock 双锁形态（schema=renderer-lock.schema/v1）。
const VENDOR = resolve(__dirname, "../src/vendor/painuo/react/v1");
const LOCK_PATH = resolve(__dirname, "../src/vendor/painuo/react/v1.lock.json");

// producer 钉 public-ai@01e6765（Root r1 重钉裁决回退点；常量在钉点上实核计算，见 audit 证据）。
const EXPECTED_PRODUCER_COMMIT = "01e67653b0e394f9a6d4f74497945c33681a6bc2";
const EXPECTED_PRODUCER_TREE = "f5370120ac11a529b5032c17f5567b1ffe5effee";
const EXPECTED_MANIFEST_SHA256 = "15bcaacab450e29fbe50e735ea735dce058f74a512f3e0a6fdd2fd6f43a643f2";

function sha256(data: string | Buffer): string {
  return createHash("sha256").update(data).digest("hex");
}

type RendererLock = {
  schemaVersion: number;
  producerCommit: string;
  producerTree: string;
  manifestSha256: string;
  consumerRepo: string;
  profile: string;
  files: { source: string; destination: string; sha256: string }[];
  host: {
    lockPath: string;
    lockSha256: string;
    manifestPath: string;
    manifestSha256: string;
    expected: Record<string, string>;
  };
  token: {
    sourceSubject: string;
    canonicalSha256: string;
    rawSha256: string;
    cssSha256: string;
    testOnly: boolean;
    productionAdopted: boolean;
  };
  contractHashes: Record<string, string>;
};

function readLock(): RendererLock {
  return JSON.parse(readFileSync(LOCK_PATH, "utf8")) as RendererLock;
}

describe("painuo renderer vendored identity (HUI-2622 R-face)", () => {
  it("R3: lock exists and pins producer 01e6765 (commit, tree, manifest hash)", () => {
    const lock = readLock();
    expect(lock.schemaVersion).toBe(1);
    expect(lock.producerCommit).toBe(EXPECTED_PRODUCER_COMMIT);
    expect(lock.producerTree).toBe(EXPECTED_PRODUCER_TREE);
    expect(lock.manifestSha256).toBe(EXPECTED_MANIFEST_SHA256);
    expect(lock.consumerRepo).toBe("leads-engine");
    expect(lock.profile).toBe("leads-web");
  });

  it("R3: all manifest files are vendored byte-identical (40/40 sha256)", () => {
    const lock = readLock();
    expect(lock.files).toHaveLength(40);
    const seen = new Set<string>();
    for (const entry of lock.files) {
      expect(seen.has(entry.source), `duplicate source ${entry.source}`).toBe(false);
      seen.add(entry.source);
      expect(entry.destination).toBe(entry.source); // v1/ 即 vendored 根，source 原样映射
      const bytes = readFileSync(join(VENDOR, entry.destination));
      expect(sha256(bytes), entry.destination).toBe(entry.sha256);
    }
    expect(seen.size).toBe(40);
  });

  it("R3: vendored contract files match the lock's contractHashes section", () => {
    const lock = readLock();
    expect(Object.keys(lock.contractHashes).sort()).toEqual(
      [
        "contract/api.json",
        "contract/behavior-vectors.json",
        "contract/profile-scopes.json",
        "contract/renderer-lock.schema.json",
      ].sort(),
    );
    for (const [rel, digest] of Object.entries(lock.contractHashes)) {
      expect(sha256(readFileSync(join(VENDOR, rel))), rel).toBe(digest);
    }
  });

  it("R3: host responsibility pins the qualified dependency matrix", () => {
    const lock = readLock();
    expect(lock.host.expected["react"]).toBe("19.1.1");
    expect(lock.host.expected["react-dom"]).toBe("19.1.1");
    expect(lock.host.expected["@base-ui/react"]).toBe("1.8.0");
    expect(lock.host.expected["lucide-react"]).toBe("1.44.0");
    expect(lock.host.manifestPath).toBe("web/package.json");
    expect(lock.host.lockPath).toBe("web/package-lock.json");
    expect(lock.host.lockSha256).toMatch(/^[0-9a-f]{64}$/);
  });

  it("R3: token block mirrors the producer manifest honestly (draft, not production-adopted)", () => {
    const lock = readLock();
    expect(lock.token).toEqual({
      sourceSubject: "4df00ece5b9196396aed878c590f418e8cd5f2a6",
      canonicalSha256: "a46310d3a401f0992d346d6c90c1643e84fca4181b990312278d2c7808d581db",
      rawSha256: "20a8e65f86e9c2582230280708a93c6af896e4168c0b3f443fba4146dbc461d2",
      cssSha256: "1045ef45d7c42e6d6e8ddbb1cfc83c51024aeef299f5f54c80c057363600dbbd",
      testOnly: true,
      productionAdopted: false,
    });
  });
});
