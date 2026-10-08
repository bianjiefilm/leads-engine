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

export interface OutboundTaskShape {
  simulation?: boolean;
  dial_succeeded?: boolean;
  real_connected?: boolean;
  cost?: string;
}

// HUI-2626 fix2（gate-r2 #7 partial 场景）：部分成功/部分失败是独立于成功/失败的
// 一态——拨打成功但未接通、或有任一回执未证实即「部分完成」，如实说，不写成已送达。
export function outboundResultLine(task: OutboundTaskShape | null): string {
  if (!task) return "还没有任务。";
  const dial = task.dial_succeeded === true ? "拨打成功：是" : task.dial_succeeded === false ? "拨打成功：否" : "没有拨打回执";
  const connected = task.real_connected === true ? "真实接通：是" : task.real_connected === false ? "真实接通：否" : "没有接通回执";
  const cost = task.cost === "unknown" || !task.cost ? "未知" : task.cost;
  const partial = task.dial_succeeded !== task.real_connected;
  const head = partial ? "部分完成：" : task.real_connected === true ? "已完成：" : "未完成：";
  return `${head}模拟：${task.simulation ? "是" : "否"}。${dial}。${connected}。费用：${cost}。`;
}
