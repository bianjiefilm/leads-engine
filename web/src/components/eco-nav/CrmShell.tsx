"use client";

import { useEffect, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { shouldMountEcoTopNav } from "@/lib/eco-nav/mount";
import { commitCrmTenant, hydrateCrmTenantFromStorage } from "@/lib/eco-nav/use-crm-scope";

export function CrmShell({ model, children }: { model: EcoNavModel; children: ReactNode }) {
  const [authenticated, setAuthenticated] = useState(false);
  const [nickname, setNickname] = useState("账户");

  useEffect(() => {
    hydrateCrmTenantFromStorage();
    fetch("/api/whoami")
      .then(async (res) => {
        if (!res.ok) {
          setAuthenticated(false);
          return;
        }
        const body = await res.json();
        setAuthenticated(true);
        setNickname(typeof body.email === "string" && body.email ? body.email : "账户");
        if (typeof body.tenant_id === "string" && body.tenant_id && !window.localStorage.getItem("leads_tenant_id")) {
          commitCrmTenant(body.tenant_id);
        }
      })
      .catch(() => setAuthenticated(false));
  }, []);

  const show = shouldMountEcoTopNav({ surface: "crm", authenticated });
  return (
    <>
      {show ? <EcoTopNav model={model} nickname={nickname} onTenantSwitch={commitCrmTenant} /> : null}
      {children}
    </>
  );
}
