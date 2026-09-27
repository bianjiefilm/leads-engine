// EcoNavModel eco-nav/v1 — TypeScript view of the frozen contract (HUI-2217).
//
// Copy this file verbatim into a consumer (e.g. GoBoost HUI-2218). It has no
// runtime dependencies. The authoritative rules are README.md +
// eco-nav-model.schema.json + internal/econavcontract; the const arrays below
// are kept identical to the Go vocabulary by internal/econavcontract/drift_test.go.
//
// Renderer discipline:
//   - Render EcoTopNav only when parsing succeeded AND isRenderable(state).
//     Any unknown version/field/enum value means: render no nav (fail-closed).
//   - Never derive permissions, payer or the app list client-side; use
//     `capabilities`, `billing` and `visible_apps` exactly as served.
//   - An app switch sends (app_id, launch_target_id) to your own BFF, which
//     calls the shell launch route. Never build or hard-code an app URL, and
//     never attach order/campaign/project data: that is task handoff
//     (Tool Guidance, HUI-2196), not navigation.
//   - Public visitor pages (campaign pages, anonymous forms, public shares)
//     neither request nor render EcoTopNav.

export const ECO_NAV_MODEL_VERSION = "eco-nav/v1" as const;
export const ECO_NAV_EVENT_VERSION = "eco-nav-event/v1" as const;

export const NAV_STATES = ["ready", "degraded", "selection_required", "hidden", "denied", "unavailable"] as const;
export type NavState = (typeof NAV_STATES)[number];

export const HIDDEN_REASONS = ["public_surface", "brand_suspended", "brand_unknown"] as const;
export const DENIED_REASONS = ["unauthenticated", "membership_denied"] as const;
export const UNAVAILABLE_REASONS = ["authority_unavailable"] as const;
export const SELECTION_REASONS = ["tenant_selection_required"] as const;
export const DEGRADED_REASONS = ["billing_unconfigured", "billing_account_unknown", "billing_unavailable", "entitlement_unavailable"] as const;
export type StatusReason =
  | (typeof HIDDEN_REASONS)[number]
  | (typeof DENIED_REASONS)[number]
  | (typeof UNAVAILABLE_REASONS)[number]
  | (typeof SELECTION_REASONS)[number]
  | (typeof DEGRADED_REASONS)[number];

export const BRAND_KINDS = ["first_party", "white_label"] as const;
export const BRAND_STATUSES = ["active"] as const;
export const APP_STATES = ["current", "launchable", "entitlement_required", "temporarily_unavailable"] as const;
export const APP_UNAVAILABLE_REASONS = ["entitlement_missing", "entitlement_unavailable"] as const;
export const LAUNCH_MODES = ["sso_launch", "none"] as const;
export const SCOPE_STATES = ["resolved", "selection_required"] as const;
export const SEAT_SOURCES = ["membership", "delegation"] as const;
export const DELEGATION_TYPES = ["maker_service", "ops_collab"] as const;
export const BILLING_KINDS = ["wallet", "customer_quota"] as const;
export const BILLING_REASONS = ["unconfigured", "account_unknown", "unavailable"] as const;
export const PAYER_SOURCES = ["personal", "delegation"] as const;
export const AVAILABILITIES = ["available", "exhausted", "unknown"] as const;
export const AMOUNT_UNITS = ["cny_fen", "quota_credit"] as const;
export const BILLING_ENTRIES = ["billing_center", "brand_quota_page"] as const;
export const ACCOUNT_ROLES = ["owner", "payer", "viewer", "none"] as const;

export const NAV_INTENTS = ["manual_switch", "task_handoff"] as const;
export type NavIntent = (typeof NAV_INTENTS)[number];

export const NAV_EVENTS = [
  "eco_nav_open",
  "eco_nav_app_switch_attempt",
  "eco_nav_app_switch_success",
  "eco_nav_app_switch_denied",
  "eco_nav_tenant_switch",
  "eco_nav_return_source",
  "eco_nav_overflow_used",
] as const;
export const SWITCH_DENIAL_REASONS = [
  "app_not_enabled",
  "membership_denied",
  "entitlement_missing",
  "entitlement_unavailable",
  "rate_limited",
  "unauthenticated",
  "authority_unavailable",
] as const;

// ---- model ------------------------------------------------------------------

