// HUI-2626 finish-r1：列表 + 抽屉共用的同义层。口径统一、不发明新语义。
// money 已由 lib/workbench centsText 统一；这里补 consent/status/source 词汇与抽屉朝向。

export const CONSENT_TEXT: Record<string, string> = {
  pending: "待确认",
  granted: "已同意",
  denied: "已拒绝",
};

// 档案级粗粒度授权的同义词。未知值原样透出（保留服务端真实含义），空值明确写「未记录」。
export function consentText(status: string | null | undefined): string {
  const raw = (status ?? "").trim();
  if (!raw) return "未记录";
  return CONSENT_TEXT[raw] ?? raw;
}

export const LEAD_STATUS_TEXT: Record<string, string> = {
  new: "新线索",
  in_progress: "跟进中",
  converted: "已转化",
  closed: "已关闭",
  filtered: "已过滤",
};

export const LEAD_SOURCE_TEXT: Record<string, string> = {
  manual: "手工录入",
  form: "自有表单",
  touch_campaign: "碰一碰",
};

export function leadStatusText(status: string | null | undefined): string {
  const raw = (status ?? "").trim();
  if (!raw) return "未记录";
  return LEAD_STATUS_TEXT[raw] ?? raw;
}

export function leadSourceText(source: string | null | undefined): string {
  const raw = (source ?? "").trim();
  if (!raw) return "未知来源";
  return LEAD_SOURCE_TEXT[raw] ?? raw;
}

// 抽屉朝向：桌面（≥760px）右侧抽屉，窄屏底部详情层（票面 A 组：移动端适合窄屏的详情层）。
export function drawerSide(width: number): "right" | "bottom" {
  return width >= 760 ? "right" : "bottom";
}
