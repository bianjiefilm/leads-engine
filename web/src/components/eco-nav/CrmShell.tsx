"use client";

import { useEffect, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { ShellWidthProvider, WorkbenchChrome, useShellWidth } from "@/components/workbench/chrome";
import { CRM_TENANT_STORAGE_KEY } from "@/lib/eco-nav/crm-scope";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { shouldMountEcoTopNav } from "@/lib/eco-nav/mount";
import { commitCrmTenant, hydrateCrmTenantFromStorage, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { chooseScope, reconcileTenant, scopeChoices, withSessionScope } from "@/lib/productShell";

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

  useEffect(() => {
    hydrateCrmTenantFromStorage();
    let cancelled = false;
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
        const allowed = withSessionScope(scopeChoices(model.scopes), session);
        const stored = window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "";
        const next = reconcileTenant(allowed, stored.trim() || session, model.active_tenant_id);
        if (next && next !== stored.trim()) commitCrmTenant(next);
      })
      .catch(() => {
        if (cancelled) return;
        setAuthenticated(false);
        const allowed = scopeChoices(model.scopes);
        const stored = window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "";
        const next = reconcileTenant(allowed, stored.trim() || null, model.active_tenant_id);
        if (next && next !== stored.trim()) commitCrmTenant(next);
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
          {children}
        </DeskFrame>
      </div>
    </ShellWidthProvider>
  );
}
