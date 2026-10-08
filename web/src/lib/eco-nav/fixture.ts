import { parseEcoNavDocument, pinLeadsApp, toViewModel, PROVISIONAL_STATUS_SUMMARY, type EcoNavModel } from "@/lib/eco-nav/model";

/**
 * 本地预览文档，必须能被 eco-nav/v1 解析。
 * 不是 public-ai Identity。启动目标只写 launch_target_id，不写域名。
 */

const APP_IDS = [
  ["touch", "碰一碰", "ti-touch-web"],
  ["orders", "接单", "ti-orders-web"],
] as const;

function launchable(appId: string, displayName: string, targetId: string) {
  return {
    app_id: appId,
    display_name: displayName,
    icon_ref: null,
    state: "launchable" as const,
    launch_mode: "sso_launch" as const,
    launch_target_id: targetId,
    unavailable_reason: null,
  };
}

function scope(tenant: string, displayName: string) {
  return { tenant_id: tenant, display_name: displayName, source: "membership" as const };
}

export const PROVISIONAL_DOCUMENT = {
  schema_version: "eco-nav/v1",
  status: { state: "degraded" as const, reasons: ["billing_unavailable"] },
  brand: {
    brand_id: "brand-huigoo",
    kind: "first_party" as const,
    status: "active" as const,
    config_version: "0",
    display_name: "数海",
    logo: null,
    theme: { token_set_ref: "default", accent: null },
    support: null,
  },
  app: {
    app_id: "leads-engine",
    display_name: "获客",
    icon_ref: null,
    home_target_id: "ti-leads-engine-web",
  },
  visible_apps: [
    {
      app_id: "leads-engine",
      display_name: "获客",
      icon_ref: null,
      state: "current" as const,
      launch_mode: "none" as const,
      launch_target_id: null,
      unavailable_reason: null,
    },
    ...APP_IDS.map(([appId, displayName, targetId]) => launchable(appId, displayName, targetId)),
  ],
  work_context: {
    state: "resolved" as const,
    current_scope: scope("tenant-a", "客户甲"),
    switchable_scopes: [scope("tenant-a", "客户甲"), scope("tenant-b", "客户乙")],
    restored: false,
  },
  role: { label: "成员", source: "membership" as const, delegations: [] },
  billing: {
    kind: "wallet" as const,
    known: false,
    reason: "unavailable" as const,
    payer_source: "personal" as const,
    viewer_account_role: null,
    availability: "unknown" as const,
    amount: null,
    entry: null,
  },
  return_context: null,
  capabilities: {
    can_switch_app: true,
    can_switch_tenant: true,
    can_manage_members: false,
    can_open_billing: false,
  },
};

export function provisionalEcoNav(summary = PROVISIONAL_STATUS_SUMMARY): EcoNavModel {
  const parsed = parseEcoNavDocument(PROVISIONAL_DOCUMENT);
  if (!parsed.ok) throw new Error("provisional eco-nav document is not eco-nav/v1");
  return pinLeadsApp(toViewModel(parsed.document, { provenance: "provisional_fixture", statusSummary: summary }));
}
