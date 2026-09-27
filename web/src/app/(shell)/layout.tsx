import type { ReactNode } from "react";
import { CrmShell } from "@/components/eco-nav/CrmShell";
import { loadEcoNavModel } from "@/lib/eco-nav/load";

/** 认证后的 CRM 壳。公开表单 /f 不经过这里，因此不挂 EcoTopNav。 */
export default async function ShellLayout({ children }: Readonly<{ children: ReactNode }>) {
  const endpoint = process.env.PUBLIC_AI_ECO_NAV_URL?.trim() || null;
  const model = await loadEcoNavModel({
    endpoint,
    identityEnabled: process.env.PUBLIC_AI_ECO_NAV_IDENTITY === "1",
  });
  return <CrmShell model={model}>{children}</CrmShell>;
}
