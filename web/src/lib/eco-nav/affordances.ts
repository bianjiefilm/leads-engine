import type { Delegation, EcoNavModel } from "@/lib/eco-nav/model";

export function isOperatorAdminLabel(label: string): boolean {
  const normalized = label.trim().toLowerCase();
  return normalized === "operator" || normalized === "operator admin" || normalized === "ops admin" || normalized === "admin";
}

export function hasBusinessDelegation(delegations: Pick<Delegation, "type">[]): boolean {
  return delegations.some((item) => item.type === "ops_collab");
}

export interface NavbarCrmAffordances {
  tenantSwitcher: boolean;
  implicitCrmHrefs: string[];
}

// operator admin 没有业务委托时，顶栏不提供进入客户 CRM 的隐式入口。
// 即使有委托，也不另造深链；租户切换只来自模型里的 membership scope。
export function navbarCrmAffordances(model: EcoNavModel): NavbarCrmAffordances {
  const blocked = isOperatorAdminLabel(model.role_label) && !hasBusinessDelegation(model.delegations);
  if (blocked || !model.renderable) {
    return { tenantSwitcher: false, implicitCrmHrefs: [] };
  }
  return {
    tenantSwitcher: model.capabilities.can_switch_tenant === true && model.scopes.length > 1,
    implicitCrmHrefs: [],
  };
}
