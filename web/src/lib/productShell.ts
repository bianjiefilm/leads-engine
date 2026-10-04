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

export function chooseScope(scopes: WorkScope[], tenantId: string): WorkScope | null {
  const id = tenantId.trim();
  if (!id) return null;
  return scopes.find((scope) => scope.tenant_id === id) ?? null;
}

export function reconcileTenant(scopes: WorkScope[], storedId: string | null, activeId: string | null): string | null {
  const stored = chooseScope(scopes, storedId ?? "");
  if (stored) return stored.tenant_id;
  const active = chooseScope(scopes, activeId ?? "");
  if (active) return active.tenant_id;
  return scopes[0]?.tenant_id ?? null;
}

export function droppedTenantRows<T extends { tenant_id?: string }>(tenantId: string | null, rows: T[]): T[] {
  if (!tenantId) return [];
  return rows.filter((row) => row.tenant_id === tenantId);
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
