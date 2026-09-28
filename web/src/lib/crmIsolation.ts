// Brand is a label on the current tenant. It is not a tenant id.
export function brandIsDisplayOnly(displayName: string): string {
  const name = displayName.trim() || "未命名";
  return `品牌「${name}」只用于来源和展示，不能代替租户。`;
}

// A CRM export file is fetched only after the caller confirms again.
export function exportConfirmHeader(): Record<string, string> {
  return { "x-export-confirm": "1" };
}
