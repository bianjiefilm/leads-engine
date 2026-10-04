import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RecordFrame, SurfaceState, WorkbenchChrome } from "@/components/workbench/chrome";
import {
  MISSING_SCOPE,
  acceptDeskPayload,
  acceptLeadRows,
  applyWorkTenant,
  chooseScope,
  droppedTenantRows,
  factTone,
  failureText,
  guardTenantCommit,
  listTenantHeader,
  nextLeadTicket,
  pagePrimary,
  productError,
  receptionSessionFrame,
  reconcileTenant,
  scopeChoices,
  runShellWhoami,
  settleWorkTenant,
  shellStructure,
  toneLabel,
  withSessionScope,
} from "@/lib/productShell";

const scopes = scopeChoices([
  { tenant_id: "tenant-a", display_name: "客户甲" },
  { tenant_id: "tenant-b", display_name: "客户乙" },
  { tenant_id: "tenant-a", display_name: "客户甲重复" },
]);

const facts = [
  { label: "来源", value: "手工录入" },
  { label: "状态", value: "状态：已接收" },
  { label: "负责人", value: "Sales A1" },
  { label: "下一步", value: "安排下一次" },
];

function markup(width: number) {
  return renderToStaticMarkup(
    createElement(WorkbenchChrome, {
      width,
      scopes,
      activeId: "tenant-a",
      onSwitch: () => undefined,
      children: createElement(RecordFrame, {
        width,
        tone: factTone({ kind: "human" }),
        title: "今天要完成",
        facts,
        primary: createElement("button", { type: "button", className: "btn primary" }, pagePrimary("today")),
      }),
    }),
  );
}

const FORMAL_PAGES = [
  "src/app/(shell)/page.tsx",
  "src/app/(shell)/leads/page.tsx",
  "src/app/(shell)/leads/[id]/page.tsx",
  "src/app/(shell)/contacts/page.tsx",
  "src/app/(shell)/contacts/[id]/page.tsx",
  "src/app/(shell)/opportunities/page.tsx",
  "src/app/(shell)/opportunities/[id]/page.tsx",
  "src/app/reception/page.tsx",
  "src/app/(shell)/sop/page.tsx",
  "src/app/(shell)/outbound/page.tsx",
  "src/app/(shell)/attribution/page.tsx",
  "src/app/(shell)/subscription/page.tsx",
  "src/app/(shell)/intent/page.tsx",
  "src/app/(shell)/enterprises/page.tsx",
  "src/app/(shell)/channel-interactions/page.tsx",
  "src/app/(shell)/isolation/page.tsx",
];

