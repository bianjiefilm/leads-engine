"use client";

import React, { useEffect, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { ShellWidthProvider, SurfaceState, WorkbenchChrome, useShellWidth } from "@/components/workbench/chrome";
import { OfflineReady } from "@/components/workbench/offlineReady";
import { CRM_TENANT_STORAGE_KEY } from "@/lib/eco-nav/crm-scope";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { shouldMountEcoTopNav } from "@/lib/eco-nav/mount";
import { commitCrmTenant, hydrateCrmTenantFromStorage, releaseCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { guardTenantCommit, rendererDensity, runShellWhoami, scopeChoices, type WorkScope } from "@/lib/productShell";
import { RendererProvider } from "@/vendor/painuo/react/v1/src/index";

function DeskFrame({
  scopes,
  children,
}: {
  scopes: WorkScope[];
  children: ReactNode;
}) {
  const width = useShellWidth();
  const scope = useCrmScope();
  return (
    // HUI-2626（DECISIONS.md D2-2）：壳级 renderer scope——工作台子树统一消费
    // painuo token（profile-scopes.json leads-web/work.light/light 合法组合），
    // 并为 Drawer/Dialog 等原语提供 portal root。attribution 页级试点 provider 保留不动。
    <RendererProvider
      profile="leads-web"
      surface="work.light"
      theme="light"
      // HUI-2626：≤430 走 renderer 原生 touch 密度（控件高度 48px），桌面 default。
      density={rendererDensity(width)}
    >
      <WorkbenchChrome
        width={width}
        scopes={scopes}
        activeId={scope.tenant}
        onSwitch={(tenant) => {
          const chosen = guardTenantCommit(scopes, tenant);
          if (chosen) commitCrmTenant(chosen);
        }}
      >
        {children}
      </WorkbenchChrome>
    </RendererProvider>
  );
}

export function CrmShell({ model, children }: { model: EcoNavModel; children: ReactNode }) {
  const [authenticated, setAuthenticated] = useState(false);
  const [nickname, setNickname] = useState("账户");
  const [scopeFault, setScopeFault] = useState("");
  const [switcher, setSwitcher] = useState<WorkScope[]>([]);

  useEffect(() => {
    hydrateCrmTenantFromStorage();
    setSwitcher([]);
    let cancelled = false;
    const stored = (window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "").trim() || null;
    void runShellWhoami({
      storedId: stored,
      membershipScopes: scopeChoices(model.scopes),
    }).then((result) => {
      if (cancelled) return;
      setAuthenticated(result.ok);
      setNickname(result.email || "账户");
      setSwitcher(result.switcher);
      if (!result.memoryTenantId) {
        setScopeFault(result.fault);
        releaseCrmTenant();
        return;
      }
      setScopeFault("");
      if (result.persist && result.storage) commitCrmTenant(result.storage);
    });
    return () => {
      cancelled = true;
    };
  }, [model]);

  const show = shouldMountEcoTopNav({ surface: "crm", authenticated });
  const allowed = new Set(switcher.map((item) => item.tenant_id));
  const navModel: EcoNavModel = { ...model, scopes: model.scopes.filter((item) => allowed.has(item.tenant_id)) };
  return (
    <ShellWidthProvider>
      {/* HUI-2626 fix2（gate-r2 #8）：断网导航兜底为应用内离线页。 */}
      <OfflineReady />
      <div className="desk-app" data-shell="sales">
        {show ? (
          <EcoTopNav
            model={navModel}
            nickname={nickname}
            onTenantSwitch={(tenant) => {
              const chosen = guardTenantCommit(switcher, tenant);
              if (chosen) commitCrmTenant(chosen);
            }}
          />
        ) : null}
        <DeskFrame scopes={switcher}>
          {scopeFault ? <SurfaceState kind="error" title="工作范围不可用" detail={scopeFault} /> : children}
        </DeskFrame>
      </div>
    </ShellWidthProvider>
  );
}
