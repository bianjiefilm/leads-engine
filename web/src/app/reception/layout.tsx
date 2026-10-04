import type { ReactNode } from "react";
import { CrmShell } from "@/components/eco-nav/CrmShell";
import { loadEcoNavModel } from "@/lib/eco-nav/load";

/** 接待台与 CRM 共用工作范围。公开访客页 /r 不走这里。 */
export default async function ReceptionLayout({ children }: Readonly<{ children: ReactNode }>) {
  const endpoint = process.env.PUBLIC_AI_ECO_NAV_URL?.trim() || null;
  const model = await loadEcoNavModel({
    endpoint,
    identityEnabled: process.env.PUBLIC_AI_ECO_NAV_IDENTITY === "1",
  });
  return <CrmShell model={model}>{children}</CrmShell>;
}
