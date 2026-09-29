import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { emptyCrmCache, rememberRows, switchCrmTenant, visibleRows } from "@/lib/eco-nav/crm-scope";
import {
  NARROW_ACTIONS,
  acceptDesk,
  allowedContactText,
  billingCaption,
  modelAdviceLine,
  moneyView,
  narrowWorkbenchFlow,
  serviceDraftClick,
  serviceDraftControl,
  showServiceDraft,
  statusLine,
  syncLine,
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

  it("stacks the three actions at 390px and does not hide them", () => {
    const flow = narrowWorkbenchFlow(390);
    expect(flow.stacked).toBe(true);
    expect(flow.actions).toEqual(["查看新线索", "记跟进", "安排下一次"]);
    expect(flow.hidden).toEqual([]);
    const css = readFileSync("src/app/globals.css", "utf8");
    expect(css).toMatch(/@media \(max-width: 720px\)[\s\S]*\.queue-actions[\s\S]*flex-direction:\s*column/);
  });

  it("keeps an unknown sync unknown", () => {
    expect(syncLine(undefined)).toBe("同步：未知");
    expect(syncLine({ crm: "unknown" })).toBe("同步：未知");
    expect(syncLine({ crm: "unknown" })).not.toBe("已跟进");
    expect(syncLine({ crm: "unknown" })).not.toContain("0");
    expect(syncLine({ crm: "received" })).toBe("CRM已接收");
  });

  it("does not price ordinary work or mark billing pass", () => {
    expect(billingCaption()).toBe("不向用户报价");
    expect(billingCaption()).not.toContain("PASS");
    expect(billingCaption()).not.toContain("¥");
    expect(modelAdviceLine()).toBe("真实模型未完成");
  });

  it("names only permitted contact methods", () => {
    expect(allowedContactText(undefined)).toBe("无");
    expect(allowedContactText(["sms", "phone", "channel"])).toBe("短信、电话、渠道内回复");
  });

  it("does not show or submit a service draft unless the server says it is present", () => {
    expect(serviceDraftControl({ present: false, enabled: false, reason: "门店销售需类别、权限和人工确认同时成立" }).present).toBe(false);
    const disabled = serviceDraftControl({ present: true, enabled: false, reason: "需要人工确认，本轮不会把草稿交给接单应用" });
    expect(disabled.present).toBe(true);
    expect(disabled.enabled).toBe(false);
    expect(disabled.reason).toContain("人工确认");
    expect(serviceDraftClick().submitted).toBe(false);
    expect(serviceDraftClick().message).not.toContain("已成交");
  });
});
