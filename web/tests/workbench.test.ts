import { describe, expect, it } from "vitest";
import { emptyCrmCache, rememberRows, switchCrmTenant, visibleRows } from "@/lib/eco-nav/crm-scope";
import {
  NARROW_ACTIONS,
  acceptDesk,
  moneyView,
  showServiceDraft,
  statusLine,
} from "@/lib/workbench";

describe("sales desk", () => {
  it("keeps the three narrow-screen actions available", () => {
    expect(NARROW_ACTIONS).toEqual(["查看新线索", "记跟进", "安排下一次"]);
  });

  it("drops another tenant's queue when the workspace changes", () => {
    let cache = switchCrmTenant(emptyCrmCache(), "tnt_A");
    cache = rememberRows(cache, "workbench", [{ id: "lead_a", tenant_id: "tnt_A" }]);
    expect(visibleRows(cache, "workbench").map((row) => row.id)).toEqual(["lead_a"]);
    cache = switchCrmTenant(cache, "tnt_B");
    expect(cache.workbench).toBeNull();
    expect(visibleRows(cache, "workbench")).toEqual([]);
    const kept = acceptDesk("tnt_B", {
      unprocessed: [
        { id: "lead_a", tenant_id: "tnt_A", kind: "lead", lead_id: "lead_a" },
        { id: "lead_b", tenant_id: "tnt_B", kind: "lead", lead_id: "lead_b" },
      ],
    });
    expect(kept.unprocessed.map((row) => row.id)).toEqual(["lead_b"]);
  });

  it("shows three money lines and does not add them together", () => {
    const view = moneyView({
      customer_deal_cents: 1500,
      painuo_service_order_cents: 9000,
      platform_tool_spend_cents: 50,
    });
    expect(view.lines.map((line) => line.label)).toEqual(["客户成交", "派诺服务订单", "平台工具消费"]);
    expect(view.lines.map((line) => line.cents)).toEqual([1500, 9000, 50]);
    expect(view.total).toBeUndefined();
    expect(JSON.stringify(view)).not.toContain("10550");
  });

  it("hides the service draft on store sales and keeps manual status facts", () => {
    expect(showServiceDraft("merchant_customer")).toBe(false);
    expect(showServiceDraft("creative_service")).toBe(true);
    expect(statusLine({
      submitted: false,
      received: true,
      assigned: true,
      followed: true,
      won: false,
      paid: false,
    })).toBe("已接收 · 已分配 · 已跟进");
  });
});
