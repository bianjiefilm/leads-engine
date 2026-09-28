// HUI-1687 外呼页的展示规则。服务端决定能不能拨。
// 声称有线路、有费用或已接通，都不能把隔离演练写成真实通话。

export interface OutboundCapability {
  production_auto?: boolean;
  real_line?: boolean;
  verification?: string;
  cost?: string;
  live_charge?: number;
  dial_succeeded?: boolean;
  real_connected?: boolean;
  note?: string;
  global_stop?: boolean;
}

export interface OutboundView {
  productionAuto: boolean;
  realLine: boolean;
  cost: "unknown";
  simulation: boolean;
  note: string;
}

const HONEST_NOTE = "没有真实线路。生产自动外呼关闭。隔离记录是模拟，不是拨打成功，也不是真实接通。";

export function presentOutbound(_cap: OutboundCapability | null | undefined): OutboundView {
  return {
    productionAuto: false,
    realLine: false,
    cost: "unknown",
    simulation: true,
    note: HONEST_NOTE,
  };
}

export function receiptLabel(kind: string, status: string): string {
  if (status === "provider_ack") return "接口应答";
  if (kind === "dial_submission" && status === "simulated") return "模拟提交";
  if (kind === "ringing" && status === "simulated") return "模拟响铃";
  if (kind === "connected" && status === "simulated") return "模拟接通";
  if (kind === "call_completed" && status === "simulated") return "模拟通话完成";
  if (kind === "intent_suggestion") return "意向建议";
  if (kind === "connected") return "未证实接通";
  if (kind === "cancel") return "已取消";
  if (kind === "human_transfer") return "已转人工";
  if (status === "blocked") return "已拦截";
  return "未证实";
}
