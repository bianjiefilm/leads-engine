// HUI-1688 接待页的展示规则。服务端是发送权和知识依据的唯一真源。

export function channelsDeliverable(replyEpoch: number, sessionEpoch: number, status: string): { text: boolean; audio: boolean } {
  const ok = replyEpoch === sessionEpoch && status === "approved";
  return { text: ok, audio: ok };
}

export function leadPurposeAllowed(purpose: string, allowContact: boolean): boolean {
  return allowContact === true && purpose === "sales_followup";
}

export function newVisitorKey(): string {
  const bytes = new Uint8Array(16);
  if (typeof crypto !== "undefined" && "getRandomValues" in crypto) {
    crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  }
  return "v" + [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export const RECEPTION_GREETING = "你好，这里是本店的自有站点接待。价格和订单以实时核对为准。";

export const MODE_TEXT: Record<string, string> = {
  ai: "AI 接待",
  assist: "人工确认后发送",
  human: "人工接管",
};

export const PENDING_TEXT: Record<string, string> = {
  clarify_or_handoff: "需要核对或转人工",
  model_unavailable: "模型暂不可用，人工可以继续",
  human_takeover: "人工接管中",
  awaiting_approval: "等待人工确认",
};

export const REPLY_STATUS_TEXT: Record<string, string> = {
  generated: "已生成，未发送",
  approved: "已批准，未发送",
  sent: "已发送",
  superseded: "已作废",
  blocked: "已拦截",
};

const STAFF_TOKEN = /^[A-Za-z0-9._:@-]{1,128}$/;

export interface LeadFields {
  purpose: string;
  allowContact: boolean;
  contactName: string;
  phone: string;
  noticeVersion: string;
}

export interface DeskCall {
  method: "POST";
  path: string;
  body: Record<string, unknown>;
}

export function replySendable(replyEpoch: number, sessionEpoch: number, status: string): boolean {
  return channelsDeliverable(replyEpoch, sessionEpoch, status).text;
}

export function replyApprovable(mode: string, replyEpoch: number, sessionEpoch: number, status: string): boolean {
  return mode === "assist" && replyEpoch === sessionEpoch && status === "generated";
}

export function leadSubmitAllowed(fields: LeadFields): boolean {
  return leadPurposeAllowed(fields.purpose, fields.allowContact)
    && fields.contactName.trim() !== ""
    && fields.phone.trim() !== ""
    && fields.noticeVersion.trim() !== ""
    && fields.noticeVersion.trim().length <= 64;
}

export function followUpSubmitAllowed(leadId: string | undefined, note: string): boolean {
  return Boolean(leadId && leadId.trim()) && note.trim() !== "";
}

export function newStaffToken(prefix: string): string {
  const bytes = new Uint8Array(8);
  if (typeof crypto !== "undefined" && "getRandomValues" in crypto) {
    crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  }
  const token = prefix + [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
  if (!STAFF_TOKEN.test(token)) {
    throw new Error("staff token is not acceptable");
  }
  return token;
}

function sessionPath(sessionId: string, suffix: string): string | null {
  if (!STAFF_TOKEN.test(sessionId)) return null;
  return `/api/reception/sessions/${sessionId}${suffix}`;
}

export function takeoverCall(sessionId: string, epoch: number): DeskCall | null {
  const path = sessionPath(sessionId, "/takeover");
  if (!path || !Number.isInteger(epoch) || epoch < 1) return null;
  return { method: "POST", path, body: { epoch } };
}

export function releaseCall(sessionId: string, epoch: number): DeskCall | null {
  const path = sessionPath(sessionId, "/release");
  if (!path || !Number.isInteger(epoch) || epoch < 1) return null;
  return { method: "POST", path, body: { epoch } };
}

export function closeCall(sessionId: string): DeskCall | null {
  const path = sessionPath(sessionId, "/close");
  if (!path) return null;
  return { method: "POST", path, body: {} };
}

export function humanReplyCall(sessionId: string, clientReplyId: string, body: string): DeskCall | null {
  const path = sessionPath(sessionId, "/replies");
  const text = body.trim();
  if (!path || !STAFF_TOKEN.test(clientReplyId) || text === "") return null;
  return { method: "POST", path, body: { client_reply_id: clientReplyId, body: text } };
}

export function sendCall(sessionId: string, replyId: string, receiptId: string): DeskCall | null {
  if (!STAFF_TOKEN.test(replyId) || !STAFF_TOKEN.test(receiptId)) return null;
  const path = sessionPath(sessionId, `/replies/${replyId}/send`);
  if (!path) return null;
  return { method: "POST", path, body: { receipt_id: receiptId } };
}

export function approveCall(sessionId: string, replyId: string): DeskCall | null {
  if (!STAFF_TOKEN.test(replyId)) return null;
  const path = sessionPath(sessionId, `/replies/${replyId}/approve`);
  if (!path) return null;
  return { method: "POST", path, body: {} };
}

export function leadCall(sessionId: string, fields: LeadFields): DeskCall | null {
  const path = sessionPath(sessionId, "/lead");
  if (!path || !leadSubmitAllowed(fields)) return null;
  return {
    method: "POST",
    path,
    body: {
      purpose: "sales_followup",
      allow_contact: true,
      contact_name: fields.contactName.trim(),
      phone: fields.phone.trim(),
      notice_version: fields.noticeVersion.trim(),
      marketing_allowed: false,
    },
  };
}

export function followUpCall(sessionId: string, leadId: string | undefined, note: string, next?: string): DeskCall | null {
  const path = sessionPath(sessionId, "/follow-up");
  const text = note.trim();
  if (!path || !followUpSubmitAllowed(leadId, text)) return null;
  const body: Record<string, unknown> = { note: text };
  if (next) body.next_follow_up_at = next;
  return { method: "POST", path, body };
}

// Empty input means the operator left the next time blank.
// Invalid input is null so the page refuses the request.
export function nextFollowUpRFC3339(value: string): string | undefined | null {
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const parsed = new Date(trimmed);
  if (Number.isNaN(parsed.getTime())) return null;
  return parsed.toISOString().replace(/\.\d{3}Z$/, "Z");
}
