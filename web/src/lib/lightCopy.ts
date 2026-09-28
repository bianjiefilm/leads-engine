// HUI-1682 当前页展示。服务端决定能不能生成、能不能把事实交出去。
// 页面不把生成完成显示成已发送、已发布或已取得营销许可。

export interface LightCapability {
  model_ready?: boolean;
  live_charge?: number;
  note?: string;
}

export interface LightDraftView {
  user_confirmed?: boolean;
  sent?: boolean;
  published?: boolean;
  marketing_permitted?: boolean;
  generated?: boolean;
}

export interface LightCopyView {
  modelReady: boolean;
  liveCharge: number;
  note: string;
}

export interface DraftStatus {
  label: string;
  sent: boolean;
  published: boolean;
  marketingPermitted: boolean;
  generated: boolean;
}

const HONEST_NOTE =
  "没有接入真实文案模型。生成会失败并说明原因，不会用模板占位。保存和手改不需要模型。生成完成不等于已发送、已发布或已取得营销许可。";

export function presentLightCopy(_cap: LightCapability | null | undefined): LightCopyView {
  return { modelReady: false, liveCharge: 0, note: HONEST_NOTE };
}

export function draftStatus(row: LightDraftView | null | undefined): DraftStatus {
  return {
    label: row?.user_confirmed ? "已确认，未发送" : "草稿，未发送",
    sent: false,
    published: false,
    marketingPermitted: false,
    generated: false,
  };
}
