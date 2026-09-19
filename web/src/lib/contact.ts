// 客户档案域纯函数(HUI-1691 / FEAT-0192)。页面组件只做渲染;所有业务判断
// 的真源在 Go 服务端,这里的函数仅镜像服务端规则用于展示(按钮显隐/文案/搜索
// 输入守卫),服务端对每个动作重新鉴权。
//
// 语义红线(FEAT-0192):
//   - consent 撤销是持久标记:已撤销的营销授权显示「已撤销(不可恢复)」,
//     界面绝不提供或暗示「恢复授权」;
//   - 平台账号与 CRM 联系人是不同对象:界面不出现、不暗示「手机号自动注册平台账号」;
//   - 手机号不作为搜索/URL 参数:搜索只按姓名/标签,疑似手机号输入被前端拦截
//     (镜像服务端 400 phone_not_searchable),取档只按联系人 id。

export interface CallerView {
  role?: string;
  memberId?: string;
  enabled?: boolean;
}

export interface RecordView {
  assigned_member_id?: string;
}

// canEditContact 镜像服务端 authz.ActionUpdate:owner 本租户任意记录;
// sales/agent 仅自己名下;disabled 一律不可。最终裁决在服务端。
export function canEditContact(caller: CallerView, record: RecordView): boolean {
  if (!caller || caller.enabled === false) return false;
  if (caller.role === "owner") return true;
  if (caller.role === "sales" || caller.role === "agent") {
    return !!caller.memberId && caller.memberId === record.assigned_member_id;
  }
  return false;
}

// canDeleteContact / canExportContacts 镜像 owner 专属动作
// (authz.ActionDelete / ActionExport):sales/agent 一律不可。
export function canDeleteContact(caller: CallerView): boolean {
  return !!caller && caller.enabled !== false && caller.role === "owner";
}

export function canExportContacts(caller: CallerView): boolean {
  return canDeleteContact(caller);
}

// ---- consent 展示 ------------------------------------------------------------

export interface ConsentRow {
  id: string;
  source_submission_ref: string;
  source_channel: string;
  notice_version: string;
  purpose: string;
  marketing_allowed: boolean;
  revoked_at?: string;
  revoked_reason?: string;
  status: string; // 服务端派生:active | revoked
}

export interface ConsentSummary {
  total: number;
  marketing_active: number;
  revoked: number;
}

// consentStatusLabel:撤销行显示持久标记语义,绝不出现「可恢复」字样。
export function consentStatusLabel(row: ConsentRow): string {
  if (row.revoked_at || row.status === "revoked") return "已撤销(不可恢复)";
  if (row.marketing_allowed) return "允许营销";
  return "不允许营销";
}

export function marketingSummaryLine(s: ConsentSummary): string {
  return `共 ${s.total} 个来源授权,当前允许营销 ${s.marketing_active} 个,已撤销 ${s.revoked} 个`;
}

// ---- 搜索输入守卫(镜像服务端 URL 纪律) ------------------------------------------

// isPhoneLike:与 Go 侧 isPhoneLike 同规则 —— 7 位以上连续数字视为手机号形态。
export function isPhoneLike(v: string): boolean {
  let run = 0;
  for (const ch of v) {
    if (ch >= "0" && ch <= "9") {
      run += 1;
      if (run >= 7) return true;
    } else {
      run = 0;
    }
  }
  return false;
}

// searchGuard 校验列表搜索输入;返回错误文案或空串。手机号不进 URL。
export function searchGuard(name: string, tag: string): string {
  if (name.trim() && isPhoneLike(name)) return "不支持按手机号搜索:请用姓名或标签,查档请用联系人链接";
  if (tag.trim() && isPhoneLike(tag)) return "标签不能是手机号形态:请用业务标签搜索";
  return "";
}

// ---- 标签展示 ----------------------------------------------------------------

// parseTags 拆服务端归一化后的逗号分隔标签串。
export function parseTags(tags: string | null | undefined): string[] {
  if (!tags) return [];
  return tags
    .split(",")
    .map((t) => t.trim())
    .filter((t) => t.length > 0);
}

export const COARSE_CONSENT_LABELS: Record<string, string> = {
  pending: "待确认",
  granted: "已同意(档案级,粗粒度)",
  denied: "已拒绝(档案级,粗粒度)",
};

// followupLine 渲染一条时间线;作者只显示成员 id,全文仅有权读者可见(来自 API)。
export interface FollowupRow {
  id: string;
  member_id: string;
  note: string;
  created_at: string;
}