export interface Brand {
  brand_id: string;
  kind: (typeof BRAND_KINDS)[number];
  status: (typeof BRAND_STATUSES)[number];
  config_version: string;
  display_name: string;
  logo: { url: string; alt: string } | null;
  theme: { token_set_ref: string; accent: string | null };
  support: { label: string; url: string | null } | null;
}

export interface CurrentApp {
  app_id: string;
  display_name: string;
  icon_ref: string | null;
  home_target_id: string;
}

export type VisibleApp = {
  app_id: string;
  display_name: string;
  icon_ref: string | null;
} & (
  | { state: "current"; launch_mode: "none"; launch_target_id: null; unavailable_reason: null }
  | { state: "launchable"; launch_mode: "sso_launch"; launch_target_id: string; unavailable_reason: null }
  | { state: "entitlement_required"; launch_mode: "none"; launch_target_id: null; unavailable_reason: "entitlement_missing" }
  | { state: "temporarily_unavailable"; launch_mode: "none"; launch_target_id: null; unavailable_reason: "entitlement_unavailable" }
);

export interface Scope {
  tenant_id: string;
  display_name: string | null;
  source: (typeof SEAT_SOURCES)[number];
}

export interface WorkContext {
  state: (typeof SCOPE_STATES)[number];
  current_scope: Scope | null;
  switchable_scopes: Scope[];
  restored: boolean;
}

export interface Role {
  /** Identity-stored role label: display only, never a permission input. */
  label: string;
  source: (typeof SEAT_SOURCES)[number];
  delegations: { delegation_id: string; type: (typeof DELEGATION_TYPES)[number]; expires_at: string }[];
}

export interface Billing {
  kind: (typeof BILLING_KINDS)[number];
  known: boolean;
  reason: (typeof BILLING_REASONS)[number] | null;
  payer_source: (typeof PAYER_SOURCES)[number] | null;
  viewer_account_role: (typeof ACCOUNT_ROLES)[number] | null;
  availability: (typeof AVAILABILITIES)[number];
  /** Present only for a viewer holding owner/payer on the account. */
  amount: { value_minor: number; unit: (typeof AMOUNT_UNITS)[number] } | null;
  entry: (typeof BILLING_ENTRIES)[number] | null;
}

export interface ReturnContext {
  source_app_id: string;
  return_target_id: string;
  label: string;
  source_ref: string | null;
}

export interface Capabilities {
  can_switch_app: boolean;
  can_switch_tenant: boolean;
  can_manage_members: boolean;
  can_open_billing: boolean;
}

interface ModelBase {
  schema_version: typeof ECO_NAV_MODEL_VERSION;
  capabilities: Capabilities;
}

/** hidden / denied / unavailable: no segments at all — render nothing. */
export interface BlockedModel extends ModelBase {
  status: { state: "hidden" | "denied" | "unavailable"; reasons: StatusReason[] };
  brand: null;
  app: null;
  visible_apps: [];
  work_context: null;
  role: null;
  billing: null;
  return_context: null;
}

/** Multi-tenant principal without a valid scope: tenant picker only. */
export interface SelectionModel extends ModelBase {
  status: { state: "selection_required"; reasons: ["tenant_selection_required"] };
  brand: Brand;
  app: CurrentApp;
  visible_apps: VisibleApp[];
  work_context: WorkContext;
  role: null;
  billing: null;
  return_context: null;
}

export interface ResolvedModel extends ModelBase {
  status: { state: "ready" | "degraded"; reasons: StatusReason[] };
  brand: Brand;
  app: CurrentApp;
  visible_apps: VisibleApp[];
  work_context: WorkContext;
  role: Role;
  billing: Billing;
  return_context: ReturnContext | null;
}

export type EcoNavModelV1 = BlockedModel | SelectionModel | ResolvedModel;

export function isRenderable(model: EcoNavModelV1): model is SelectionModel | ResolvedModel {
  return model.status.state === "ready" || model.status.state === "degraded" || model.status.state === "selection_required";
}

// ---- telemetry -----------------------------------------------------------------

export interface EcoNavEventV1 {
  schema_version: typeof ECO_NAV_EVENT_VERSION;
  event: (typeof NAV_EVENTS)[number];
  /** EcoTopNav only ever emits manual_switch; task_handoff is Tool Guidance's. */
  intent: "manual_switch";
  brand_id: string;
  app_id: string;
  target_app_id: string | null;
  tenant_id: string | null;
  reason: (typeof SWITCH_DENIAL_REASONS)[number] | null;
  occurred_at: string;
}
