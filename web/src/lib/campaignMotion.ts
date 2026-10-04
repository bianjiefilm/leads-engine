// HUI-2747 获客侧展示。页面只使用这里的固定说明，不采用服务端的完成态文案。
// 多渠道只显示渠道名。转化留在获客侧，不写回创作工程。

export const CAMPAIGN_MOTION_NOTICE = "只记下交给动效工程的活动事实。没有生成成片，也没有送出成片。";

const STATUS = "渠道名已记下。没有生成成片，也没有送出成片。";

export interface CampaignMotionInput {
  piece_generated?: boolean;
  piece_sent?: boolean;
  model_calls?: number;
  creative_project_writes?: number;
  notice?: string;
  channels?: unknown;
}

export interface CampaignMotionView {
  notice: string;
  status: string;
  pieceGenerated: boolean;
  pieceSent: boolean;
  modelCalls: number;
  creativeProjectWrites: number;
  channels: string[];
  conversionNote: string;
}

export function presentCampaignMotion(input?: CampaignMotionInput | null): CampaignMotionView {
  const channels: string[] = [];
  if (Array.isArray(input?.channels)) {
    for (const channel of input.channels) {
      if (typeof channel === "string" && channel.trim()) channels.push(channel.trim());
    }
  }
  return {
    notice: CAMPAIGN_MOTION_NOTICE,
    status: STATUS,
    pieceGenerated: false,
    pieceSent: false,
    modelCalls: 0,
    creativeProjectWrites: 0,
    channels,
    conversionNote: "转化留在获客侧，不写回创作工程。",
  };
}
