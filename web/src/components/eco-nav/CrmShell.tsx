"use client";

import { useEffect, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { ShellWidthProvider, SurfaceState, WorkbenchChrome, useShellWidth } from "@/components/workbench/chrome";
import { CRM_TENANT_STORAGE_KEY } from "@/lib/eco-nav/crm-scope";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { shouldMountEcoTopNav } from "@/lib/eco-nav/mount";
import { commitCrmTenant, hydrateCrmTenantFromStorage, releaseCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { applyWorkTenant, chooseScope, scopeChoices, settleWorkTenant, withSessionScope } from "@/lib/productShell";

function DeskFrame({
  model,
  sessionTenant,
  children,
}: {
  model: EcoNavModel;
  sessionTenant: string | null;
  children: ReactNode;
}) {
  const width = useShellWidth();
  const scope = useCrmScope();
  const choices = withSessionScope(scopeChoices(model.scopes), sessionTenant);
  return (
    <WorkbenchChrome
      width={width}
      scopes={choices}
      activeId={scope.tenantId}
      onSwitch={(tenantId) => {
        const chosen = chooseScope(choices, tenantId);
        if (chosen) commitCrmTenant(chosen.tenant_id);
      }}
    >
      {children}
    </WorkbenchChrome>
  );
}

export function CrmShell({ model, children }: { model: EcoNavModel; children: ReactNode }) {
  const [authenticated, setAuthenticated] = useState(false);
  const [nickname, setNickname] = useState("账户");
  const [sessionTenant, setSessionTenant] = useState<string | null>(null);
  const [scopeFault, setScopeFault] = useState("");

  useEffect(() => {
    hydrateCrmTenantFromStorage();
    let cancelled = false;
    const apply = (whoamiOk: boolean, session: string | null) => {
      const stored = (window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "").trim() || null;
      const settlement = settleWorkTenant({
        membershipScopes: scopeChoices(model.scopes),
        whoamiOk,
        sessionTenant: session,
        storedId: stored,
      });
      const applied = applyWorkTenant(stored, stored, settlement);
      if (!applied.memoryTenantId) {
        setScopeFault("没有可用的工作范围。今天、线索和接待不会改去看别的客户。");
        releaseCrmTenant();
        return;
      }
      setScopeFault("");
      if (applied.storage && applied.storage !== stored) commitCrmTenant(applied.storage);
    };
    fetch("/api/whoami")
      .then(async (res) => {
        if (cancelled) return;
        let session: string | null = null;
        if (res.ok) {
          const body = await res.json();
          setAuthenticated(true);
          setNickname(typeof body.email === "string" && body.email ? body.email : "账户");
          if (typeof body.tenant_id === "string" && body.tenant_id) session = body.tenant_id;
        } else {
          setAuthenticated(false);
        }
        if (session) setSessionTenant(session);
        apply(res.ok, session);
      })
      .catch(() => {
        if (cancelled) return;
        setAuthenticated(false);
        setSessionTenant(null);
        apply(false, null);
      });
    return () => {
      cancelled = true;
    };
  }, [model]);

  const show = shouldMountEcoTopNav({ surface: "crm", authenticated });
  return (
    <ShellWidthProvider>
      <div className="desk-app" data-shell="sales">
        {show ? <EcoTopNav model={model} nickname={nickname} onTenantSwitch={commitCrmTenant} /> : null}
        <DeskFrame model={model} sessionTenant={sessionTenant}>
          {scopeFault ? <SurfaceState kind="error" title="工作范围不可用" detail={scopeFault} /> : null}
          {children}
        </DeskFrame>
      </div>
    </ShellWidthProvider>
  );
}
