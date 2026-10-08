// CRM 查询范围与列表缓存。navbar 切租户和页面上的租户选择走同一份状态。
// 切走之后旧列表、筛选和详情必须为空，不能把上一租户的联系人/商机留在内存里。

export const CRM_TENANT_STORAGE_KEY = "leads_tenant_id";

export interface ScopedRow {
  id: string;
  tenant_id: string;
}

export interface CrmCache {
  tenant: string | null;
  epoch: number;
  contacts: ScopedRow[] | null;
  opportunities: ScopedRow[] | null;
  leads: ScopedRow[] | null;
  workbench: ScopedRow[] | null;
  filters: Record<string, string>;
  details: Record<string, unknown>;
}

export function emptyCrmCache(): CrmCache {
  return {
    tenant: null,
    epoch: 0,
    contacts: null,
    opportunities: null,
    leads: null,
    workbench: null,
    filters: {},
    details: {},
  };
}

export function switchCrmTenant(cache: CrmCache, nextTenantId: string): CrmCache {
  const id = nextTenantId.trim();
  if (!id || id === cache.tenant) return cache;
  return {
    tenant: id,
    epoch: cache.epoch + 1,
    contacts: null,
    opportunities: null,
    leads: null,
    workbench: null,
    filters: {},
    details: {},
  };
}

export function rememberRows(cache: CrmCache, kind: "contacts" | "opportunities" | "leads" | "workbench", rows: ScopedRow[]): CrmCache {
  if (!cache.tenant) return cache;
  const scoped = rows.filter((row) => row.tenant_id === cache.tenant);
  return { ...cache, [kind]: scoped };
}

export function visibleRows(cache: CrmCache, kind: "contacts" | "opportunities" | "leads" | "workbench"): ScopedRow[] {
  const rows = cache[kind];
  if (!cache.tenant || rows === null) return [];
  return rows.filter((row) => row.tenant_id === cache.tenant);
}
