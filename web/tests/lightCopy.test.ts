import { describe, expect, it } from "vitest";
import { draftStatus, presentLightCopy } from "../src/lib/lightCopy";

describe("light copy display", () => {
  it("does not treat a claimed model or a finished generation as sent", () => {
    const view = presentLightCopy({
      model_ready: true,
      live_charge: 12,
      note: "已用模板生成并发送",
    });
    expect(view.modelReady).toBe(false);
    expect(view.liveCharge).toBe(0);
    expect(view.note).toContain("不会用模板占位");
    expect(view.note).toContain("已取得营销许可");
  });

  it("keeps a confirmed draft unsent and unpublished", () => {
    const status = draftStatus({
      user_confirmed: true,
      sent: true,
      published: true,
      marketing_permitted: true,
      generated: true,
    });
    expect(status.label).toBe("已确认，未发送");
    expect(status.sent).toBe(false);
    expect(status.published).toBe(false);
    expect(status.marketingPermitted).toBe(false);
    expect(status.generated).toBe(false);
  });
});
