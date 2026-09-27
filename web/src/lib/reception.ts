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
