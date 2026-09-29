// Brand is a label on the current tenant. It is not a tenant id.
export function brandIsDisplayOnly(displayName: string): string {
  const name = displayName.trim() || "未命名";
  return `品牌「${name}」只用于来源和展示，不能代替租户。`;
}

// A source tag, brand, and campaign stay on the current tenant.
export function sourceStaysOnTenant(sourceTag: string, brand: string, campaignId: string): string {
  const tag = sourceTag.trim() || "未填写";
  const name = brand.trim() || "未命名";
  const campaign = campaignId.trim() || "未填写";
  return `来源标签「${tag}」、品牌「${name}」和活动「${campaign}」只留在当前租户，不能改归属。`;
}

// The real Touch → Notify → Leads chain has not been verified.
export const WHITE_LABEL_CHAIN_UNVERIFIED =
  "Touch 到真实 Notify 再到 Leads 仍未验证。夹具接收器不是这条链的证据。";

// A CRM export file is fetched only after the caller confirms again.
export function exportConfirmHeader(): Record<string, string> {
  return { "x-export-confirm": "1" };
}
