import { describe, expect, it } from "vitest";
import { presentOutbound, receiptLabel } from "../src/lib/outboundCall";

describe("outbound call display", () => {
  it("keeps production auto closed when a payload claims a live line and a charge", () => {
    const view = presentOutbound({
      production_auto: true,
      real_line: true,
      verification: "verified",
      cost: "known",
      live_charge: 880,
      dial_succeeded: true,
      real_connected: true,
    });
    expect(view.productionAuto).toBe(false);
    expect(view.realLine).toBe(false);
    expect(view.cost).toBe("unknown");
    expect(view.simulation).toBe(true);
    expect(view.note).toContain("模拟");
    expect(view.note).toContain("关闭");
  });

  it("labels each receipt without turning a simulation or an HTTP ack into success", () => {
    expect(receiptLabel("dial_submission", "simulated")).toBe("模拟提交");
    expect(receiptLabel("ringing", "simulated")).toBe("模拟响铃");
    expect(receiptLabel("connected", "simulated")).toBe("模拟接通");
    expect(receiptLabel("call_completed", "simulated")).toBe("模拟通话完成");
    expect(receiptLabel("intent_suggestion", "recorded")).toBe("意向建议");
    expect(receiptLabel("connected", "provider_ack")).toBe("接口应答");
    expect(receiptLabel("connected", "success")).toBe("未证实接通");
  });
});
