import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { outboundResultLine, receiptLabel } from "@/lib/outboundCall";

// HUI-2626 fix2（gate-r2 #6 主行动收敛 / #7 partial / #8 offline）。

const WEB = joinWeb();

function joinWeb(): string {
  // tests 目录的上一级即 web/。
  return ".";
}

function src(path: string): string {
  return readFileSync(`${WEB}/src/${path}`, "utf8");
}

describe("主行动收敛（至多一个可执行主行动）", () => {
  it("/sop：主行动仅在可执行时渲染，不可执行时给指引，次级保持 secondary", () => {
    const sop = src("app/(shell)/sop/page.tsx");
    expect(sop).toContain('data-page-primary="true"');
    // 不可执行时不再渲染一排等权灰按钮，而是给下一步指引。
    expect(sop).toContain("选择客户并填写内容后，这里可以记下内部提醒");
    // 主行动改为条件渲染（payload 就绪才出现）。
    expect(sop).toMatch(/\{payload \? \(/);
    // 人工确认在无草稿时不渲染等权灰钮。
    expect(sop).toMatch(/\{draftId \? \(/);
  });

  it("/outbound：主行动仅在选中客户与活动后渲染；取消/转人工仅在有任务时渲染", () => {
    const outbound = src("app/(shell)/outbound/page.tsx");
    expect(outbound).toContain('data-page-primary="true"');
    expect(outbound).toContain("选择客户后，这里记录一次模拟提交");
    expect(outbound).toMatch(/\{selected && campaignId \? \(/);
    expect(outbound).toMatch(/\{task \? \(/);
  });

  it("「今天」Home：行内导航至多一个（线索 > 接待 > 商机）", () => {
    const home = src("app/(shell)/page.tsx");
    expect(home).toContain("行内导航至多一个");
    // 旧的三连并存写法不再出现。
    expect(home).not.toContain('href="/reception">打开接待，本屏尚未完成这一步</Link>');
  });
});

describe("partial 部分成功/部分失败一态（fixture）", () => {
  it("拨打成功但未接通 = 部分完成，不写成已送达", () => {
    expect(
      outboundResultLine({ simulation: true, dial_succeeded: true, real_connected: false, cost: "0" }),
    ).toBe("部分完成：模拟：是。拨打成功：是。真实接通：否。费用：0。");
  });

  it("完全成功 / 完全失败 / 无回执各成一态", () => {
    expect(outboundResultLine({ simulation: true, dial_succeeded: true, real_connected: true, cost: "1.2" }).startsWith("已完成：")).toBe(true);
    expect(outboundResultLine({ simulation: true, dial_succeeded: false, real_connected: false, cost: "unknown" }).startsWith("未完成：")).toBe(true);
    expect(outboundResultLine(null)).toBe("还没有任务。");
  });

  it("回执词汇沿用同族中文徽章（partial 场景的非 SSR 断言锚点）", () => {
    expect(receiptLabel("connected", "simulated")).toBe("模拟接通");
    expect(receiptLabel("dial_submission", "simulated")).toBe("模拟提交");
    expect(receiptLabel("connected", "other")).toBe("未证实接通");
  });

  it("/outbound 页面消费同一 composer（场景证据与页面一致）", () => {
    const outbound = src("app/(shell)/outbound/page.tsx");
    expect(outbound).toContain("outboundResultLine(task)");
  });
});

describe("offline 应用内离线 UI", () => {
  it("service worker 对导航请求做 offline.html 兜底", () => {
    const sw = readFileSync(`${WEB}/public/sw.js`, "utf8");
    expect(sw).toContain('"navigate"');
    expect(sw).toContain("offline.html");
    expect(sw).toContain("caches.open");
  });

  it("offline.html 是应用内离线 UI：产品语气 + 重试入口", () => {
    const page = readFileSync(`${WEB}/public/offline.html`, "utf8");
    expect(page).toContain("离线");
    expect(page).toContain("重试");
    expect(page).toContain("location.reload");
    // 不是浏览器错误页：不出现 CHROME/ERR 机器串。
    expect(page).not.toContain("ERR_INTERNET_DISCONNECTED");
  });

  it("壳层注册 service worker（CrmShell 挂 OfflineReady）", () => {
    const shell = src("components/eco-nav/CrmShell.tsx");
    expect(shell).toContain("OfflineReady");
    const ready = src("components/workbench/offlineReady.tsx");
    expect(ready).toContain("serviceWorker.register");
    expect(ready).toContain("/sw.js");
  });
});
