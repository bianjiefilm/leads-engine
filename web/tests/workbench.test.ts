import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { emptyCrmCache, rememberRows, switchCrmTenant, visibleRows } from "@/lib/eco-nav/crm-scope";
import {
  NARROW_ACTIONS,
  acceptDesk,
  allowedContactText,
  billingCaption,
  deskState,
  jointChainLine,
  modelAdviceLine,
  moneyView,
  narrowWorkbenchFlow,
  refreshedLead,
  renderLeadFacts,
  salesScopeIsNotOwner,
  scopeCaption,
  serviceDraftClick,
  serviceDraftControl,
  showServiceDraft,
  statusLine,
  syncLine,
  visibleOutreach,
} from "@/lib/workbench";
import fixture from "./fixtures/hui-1893-local-lead.json";

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
    expect(serviceDraftClick().outreach).toBe(false);
    expect(serviceDraftClick().message).not.toContain("已成交");
    expect(serviceDraftClick().message).not.toContain("自动触达已成功");
  });

  it("shows one tenant-local fixture lead and does not paint unknown sync as followed-up", () => {
    expect(fixture.touch_delivered).toBe(false);
    expect(fixture.note).toContain("不是 Touch");
    expect(salesScopeIsNotOwner(fixture.scopes.sales, fixture.scopes.owner)).toBe(true);
    expect(salesScopeIsNotOwner("tenant", "tenant")).toBe(false);
    expect(scopeCaption("own")).toBe("我的范围");
    expect(scopeCaption("tenant")).toBe("全租户");
    expect(scopeCaption("own")).not.toBe(scopeCaption("tenant"));

    const kept = acceptDesk(fixture.tenant_id, {
      unprocessed: [fixture.lead, fixture.other_tenant_lead],
    });
    expect(kept.unprocessed.map((row) => row.id)).toEqual([fixture.lead.id]);
    const refreshed = acceptDesk(fixture.tenant_id, {
      unprocessed: [fixture.lead, fixture.other_tenant_lead],
    });
    const first = renderLeadFacts(kept.unprocessed[0], fixture.joint_chain);
    const second = renderLeadFacts(refreshed.unprocessed[0], fixture.joint_chain);
    expect(refreshedLead(first, second)?.id).toBe(fixture.lead.id);
    expect(first).toMatchObject({
      source: "手工录入 · 2026-09-29T01:00:00Z",
      owner: "Sales A1",
      state: "已接收 · 已分配",
      next: "不是模型输出 · 安排下一次跟进",
      sync: "同步：未知",
      chain: "联合经营链未完成",
      outreachSubmitted: false,
    });
    expect(first.state).not.toContain("已跟进");
    expect(first.sync).not.toBe("已跟进");
    expect(first.chain).not.toContain("已跟进");
    expect(deskState(fixture.lead.statuses, fixture.lead.sync)).not.toContain("已跟进");
    expect(jointChainLine(fixture.joint_chain)).toBe("联合经营链未完成");
    expect(jointChainLine({ status: "incomplete", label: "已跟进" })).toBe("联合经营链未完成");
    expect(visibleOutreach("自动触达已成功")).toBe("");
    expect(visibleOutreach("没有营销许可")).toBe("没有营销许可");

    const pool = renderLeadFacts(fixture.unassigned, fixture.joint_chain);
    expect(pool.owner).toBe("待分配");
    expect(pool.state).not.toContain("已跟进");

    const home = readFileSync("src/app/(shell)/page.tsx", "utf8");
    const leadPage = readFileSync("src/app/(shell)/leads/[id]/page.tsx", "utf8");
    expect(home).toContain("renderLeadFacts");
    expect(home).toContain("状态：");
    expect(home).toContain("jointChainLine");
    expect(home).toContain("visibleOutreach");
    expect(home).not.toContain("自动触达已成功");
    expect(leadPage).toContain("jointChainLine");
    expect(leadPage).toContain("deskState");
    expect(leadPage).not.toContain("自动触达已成功");
  });
});
