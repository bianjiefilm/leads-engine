// 销售工作台外壳。分组和金额仍以服务端为准，这里只规定正式页面怎么呈现。
// 租户只能从工作范围里选，不能手填。A/B 切换后另一侧的行不能留在屏幕上。

export interface WorkScope {
  tenant_id: string;
  display_name: string;
}

export interface ShellStructure {
  density: "compact" | "regular" | "dense";
  navigation: "bar" | "rail";
  records: "cards" | "grid";
  factLayout: "chips" | "columns";
  columns: 1 | 2 | 4;
  primaryCount: 1;
}

export const SHELL_NAV = [
  { href: "/", label: "今天", id: "today" },
  { href: "/leads", label: "线索", id: "leads" },
  { href: "/contacts", label: "客户", id: "contacts" },
  { href: "/reception", label: "接待", id: "reception" },
  { href: "/opportunities", label: "商机", id: "opportunities" },
  { href: "/sop", label: "跟进提醒", id: "sop" },
  { href: "/outbound", label: "外呼", id: "outbound" },
  { href: "/attribution", label: "来源与费用", id: "attribution" },
  { href: "/subscription", label: "订阅与用量", id: "billing" },
] as const;

export const MISSING_SCOPE = "先在顶部选择工作范围。";

const PAGE_PRIMARY: Record<string, string> = {
  today: "记跟进",
  leads: "打开线索",
  contacts: "创建档案",
  "lead-detail": "记跟进",
  "contact-detail": "保存修改",
  reception: "继续接待",
  opportunities: "打开商机",
  "opportunity-detail": "推进阶段",
  sop: "记下内部提醒",
  outbound: "记录模拟提交",
  attribution: "复算",
  billing: "查看用量",
  isolation: "申请导出",
};

export function pagePrimary(page: string): string {
  return PAGE_PRIMARY[page] ?? "继续";
}

export function scopeChoices(scopes: Array<{ tenant_id: string; display_name?: string | null }>): WorkScope[] {
  const out: WorkScope[] = [];
  for (const scope of scopes) {
    const id = scope.tenant_id.trim();
    if (!id || out.some((item) => item.tenant_id === id)) continue;
    const name = (scope.display_name ?? "").trim();
    out.push({ tenant_id: id, display_name: name || "未命名工作范围" });
  }
  return out;
}

// 登录租户不在导航范围里时，只追加这一个受控项，不开放手填。
export function withSessionScope(scopes: WorkScope[], sessionTenantId: string | null): WorkScope[] {
  const id = (sessionTenantId ?? "").trim();
  if (!id || scopes.some((scope) => scope.tenant_id === id)) return scopes;
  return [...scopes, { tenant_id: id, display_name: "当前登录范围" }];
}

export function chooseScope(scopes: WorkScope[], tenant: string): WorkScope | null {
  const id = tenant.trim();
  if (!id) return null;
  return scopes.find((scope) => scope.tenant_id === id) ?? null;
}

export function reconcileTenant(scopes: WorkScope[], storedId: string | null, sessionTenantId: string | null): string | null {
  const stored = chooseScope(scopes, storedId ?? "");
  if (stored) return stored.tenant_id;
  const session = chooseScope(scopes, sessionTenantId ?? "");
  if (session) return session.tenant_id;
  return null;
}

export interface WorkTenantSettlement {
  tenant: string | null;
  persist: boolean;
}

// whoami 失败时，预览夹具不是允许集合。存储和登录租户都不在允许集合里时返回空，不落到第一项。
export function settleWorkTenant(input: {
  membershipScopes: WorkScope[];
  whoamiOk: boolean;
  sessionTenant: string | null;
  storedId: string | null;
}): WorkTenantSettlement {
  if (!input.whoamiOk) return { tenant: null, persist: false };
  const allowed = withSessionScope(input.membershipScopes, input.sessionTenant);
  const tenant = reconcileTenant(allowed, input.storedId, input.sessionTenant);
  const stored = (input.storedId ?? "").trim();
  return { tenant, persist: Boolean(tenant) && tenant !== stored };
}

export function applyWorkTenant(
  memoryTenantId: string | null,
  storedId: string | null,
  settlement: WorkTenantSettlement,
): { memoryTenantId: string | null; storage: string | null } {
  if (!settlement.tenant) return { memoryTenantId: null, storage: storedId };
  if (!settlement.persist) return { memoryTenantId: settlement.tenant || memoryTenantId, storage: storedId };
  return { memoryTenantId: settlement.tenant, storage: settlement.tenant };
}

export function listTenantHeader(tenant: string | null): { "x-tenant-id": string } | null {
  const id = (tenant ?? "").trim();
  if (!id) return null;
  return { "x-tenant-id": id };
}

export const SCOPE_UNAVAILABLE = "没有可用的工作范围。今天、线索和接待不会改去看别的客户。";

export interface ShellWhoamiBody {
  tenant_id?: unknown;
  email?: unknown;
}

export interface ShellWhoamiResult {
  ok: boolean;
  email: string | null;
  sessionTenant: string | null;
  memoryTenantId: string | null;
  storage: string | null;
  persist: boolean;
  switcher: WorkScope[];
  fault: string;
  requestedTenant: string | null;
}

async function browserWhoami(path: string, init: { headers: Record<string, string> }): Promise<{ ok: boolean; body: ShellWhoamiBody }> {
  const res = await fetch(path, { headers: init.headers });
  const body = (await res.json().catch(() => ({}))) as ShellWhoamiBody;
  return { ok: res.ok, body };
}

