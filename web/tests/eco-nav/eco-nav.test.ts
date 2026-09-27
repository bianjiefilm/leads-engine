import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { navbarCrmAffordances } from "@/lib/eco-nav/affordances";
import { emptyCrmCache, rememberRows, switchCrmTenant, visibleRows } from "@/lib/eco-nav/crm-scope";
import { applyManualAppSwitch } from "@/lib/eco-nav/manual-switch";
import {
  billingBadgeText,
  LEADS_APP_ID,
  parseEcoNavDocument,
  pinLeadsApp,
  planManualSwitch,
  selectTenant,
  switchableApps,
  toViewModel,
  type EcoNavModel,
  type VisibleApp,
} from "@/lib/eco-nav/model";
import { shouldMountEcoTopNav } from "@/lib/eco-nav/mount";
import { provisionalEcoNav } from "@/lib/eco-nav/fixture";

const here = dirname(fileURLToPath(import.meta.url));

function readFixture(name: string): unknown {
  return JSON.parse(readFileSync(join(here, "fixtures", name), "utf8"));
}

function view(document: unknown, provenance: "public_ai_context" | "provisional_fixture" = "public_ai_context"): EcoNavModel {
  const parsed = parseEcoNavDocument(document);
  if (!parsed.ok) throw new Error("fixture rejected");
  return pinLeadsApp(toViewModel(parsed.document, { provenance, statusSummary: "" }));
}

describe("mount", () => {
  it("hides EcoTopNav on anonymous forms even when a session exists", () => {
    expect(shouldMountEcoTopNav({ surface: "public_form", authenticated: true })).toBe(false);
    expect(shouldMountEcoTopNav({ surface: "public_form", authenticated: false })).toBe(false);
  });

  it("mounts EcoTopNav only on the authenticated CRM shell", () => {
    expect(shouldMountEcoTopNav({ surface: "crm", authenticated: false })).toBe(false);
    expect(shouldMountEcoTopNav({ surface: "crm", authenticated: true })).toBe(true);
  });
});

describe("tenant scope", () => {
  it("clears contacts and opportunities across 20 A/B switches", () => {
    let cache = switchCrmTenant(emptyCrmCache(), "tnt_A");
    cache = rememberRows(cache, "contacts", [{ id: "c-a", tenant_id: "tnt_A" }]);
    cache = rememberRows(cache, "opportunities", [{ id: "o-a", tenant_id: "tnt_A" }]);
    cache = { ...cache, filters: { name: "甲" }, details: { "c-a": { name: "甲" } } };

    for (let i = 0; i < 20; i++) {
      const next = i % 2 === 0 ? "tnt_B" : "tnt_A";
      cache = switchCrmTenant(cache, next);
      expect(cache.tenantId).toBe(next);
      expect(cache.contacts).toBeNull();
      expect(cache.opportunities).toBeNull();
      expect(cache.leads).toBeNull();
      expect(cache.filters).toEqual({});
      expect(cache.details).toEqual({});
      expect(visibleRows(cache, "contacts")).toEqual([]);
      expect(visibleRows(cache, "opportunities")).toEqual([]);
      cache = rememberRows(cache, "contacts", [
        { id: "c-a", tenant_id: "tnt_A" },
        { id: "c-b", tenant_id: "tnt_B" },
      ]);
      expect(visibleRows(cache, "contacts").map((row) => row.id)).toEqual([next === "tnt_B" ? "c-b" : "c-a"]);
      expect(visibleRows(cache, "contacts").every((row) => row.tenant_id === next)).toBe(true);
    }
  });
});

