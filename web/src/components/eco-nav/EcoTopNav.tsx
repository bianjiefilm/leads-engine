"use client";

import { useEffect, useState } from "react";
import { navbarCrmAffordances } from "@/lib/eco-nav/affordances";
import {
  billingBadgeText,
  partitionApps,
  pinLeadsApp,
  planManualSwitch,
  selectTenant,
  switchableApps,
  type EcoNavModel,
  type LaunchResolver,
  type VisibleApp,
} from "@/lib/eco-nav/model";
import styles from "./eco-top-nav.module.css";

const noopResolve: LaunchResolver = () => null;

function AppControl({ app, canSwitch }: { app: VisibleApp; canSwitch: boolean }) {
  const plan = planManualSwitch(app, canSwitch, noopResolve);
  return (
    <span
      className={styles.quiet}
      data-testid={`eco-app-${app.app_id}`}
      data-nav-intent="manual_switch"
      data-creates-handoff="false"
      data-launch-target-id={plan.launch_target_id ?? ""}
      title={plan.residual ?? ""}
    >
      {app.display_name}
    </span>
  );
}

export function EcoTopNav({
  model,
  nickname,
  onTenantSwitch,
}: {
  model: EcoNavModel;
  nickname: string;
  onTenantSwitch: (tenant: string) => void;
}) {
  const [nav, setNav] = useState(() => pinLeadsApp(model));
  const [menuOpen, setMenuOpen] = useState(false);

  useEffect(() => {
    setNav(pinLeadsApp(model));
    setMenuOpen(false);
  }, [model]);

  if (!nav.renderable || !nav.brand) return null;

  const affordances = navbarCrmAffordances(nav);
  const apps = switchableApps(nav.apps, nav.current_app.app_id);
  const parts = partitionApps(apps, "wide");
  const canSwitch = nav.capabilities.can_switch_app === true;
  const roleText = nav.provenance === "public_ai_context" ? nav.role_label || "角色需确认" : "角色需确认";

  return (
    <header className={styles.bar} aria-label="生态导航" data-testid="eco-top-nav" data-app-id={nav.current_app.app_id}>
      <div className={styles.brand}>
        <span className={styles.brandName} title={nav.brand.display_name}>
          {nav.brand.display_name}
        </span>
        <span className={styles.currentApp} data-testid="eco-current-app" data-app-id={nav.current_app.app_id} aria-current="page">
          {nav.current_app.display_name}
        </span>
      </div>
      <div className={styles.apps}>
        <div className={styles.pinned}>
          {parts.pinned.map((app) => (
            <AppControl key={app.app_id} app={app} canSwitch={canSwitch} />
          ))}
        </div>
        {parts.overflow.length > 0 ? (
          <div className={styles.popover}>
            <button type="button" className={styles.button} aria-expanded={menuOpen} onClick={() => setMenuOpen((open) => !open)}>
              应用
            </button>
            {menuOpen ? (
              <div className={`${styles.menu} ${styles.menuLeft}`} role="menu">
                {parts.overflow.map((app) => (
                  <AppControl key={app.app_id} app={app} canSwitch={canSwitch} />
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>
      <div className={styles.spacer} />
      <div className={styles.context}>
        {affordances.tenantSwitcher
          ? nav.scopes.map((item) => {
              const name = item.display_name ?? item.tenant_id;
              return (
                <button
                  key={item.tenant_id}
                  type="button"
                  className={item.tenant_id === nav.active_tenant_id ? `${styles.button} ${styles.pressed}` : styles.button}
                  data-testid={`eco-tenant-${item.tenant_id}`}
                  aria-pressed={item.tenant_id === nav.active_tenant_id}
                  onClick={() => {
                    const next = selectTenant(nav, item.tenant_id);
                    if (next.active_tenant_id === nav.active_tenant_id) return;
                    setNav(next);
                    if (next.active_tenant_id) onTenantSwitch(next.active_tenant_id);
                  }}
                >
                  <span className={styles.ellipsis}>{name}</span>
                </button>
              );
            })
          : null}
        <span data-testid="eco-billing">{billingBadgeText(nav)}</span>
        <span className={styles.badge} data-testid="eco-role">
          {roleText}
        </span>
      </div>
      <div className={styles.right}>
        <span className={styles.ellipsis}>{nickname}</span>
      </div>
      {affordances.implicitCrmHrefs.length === 0 ? <span hidden data-testid="eco-implicit-crm-entry" data-count="0" /> : null}
    </header>
  );
}