describe("sales workbench shell", () => {
  it("switches only among named scopes and drops the other tenant", () => {
    expect(scopes.map((scope) => scope.display_name)).toEqual(["客户甲", "客户乙"]);
    expect(chooseScope(scopes, "tenant-b")?.display_name).toBe("客户乙");
    expect(chooseScope(scopes, "typed-by-hand")).toBeNull();
    expect(reconcileTenant(scopes, "typed-by-hand", "tenant-a")).toBe("tenant-a");
    expect(reconcileTenant(scopes, "tenant-b", "tenant-a")).toBe("tenant-b");
    expect(reconcileTenant(scopes, "typed-by-hand", null)).toBeNull();
    expect(reconcileTenant(scopes, "typed-by-hand", "tenant-real")).toBeNull();
    const session = withSessionScope(scopes, "real-tenant");
    expect(session.map((scope) => scope.display_name)).toContain("当前登录范围");
    expect(withSessionScope(scopes, "tenant-a")).toHaveLength(2);
    expect(chooseScope(session, "typed-by-hand")).toBeNull();
    expect(droppedTenantRows("tenant-b", [
      { id: "a", tenant_id: "tenant-a" },
      { id: "b", tenant_id: "tenant-b" },
    ]).map((row) => row.id)).toEqual(["b"]);
    expect(droppedTenantRows(null, [{ id: "a", tenant_id: "tenant-a" }])).toEqual([]);
    expect(MISSING_SCOPE).not.toContain("请输入");
    expect(MISSING_SCOPE).not.toContain("租户ID");
  });

  it("hides raw transport failures and keeps a sales sentence", () => {
    expect(productError("HTTP 500")).not.toMatch(/HTTP/);
    expect(productError('{"error":"nope"}')).not.toContain("{");
    expect(productError("Failed to fetch")).not.toContain("fetch");
    expect(productError("没有营销许可")).toBe("没有营销许可");
    expect(failureText({ error: "HTTP 403" }, 403)).not.toMatch(/HTTP|403/);
    expect(factTone({ kind: "ai_draft" })).toBe("ai");
    expect(factTone({ kind: "reception" })).toBe("human");
    expect(factTone({ auto: true, kind: "ai_draft" })).toBe("automation");
    expect(toneLabel("ai")).toBe("AI 建议");
    expect(toneLabel("human")).toBe("人工事实");
    expect(toneLabel("automation")).toBe("自动化状态");
  });

  it("renders cards at 390 and 430, and a dense grid at 1440", () => {
    expect(shellStructure(390)).toMatchObject({ density: "compact", navigation: "bar", records: "cards", factLayout: "chips", columns: 1, primaryCount: 1 });
    expect(shellStructure(430)).toMatchObject({ density: "compact", navigation: "bar", records: "cards", factLayout: "chips", columns: 2, primaryCount: 1 });
    expect(shellStructure(1440)).toMatchObject({ density: "dense", navigation: "rail", records: "grid", factLayout: "columns", columns: 4, primaryCount: 1 });

    const phone = markup(390);
    const largePhone = markup(430);
    const desk = markup(1440);
    for (const html of [phone, largePhone]) {
      expect(html).toContain('data-shell-density="compact"');
      expect(html).toContain('data-shell-nav="bar"');
      expect(html).toContain('data-record="cards"');
      expect(html).toContain('data-facts="chips"');
      expect(html).toContain("<li>");
      expect(html).not.toMatch(/<p>[^<]*来源[^<]*负责人[^<]*下一步/);
    }
    expect(phone).toContain('data-columns="1"');
    expect(largePhone).toContain('data-columns="2"');
    expect(desk).toContain('data-shell-density="dense"');
    expect(desk).toContain('data-shell-nav="rail"');
    expect(desk).toContain('data-record="grid"');
    expect(desk).toContain('data-facts="columns"');
    expect(desk).toContain('data-columns="4"');
    expect(desk).toContain("<dt>来源</dt>");
    expect(desk.match(/data-page-primary="true"/g)).toHaveLength(1);
    expect(phone).toContain("客户甲");
    expect(phone).not.toContain("请输入租户");
    expect(phone).not.toContain("租户 id");
    expect(phone).not.toContain(">tenant-a<");
    expect(phone).toContain("记跟进");
  });

  it("gives loading, empty, error and recovery a mark, not a lone sentence", () => {
    for (const kind of ["loading", "empty", "error", "recovery"] as const) {
      const html = renderToStaticMarkup(createElement(SurfaceState, { kind, title: "标题", detail: "说明" }));
      expect(html).toContain(`data-surface-state="${kind}"`);
      expect(html).toContain(`data-state-mark="${kind}"`);
      expect(html).toContain("<h2>");
      expect(html.startsWith("<p")).toBe(false);
    }
    const loading = renderToStaticMarkup(createElement(SurfaceState, { kind: "loading" }));
    expect(loading).toContain('data-skeleton="block"');
    expect(loading).toContain('data-skeleton="line"');
  });

  it("does not issue the fixture tenant when login is outside the preview list", () => {
    const fixture = scopeChoices([
      { tenant_id: "tenant-a", display_name: "客户甲" },
      { tenant_id: "tenant-b", display_name: "客户乙" },
    ]);
    expect(chooseScope(fixture, "tenant-real")).toBeNull();
    const cases = [
      { storedId: "not-a-real-id", sessionTenant: "tenant-real", whoamiOk: true },
      { storedId: "tenant-real", sessionTenant: "tenant-real", whoamiOk: true },
      { storedId: "tenant-real", sessionTenant: null, whoamiOk: false },
      { storedId: "not-a-real-id", sessionTenant: null, whoamiOk: false },
    ];
    for (const item of cases) {
      const settlement = settleWorkTenant({
        membershipScopes: fixture,
        whoamiOk: item.whoamiOk,
        sessionTenant: item.sessionTenant,
        storedId: item.storedId,
      });
      const applied = applyWorkTenant(item.storedId, item.storedId, settlement);
      const header = listTenantHeader(applied.memoryTenantId);
      expect(header?.["x-tenant-id"] ?? "", item.storedId).not.toBe("tenant-a");
      expect(applied.storage, item.storedId).not.toBe("tenant-a");
      if (item.whoamiOk) {
        expect(header).toEqual({ "x-tenant-id": "tenant-real" });
      } else {
        expect(header).toBeNull();
        expect(applied.storage).toBe(item.storedId);
        expect(settlement.persist).toBe(false);
      }
    }
  });

  it("sends the stored tenant on whoami and hides the preview fixtures after failure", async () => {
    const fixture = scopeChoices([
      { tenant_id: "tenant-a", display_name: "客户甲" },
      { tenant_id: "tenant-b", display_name: "客户乙" },
    ]);
    const withReal = scopeChoices([
      ...fixture,
      { tenant_id: "tenant-real", display_name: "真实客户" },
    ]);
    const confirmed = await runShellWhoami({
      storedId: "tenant-real",
      membershipScopes: withReal,
      fetchImpl: async (_path, init) => {
        expect(init.headers["x-tenant-id"]).toBe("tenant-real");
        return { ok: true, body: { tenant_id: "tenant-real" } };
      },
    });
    expect(confirmed.memoryTenantId).toBe("tenant-real");
    expect(listTenantHeader(confirmed.memoryTenantId)).toEqual({ "x-tenant-id": "tenant-real" });
    expect(confirmed.storage).toBe("tenant-real");
    expect(guardTenantCommit(confirmed.switcher, "tenant-real")).toBe("tenant-real");

    const failed = await runShellWhoami({
      storedId: "not-a-real-id",
      membershipScopes: fixture,
      fetchImpl: async (_path, init) => {
        expect(init.headers["x-tenant-id"] ?? "").not.toBe("tenant-a");
        return { ok: false, body: {} };
      },
    });
    expect(failed.switcher.map((scope) => scope.tenant_id)).not.toEqual(expect.arrayContaining(["tenant-a", "tenant-b"]));
    expect(failed.switcher).toEqual([]);
    expect(guardTenantCommit(failed.switcher, "tenant-a")).toBeNull();
    expect(guardTenantCommit(failed.switcher, "tenant-b")).toBeNull();
    expect(failed.memoryTenantId).toBeNull();
    expect(failed.storage).toBe("not-a-real-id");
    expect(listTenantHeader(failed.memoryTenantId)).toBeNull();
    expect(failed.fault).toContain("没有可用的工作范围");

    const denied = await runShellWhoami({
      storedId: "tenant-real",
      membershipScopes: fixture,
      fetchImpl: async () => ({ ok: false, body: {} }),
    });
    expect(denied.requestedTenant).toBe("tenant-real");
    expect(denied.switcher).toEqual([]);
    expect(denied.storage).toBe("tenant-real");
    expect(listTenantHeader(denied.memoryTenantId)?.["x-tenant-id"] ?? "").not.toBe("tenant-a");
  });

  it("drops a late lead list after the tenant switches or clears", async () => {
    const pending = new Map<string, (rows: { id: string }[]) => void>();
    const fetchRows = (tenantId: string) =>
      new Promise<{ id: string }[]>((resolve) => {
        pending.set(tenantId, resolve);
      });
    let active = nextLeadTicket(0, "tenant-a");
    const ticketA = active;
    const flightA = fetchRows("tenant-a").then((rows) => acceptLeadRows(active, ticketA, rows));
    active = nextLeadTicket(active.seq, "tenant-b");
    const ticketB = active;
    const flightB = fetchRows("tenant-b").then((rows) => acceptLeadRows(active, ticketB, rows));
    pending.get("tenant-b")?.([{ id: "lead-b" }]);
    pending.get("tenant-a")?.([{ id: "lead-a" }]);
    expect(await flightA).toBeNull();
    expect(await flightB).toEqual([{ id: "lead-b" }]);

    active = nextLeadTicket(active.seq, "");
    const flightCleared = fetchRows("tenant-a-late").then((rows) => acceptLeadRows(active, ticketA, rows));
    pending.get("tenant-a-late")?.([{ id: "lead-a" }]);
    expect(await flightCleared).toBeNull();
    expect(acceptLeadRows(active, ticketB, [{ id: "lead-b" }])).toBeNull();
  });

  it("drops a late reception desk response and the previous session frame", () => {
    let active = { seq: 1, tenantId: "tenant-a" };
    const late = acceptDeskPayload(active, 1, "tenant-a", [{ session_id: "from-a" }]);
    active = { seq: 2, tenantId: "tenant-b" };
    expect(acceptDeskPayload(active, 1, "tenant-a", [{ session_id: "from-a" }])).toBeNull();
    expect(acceptDeskPayload(active, 2, "tenant-b", [{ session_id: "from-b" }])).toEqual([{ session_id: "from-b" }]);
    expect(late).toEqual([{ session_id: "from-a" }]);
    expect(receptionSessionFrame("sess-a", "tenant-a", "tenant-b")).toBeNull();
    expect(receptionSessionFrame("sess-b", "tenant-b", "tenant-b")).toEqual({ tenant: "tenant-b", sessionId: "sess-b" });
    const page = readFileSync("src/app/reception/page.tsx", "utf8");
    expect(page).toContain("acceptDeskPayload");
    expect(page).toContain("receptionSessionFrame");
    expect(page).not.toContain("tenant={scope.tenantId} sessionId={selected}");
  });

  it("keeps formal pages free of a tenant id field and raw failure copy", () => {
    for (const file of FORMAL_PAGES) {
      const text = readFileSync(file, "utf8");
      expect(text, file).not.toContain('placeholder="租户 id"');
      expect(text, file).not.toContain("请输入租户");
      expect(text, file).not.toContain("先填写当前租户");
      expect(text, file).not.toMatch(/HTTP \$\{/);
      expect(text, file).toContain('data-page-primary="true"');
    }
    const home = readFileSync("src/app/(shell)/page.tsx", "utf8");
    expect(home).toContain("RecordFrame");
    expect(home).toContain("SurfaceState");
    expect(readFileSync("src/components/eco-nav/CrmShell.tsx", "utf8")).toContain("WorkbenchChrome");
    expect(readFileSync("src/app/reception/layout.tsx", "utf8")).toContain("CrmShell");
  });
});
