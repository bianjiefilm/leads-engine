import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RecordFrame, SurfaceState, WorkbenchChrome } from "@/components/workbench/chrome";
import {
  MISSING_SCOPE,
  chooseScope,
  droppedTenantRows,
  factTone,
  failureText,
  pagePrimary,
  productError,
  reconcileTenant,
  scopeChoices,
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
