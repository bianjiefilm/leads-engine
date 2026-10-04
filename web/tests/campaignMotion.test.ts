import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { presentCampaignMotion } from "../src/lib/campaignMotion";

const forbidden = ["已生成", "已送出", "已经生成", "已经送出"];

describe("campaign motion handoff display", () => {
  it("does not claim a finished piece was produced or sent", () => {
    const view = presentCampaignMotion({
      piece_generated: true,
      piece_sent: true,
      model_calls: 4,
      creative_project_writes: 3,
      notice: "已生成成片并已送出",
      channels: ["抖音", "视频号", "小红书"],
    });
    expect(view.pieceGenerated).toBe(false);
    expect(view.pieceSent).toBe(false);
    expect(view.modelCalls).toBe(0);
    expect(view.creativeProjectWrites).toBe(0);
    expect(view.channels).toEqual(["抖音", "视频号", "小红书"]);
    expect(view.conversionNote).toContain("不写回创作工程");
    for (const phrase of forbidden) {
      expect(view.notice).not.toContain(phrase);
      expect(view.status).not.toContain(phrase);
    }
    expect(view.notice).toContain("没有生成成片");
    expect(view.notice).toContain("没有送出成片");
  });

  it("keeps the page copy from claiming a finished piece", () => {
    const files = ["../src/lib/campaignMotion.ts", "../src/components/CampaignMotionPanel.tsx"];
    for (const file of files) {
      const src = readFileSync(new URL(file, import.meta.url), "utf8");
      for (const phrase of forbidden) {
        expect(src, file).not.toContain(phrase);
      }
    }
  });
});
