import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RendererProvider, Status, type StatusState } from "@/vendor/painuo/react/v1/src/index";

// HUI-2622 R 面：Status 原语在真实 node SSR 下的行为 + 负控。
// node env（无 jsdom）；renderToString 即 react-dom/server 生产 SSR 路径。

function tree(state: StatusState = "failed", label = "失败") {
  return (
    <RendererProvider profile="leads-web" surface="work.light" theme="light">
      <Status state={state} label={label} />
    </RendererProvider>
  );
}

describe("painuo renderer Status under node SSR (HUI-2622 R-face)", () => {
  it("R4: SSR renders the provider scope and the status badge", () => {
    const html = renderToString(tree());
    expect(html).toContain("pn-r-scope");
    expect(html).toContain('data-pn-profile="leads-web"');
    expect(html).toContain('data-pn-surface="work.light"');
    expect(html).toContain('data-pn-theme="light"');
    expect(html).toContain("pn-r-status");
    expect(html).toContain('data-pn-status="failed"');
    expect(html).toContain("pn-r-badge-danger");
    expect(html).toContain("失败");
  });

  it("R4: state outside the closed enum degrades to neutral unknown without throwing", () => {
    const html = renderToString(tree("not-a-real-state" as StatusState));
    expect(html).toContain('data-pn-status="unknown"');
    expect(html).toContain("pn-r-badge-neutral");
    expect(html).not.toContain("pn-r-badge-success");
  });

  it("R4: Status without a provider throws 'renderer provider required'", () => {
    expect(() => renderToString(<Status state="failed" label="失败" />)).toThrow(
      /renderer provider required/,
    );
  });

  it("R5: leads-web with an illegal surface/theme combo throws 'renderer scope invalid'", () => {
    expect(() =>
      renderToString(
        <RendererProvider profile="leads-web" surface="portal.brand" theme="light">
          <Status state="failed" label="失败" />
        </RendererProvider>,
      ),
    ).toThrow(/renderer scope invalid/);
  });

  it("R5: leads-web work.light/dark is registered but this slice keeps light; dark still renders legally", () => {
    // work.light/dark 是登记合法组合（profile-scopes.json）；本切片不启用 dark，但合同组合必须可渲染。
    const html = renderToString(
      <RendererProvider profile="leads-web" surface="work.light" theme="dark">
        <Status state="resultReady" label="完成" />
      </RendererProvider>,
    );
    expect(html).toContain('data-pn-status="resultReady"');
  });
});
