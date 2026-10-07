import { describe, expect, it } from "vitest";
import { DEGRADED_STATE, roiStatusToState } from "@/lib/painuoStatus";

// HUI-2622 R 面：roiStatus → renderer StatusState 纯映射（无 IO、无 React）。

describe("roiStatus to renderer StatusState mapping (HUI-2622 R-face)", () => {
  it("R6: complete maps to resultReady", () => {
    expect(roiStatusToState("complete")).toBe("resultReady");
  });

  it("R6: incomplete maps to needsAttention", () => {
    expect(roiStatusToState("incomplete")).toBe("needsAttention");
  });

  it("R6: source_stats_only maps to waiting", () => {
    expect(roiStatusToState("source_stats_only")).toBe("waiting");
  });

  it("R6: unknown values degrade to the neutral state, never a success colour", () => {
    expect(DEGRADED_STATE).toBe("queued");
    expect(roiStatusToState("mystery-status")).toBe("queued");
    expect(roiStatusToState("")).toBe("queued");
  });
});
