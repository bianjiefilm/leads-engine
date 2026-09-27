import { planManualSwitch, type LaunchResolver, type VisibleApp } from "@/lib/eco-nav/model";

export interface CrmRecords {
  leads: string[];
  opportunities: string[];
}

export interface ManualSwitchEffect {
  leads: string[];
  opportunities: string[];
  createdHandoffs: string[];
  creates_handoff: false;
  nav_intent: "manual_switch";
}

// 手工切 App 只携带 app_id + launch_target_id。不复制线索/商机，不建 Handoff。
export function applyManualAppSwitch(
  records: CrmRecords,
  app: VisibleApp,
  canSwitch: boolean,
  resolve: LaunchResolver = () => null,
): ManualSwitchEffect {
  const plan = planManualSwitch(app, canSwitch, resolve);
  return {
    leads: records.leads.slice(),
    opportunities: records.opportunities.slice(),
    createdHandoffs: [],
    creates_handoff: plan.creates_handoff,
    nav_intent: plan.nav_intent,
  };
}
