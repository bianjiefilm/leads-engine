import { describe, expect, it } from "vitest";
import { consentText, drawerSide, LEAD_SOURCE_TEXT, LEAD_STATUS_TEXT } from "@/lib/finish";
import { rendererDensity } from "@/lib/productShell";

// HUI-2626 finish-r1：列表/抽屉共用的同义层与抽屉朝向（DECISIONS.md D2-6/D2-3）。

describe("consentText", () => {
  it("maps coarse consent statuses to the shared wording", () => {
    expect(consentText("pending")).toBe("待确认");
    expect(consentText("granted")).toBe("已同意");
    expect(consentText("denied")).toBe("已拒绝");
  });

  it("keeps unknown values visible instead of inventing a label", () => {
    expect(consentText("")).toBe("未记录");
    expect(consentText("future_state")).toBe("future_state");
  });
});

describe("drawerSide", () => {
  it("uses a right drawer on desktop and a bottom sheet on narrow screens", () => {
    expect(drawerSide(1920)).toBe("right");
    expect(drawerSide(1440)).toBe("right");
    expect(drawerSide(760)).toBe("right");
    expect(drawerSide(430)).toBe("bottom");
    expect(drawerSide(390)).toBe("bottom");
  });
});

describe("lead vocabulary", () => {
  it("maps lead status and source enums to the shared wording", () => {
    expect(LEAD_STATUS_TEXT.new).toBe("新线索");
    expect(LEAD_STATUS_TEXT.in_progress).toBe("跟进中");
    expect(LEAD_SOURCE_TEXT.manual).toBe("手工录入");
    expect(LEAD_SOURCE_TEXT.touch_campaign).toBe("碰一碰");
  });
});

describe("rendererDensity", () => {
  // HUI-2626：390/430 触控密度走 vendored 原生机制（RendererProvider density="touch"
  // → --pn-control-height-touch: 48px，全部控件生效），不是页面自拼 min-height。
  it("uses touch density on phone widths and default elsewhere", () => {
    expect(rendererDensity(390)).toBe("touch");
    expect(rendererDensity(430)).toBe("touch");
    expect(rendererDensity(431)).toBe("default");
    expect(rendererDensity(1024)).toBe("default");
    expect(rendererDensity(1920)).toBe("default");
  });
});