// 已有存储或登录租户时必须带上 x-tenant-id。空头会在进 whoami 之前被拒，登录租户就进不了允许集合。
export async function runShellWhoami(input: {
  storedId: string | null;
  membershipScopes: WorkScope[];
  fetchImpl?: (path: string, init: { headers: Record<string, string> }) => Promise<{ ok: boolean; body: ShellWhoamiBody }>;
}): Promise<ShellWhoamiResult> {
  const header = listTenantHeader(input.storedId);
  const headers: Record<string, string> = header ? { ...header } : {};
  let ok = false;
  let session: string | null = null;
  let email: string | null = null;
  try {
    const res = await (input.fetchImpl ?? browserWhoami)("/api/whoami", { headers });
    ok = res.ok === true;
    if (ok && typeof res.body?.tenant_id === "string" && res.body.tenant_id.trim()) session = res.body.tenant_id.trim();
    if (ok && typeof res.body?.email === "string" && res.body.email.trim()) email = res.body.email.trim();
  } catch {
    ok = false;
  }
  const settlement = settleWorkTenant({
    membershipScopes: input.membershipScopes,
    whoamiOk: ok,
    sessionTenant: session,
    storedId: input.storedId,
  });
  const applied = applyWorkTenant(input.storedId, input.storedId, settlement);
  const confirmed = Boolean(applied.memoryTenantId);
  return {
    ok,
    email,
    sessionTenant: ok ? session : null,
    memoryTenantId: applied.memoryTenantId,
    storage: applied.storage,
    persist: settlement.persist,
    switcher: confirmed ? withSessionScope(input.membershipScopes, session) : [],
    fault: confirmed ? "" : SCOPE_UNAVAILABLE,
    requestedTenant: header?.["x-tenant-id"] ?? null,
  };
}

export function guardTenantCommit(switcher: WorkScope[], tenant: string): string | null {
  return chooseScope(switcher, tenant)?.tenant_id ?? null;
}

export function nextLeadTicket(previousSeq: number, tenant: string): { seq: number; tenant: string } {
  return { seq: previousSeq + 1, tenant };
}

export function acceptLeadRows<T>(
  active: { seq: number; tenant: string },
  ticket: { seq: number; tenant: string },
  rows: T,
): T | null {
  if (active.seq !== ticket.seq || active.tenant !== ticket.tenant) return null;
  return rows;
}

export function acceptDeskPayload<T>(
  active: { seq: number; tenant: string },
  arrivedSeq: number,
  arrivedTenant: string,
  payload: T,
): T | null {
  if (active.seq !== arrivedSeq || active.tenant !== arrivedTenant) return null;
  return payload;
}

export function receptionSessionFrame(
  selectedId: string,
  selectedTenant: string | null,
  tenant: string | null,
): { tenant: string; sessionId: string } | null {
  if (!selectedId || !tenant || selectedTenant !== tenant) return null;
  return { tenant: tenant, sessionId: selectedId };
}

export function droppedTenantRows<T extends { tenant_id?: string }>(tenant: string | null, rows: T[]): T[] {
  if (!tenant) return [];
  return rows.filter((row) => row.tenant_id === tenant);
}

export function shellStructure(width: number): ShellStructure {
  if (width <= 399) {
    return { density: "compact", navigation: "bar", records: "cards", factLayout: "chips", columns: 1, primaryCount: 1 };
  }
  if (width <= 430) {
    return { density: "compact", navigation: "bar", records: "cards", factLayout: "chips", columns: 2, primaryCount: 1 };
  }
  if (width >= 1440) {
    return { density: "dense", navigation: "rail", records: "grid", factLayout: "columns", columns: 4, primaryCount: 1 };
  }
  return { density: "regular", navigation: "bar", records: "cards", factLayout: "columns", columns: 2, primaryCount: 1 };
}

// HUI-2626：390/430 触控密度走 vendored 原生机制——RendererProvider density="touch"
// 把 --pn-r-height 换成 --pn-control-height-touch（48px），按钮/输入/分段/选择全部生效。
// 桌面维持 default。取值必须是 renderer 合法枚举（compact/default/touch 中的 touch/default）。
export function rendererDensity(width: number): "touch" | "default" {
  return width <= 430 ? "touch" : "default";
}

const TECHNICAL = /HTTP\s*\d*|^\s*[\[{]|ECONN|fetch failed|Unexpected token|SyntaxError|TypeError|Failed to fetch|NetworkError|status code|\bundefined\b/i;

export function productError(raw: unknown, fallback = "这一步没有完成，请稍后重试。"): string {
  const text = typeof raw === "string" ? raw.trim() : "";
  if (!text || TECHNICAL.test(text) || text.length > 280) return fallback;
  return text;
}

export function failureText(body: { message?: string; error?: string } | null | undefined, status?: number): string {
  return productError(body?.message || body?.error || (status ? `HTTP ${status}` : ""));
}

export type FactTone = "ai" | "human" | "automation";

export function factTone(input: { kind?: string; auto?: boolean }): FactTone {
  if (input.auto) return "automation";
  if (input.kind === "ai_draft" || input.kind === "model") return "ai";
  return "human";
}

export function toneLabel(tone: FactTone): string {
  if (tone === "ai") return "AI 建议";
  if (tone === "automation") return "自动化状态";
  return "人工事实";
}
