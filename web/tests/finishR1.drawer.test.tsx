import React from "react";
import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RendererProvider } from "@/vendor/painuo/react/v1/src/index";
import { DetailDrawer } from "@/components/workbench/detailDrawer";
import { RendererSelect } from "@/components/workbench/rendererSelect";

// HUI-2626 finish-r1：DetailDrawer / RendererSelect 接线。
// node SSR：vendored Drawer/Select 的弹层走 ScopedPortal（portal root 未连接时渲染 null），
// 这里锁定「closed 不渲染 / open 不抛错 + 触发器结构」；键盘/焦点/开合行为在真实浏览器走查取证
// （docs/audits/hui-2626/finish-r1/），vendored 行为本身由 producer 锁保护。

function withScope(node: React.ReactNode) {
  return (
    <RendererProvider profile="leads-web" surface="work.light" theme="light">
      {node}
    </RendererProvider>
  );
}

describe("DetailDrawer", () => {
  it("renders nothing while closed", () => {
    const html = renderToString(
      withScope(
        <DetailDrawer open={false} onClose={() => {}} title="线索详情" width={1440}>
          {"DETAIL_BODY"}
        </DetailDrawer>,
      ),
    );
    expect(html).not.toContain("DETAIL_BODY");
  });

  it("open drawer SSR is portal-deferred and must not throw", () => {
    const html = renderToString(
      withScope(
        <div>
          <DetailDrawer open onClose={() => {}} title="线索详情" width={390}>
            {"DETAIL_BODY"}
          </DetailDrawer>
        </div>,
      ),
    );
    expect(typeof html).toBe("string");
  });
});

describe("RendererSelect", () => {
  it("renders the labeled trigger with the chosen value in SSR", () => {
    const html = renderToString(
      withScope(
        <RendererSelect
          label="授权状态"
          value="granted"
          onValueChange={() => {}}
          choices={[
            { value: "pending", label: "待确认" },
            { value: "granted", label: "已同意" },
            { value: "denied", label: "已拒绝" },
          ]}
        />,
      ),
    );
    expect(html).toContain("授权状态");
    expect(html).toContain("已同意");
    expect(html).toContain("pn-r-select");
  });
});
