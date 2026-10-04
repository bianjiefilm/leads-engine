"use client";

import { useEffect, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { ShellWidthProvider, SurfaceState, WorkbenchChrome, useShellWidth } from "@/components/workbench/chrome";
import { CRM_TENANT_STORAGE_KEY } from "@/lib/eco-nav/crm-scope";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { shouldMountEcoTopNav } from "@/lib/eco-nav/mount";
import { commitCrmTenant, hydrateCrmTenantFromStorage, releaseCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { guardTenantCommit, runShellWhoami, scopeChoices, type WorkScope } from "@/lib/productShell";

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
    <WorkbenchChrome
      width={width}
      scopes={scopes}
      activeId={scope.tenantId}
      onSwitch={(tenantId) => {
        const chosen = guardTenantCommit(scopes, tenantId);
        if (chosen) commitCrmTenant(chosen);
      }}
    >
      {children}
    </WorkbenchChrome>
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
      <div className="desk-app" data-shell="sales">
        {show ? (
          <EcoTopNav
            model={navModel}
            nickname={nickname}
            onTenantSwitch={(tenantId) => {
              const chosen = guardTenantCommit(switcher, tenantId);
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
