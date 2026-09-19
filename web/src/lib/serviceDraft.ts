// 服务需求草稿交接(HUI-1749)纯函数。页面组件只做渲染;所有业务判断的
// 真源在 Go 服务端(handoffsender),这里仅镜像服务端规则用于展示:
//   - 按钮显隐:只有 creative_service + 更新权限(负责人/owner)可见,
//     merchant_customer 一律 false(动作不存在,不是“置灰”);
//   - 缺失字段只标缺失,绝不从 CRM 备注猜测或代填;
//   - 投递成功 ≠ 成交:状态文案绝不出现「已成交」;
//   - 撤销只在接单侧未接受前可用;已接受引导走接单侧变更流程。
import type { CallerView, StageRecord } from "./opportunity";
import { canTransitionStage } from "./opportunity";

export interface ServiceDraftRecord extends StageRecord {
  business_category?: string;
}

// canCreateServiceDraft mirrors the server gates (category + authz.ActionUpdate):
// the action must not exist for merchant_customer, and disabled users never
// see it. The server re-validates every call (404 mask / 422).
export function canCreateServiceDraft(caller: CallerView, record: ServiceDraftRecord): boolean {
  if (record.business_category !== "creative_service") return false;
  return canTransitionStage(caller, record);
}

// ---- preview ------------------------------------------------------------------

export interface ServiceDraftAssetInput {
  asset_ref: string;
  sha256: string;
  size_bytes: number;
  media_type: string;
}

export interface ServiceDraftInput {
  confirm: boolean;
  summary: string;
  service_category: string;
  budget_cents: number | null;
  deadline: string;
  assets: ServiceDraftAssetInput[];
}

export interface ServiceDraftPreview {
  summary: string;
  service_category: string;
  budget_cents: number | null;
  deadline: string;
  assets: ServiceDraftAssetInput[];
  missing: string[];
  receiver: { target_app: string; return_target_id: string; scopes: string[] };
  note: string;
}

const MISSING_LABELS: Record<string, string> = {
  summary: "需求摘要",
  budget_cents: "预算",
  deadline: "截止日期",
  assets: "品牌/素材资产",
};

// missingText renders the 缺失 marks. Absent facts stay absent: nothing here
// ever proposes a value (缺失字段标缺失,不由 CRM 备注猜测).
export function missingText(missing: string[]): string {
  if (missing.length === 0) return "无缺失";
  return "缺失:" + missing.map((m) => MISSING_LABELS[m] ?? m).join("、");
}

// ---- restricted handoff projection ----------------------------------------------

export interface ServiceDraftHandoff {
  handoff_id: string;
  source_version: number;
  local_status: "confirmed" | "delivered" | "delivery_failed" | "revoked";
  draft_ref: string;
  target_app: string;
  target_status: string;
  target_dirty: boolean;
  target_revoked: boolean;
  confirmed_at: string;
  delivered_at?: string | null;
  revoked_at?: string | null;
  note: string;
}

// handoffStatusLine renders one guarded status line for the detail page.
// 守卫红线:任何分支都不出现「已成交」;投递成功只表述为“接单侧已建立草稿”。
export function handoffStatusLine(h: ServiceDraftHandoff): string {
  const ref = h.draft_ref ? `草稿引用 ${h.draft_ref}` : "草稿引用待接收端返回";
  switch (h.local_status) {
    case "delivered":
      return `接单侧已建立服务需求草稿(${ref})— 这是交接事实投影,不代表成交/已收款,商机状态以 CRM 为准。`;
    case "delivery_failed":
      return `交接快照已保存(${ref});投递暂未完成,可重试,确认事实不丢失。`;
    case "revoked":
      return `该交接已撤销(${ref});如需继续请重新确认(生成新版本新引用)。`;
    default:
      return `交接快照已确认(${ref}),待投递。`;
  }
}

export interface ServiceDraftActions {
  canRetry: boolean;
  canRevoke: boolean;
  canRefresh: boolean;
}

// serviceDraftActions mirrors the server guards:
//   - retry: any live (non-revoked) snapshot;
//   - revoke: only while the target is still an unaccepted draft (or silent);
//     after acceptance the target product's change flow owns the record;
//   - refresh: live snapshots only (a revoked one has nothing to mirror).
export function serviceDraftActions(h: ServiceDraftHandoff): ServiceDraftActions {
  if (h.local_status === "revoked") {
    return { canRetry: false, canRevoke: false, canRefresh: false };
  }
  const accepted = h.target_status !== "" && h.target_status !== "draft";
  return {
    canRetry: true,
    canRevoke: !accepted,
    canRefresh: true,
  };
}

export const REVOKE_ACCEPTED_COPY =
  "接单侧已接受该需求:后续变更请走接单应用的需求变更流程,此处不覆盖已确认内容。";
