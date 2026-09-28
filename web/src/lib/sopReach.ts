// HUI-1689 跟进页的展示规则。服务端决定能不能发。
// 余额、分数和自称已接通的渠道都不能把未送达写成已送达。

export interface SOPCapability {
  level?: string;
  unattended?: boolean;
  explicit_unattended?: boolean;
  verification?: string;
  live_channels?: string[];
  balance_cents?: number;
  ai_score?: number;
  note?: string;
  global_stop?: boolean;
}

export interface SOPView {
  verified: boolean;
  liveChannels: string[];
  unattended: boolean;
  note: string;
}

const HONEST_NOTE = "没有真实短信、邮件或企微渠道。人工确认后停在待发送或未送达，不会标成已送达。";

export function unattendedOpen(cap: SOPCapability | null | undefined): boolean {
  return cap?.level === "unattended" && cap.explicit_unattended === true;
}

export function presentSOP(cap: SOPCapability | null | undefined): SOPView {
  return {
    verified: false,
    liveChannels: [],
    unattended: unattendedOpen(cap),
    note: HONEST_NOTE,
  };
}

export function recordLabel(kind: string, status: string): string {
  if (kind === "internal_reminder") return "内部提醒";
  if (kind === "pending_draft") return "待发送草稿";
  if (kind === "channel_submission" && status === "pending_send") return "待发送";
  if (kind === "channel_submission" && status === "blocked") return "已拦截";
  if (kind === "channel_delivery") return "未送达";
  if (kind === "user_reply") return "用户回复";
  if (status === "pending_send") return "待发送";
  if (status === "blocked") return "已拦截";
  return "未送达";
}