describe("manual app switch", () => {
  it("does not copy leads or opportunities or create a handoff", () => {
    const touch: VisibleApp = {
      app_id: "touch",
      display_name: "碰一碰",
      icon_ref: null,
      state: "launchable",
      launch_mode: "sso_launch",
      launch_target_id: "ti-touch-web",
      unavailable_reason: null,
    };
    const leads: VisibleApp = {
      app_id: "leads-engine",
      display_name: "获客",
      icon_ref: null,
      state: "current",
      launch_mode: "none",
      launch_target_id: null,
      unavailable_reason: null,
    };
    const records = { leads: ["lead-1"], opportunities: ["opp-1"] };
    const away = applyManualAppSwitch(records, touch, true, () => null);
    const back = applyManualAppSwitch(
      { leads: away.leads, opportunities: away.opportunities },
      leads,
      true,
    );
    expect(away.creates_handoff).toBe(false);
    expect(away.nav_intent).toBe("manual_switch");
    expect(away.createdHandoffs).toEqual([]);
    expect(back.leads).toEqual(["lead-1"]);
    expect(back.opportunities).toEqual(["opp-1"]);
    expect(back.createdHandoffs).toEqual([]);
    const plan = planManualSwitch(touch, true, () => "https://touch.example/in?contact_id=c1");
    expect(plan.href).toBeNull();
    expect(plan.creates_handoff).toBe(false);
    expect(plan.launch_target_id).toBe("ti-touch-web");
  });
});

describe("billing", () => {
  it("does not present a customer deal amount as platform balance", () => {
    const model = view(readFixture("ready-first-party.json"));
    const dealCents = 99_999_900;
    const text = billingBadgeText(model, dealCents);
    expect(text).not.toContain("999999");
    expect(text).not.toContain("999,999");
    expect(text).not.toContain("成交");
    const preview = billingBadgeText(provisionalEcoNav(), dealCents);
    expect(preview).toBe("额度需确认");
    expect(preview).not.toContain("999999");
  });
});

describe("white label", () => {
  it("shows only the brand's visible apps", () => {
    const raw = readFixture("ready-white-label.json") as { visible_apps: { app_id: string }[] };
    const model = view(raw);
    expect(model.brand?.kind).toBe("white_label");
    expect(model.apps.map((app) => app.app_id)).toEqual(raw.visible_apps.map((app) => app.app_id));
    expect(model.apps.map((app) => app.app_id)).not.toContain("touch");
    expect(model.apps.map((app) => app.app_id)).not.toContain("leads-engine");
    expect(switchableApps(model.apps, model.current_app.app_id).map((app) => app.app_id)).toEqual(["video-tool"]);
    expect(model.current_app.app_id).toBe(LEADS_APP_ID);
  });
});

describe("operator admin", () => {
  it("does not offer an implicit customer CRM entry without a business delegation", () => {
    const base = view(readFixture("ready-first-party.json"));
    const operator = {
      ...base,
      role_label: "operator admin",
      delegations: [],
      capabilities: { ...base.capabilities, can_switch_tenant: true },
    };
    expect(navbarCrmAffordances(operator)).toEqual({ tenantSwitcher: false, implicitCrmHrefs: [] });
    const delegated = {
      ...operator,
      delegations: [{ delegation_id: "dlg_1", type: "ops_collab" as const, expires_at: "2026-10-31T00:00:00Z" }],
    };
    expect(navbarCrmAffordances(delegated).implicitCrmHrefs).toEqual([]);
    expect(navbarCrmAffordances(delegated).tenantSwitcher).toBe(base.scopes.length > 1);
  });
});

describe("public surface", () => {
  it("does not render navigation for a hidden public page", () => {
    const model = view(readFixture("hidden-public-surface.json"));
    expect(model.renderable).toBe(false);
    expect(model.apps).toEqual([]);
  });
});

describe("tenant view", () => {
  it("drops the previous scope's billing when the tenant changes", () => {
    const model = view(readFixture("ready-first-party.json"));
    const other = model.scopes.find((scope) => scope.tenant_id !== model.active_tenant_id);
    expect(other).toBeTruthy();
    const next = selectTenant(model, other!.tenant_id);
    expect(next.active_tenant_id).toBe(other!.tenant_id);
    expect(next.return_context).toBeNull();
    expect(billingBadgeText(next)).toBe("额度需确认");
  });
});
