import React from "react";
import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { CrmShell } from "@/components/eco-nav/CrmShell";
import { provisionalEcoNav } from "@/lib/eco-nav/fixture";

// HUI-2626 finish-r1：CRM 壳为整个工作台子树提供 painuo renderer scope（DECISIONS.md D2-2）。
// node SSR 环境（无 jsdom）；CrmShell 首屏 authenticated=false，effect 不执行，无网络依赖。

describe("finish-r1 shell token scope", () => {
  it("CrmShell renders the painuo renderer scope around the workbench subtree", () => {
    const html = renderToString(
      <CrmShell model={provisionalEcoNav()}>
        <main>{"PAGE_MARKER"}</main>
      </CrmShell>,
    );
    expect(html).toContain("pn-r-scope");
    expect(html).toContain('data-pn-profile="leads-web"');
    expect(html).toContain('data-pn-surface="work.light"');
    expect(html).toContain('data-pn-theme="light"');
    expect(html).toContain("desk-frame");
    expect(html).toContain("PAGE_MARKER");
  });
});
