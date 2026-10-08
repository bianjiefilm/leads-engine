import { readFileSync } from "node:fs";
import React from "react";
import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { contactRowFacts } from "@/lib/finish";

// HUI-2626 finish-r1：contacts 列表成品化（票面 A 组）。
// 同义层在 lib/finish.ts 锁定；页面级锁定「唯一 primary + 筛选栏结构 + 红线（无裸 table/button）」。

const row = {
  id: "c1",
  name: "王客户",
  business_category: "merchant_customer",
  source_type: "manual",
  consent_status: "granted",
  tags: "vip,重点",
  assigned_member_id: "m1",
};

describe("contactRowFacts", () => {
  it("renders category/source/consent in the shared wording with tags", () => {
    const facts = contactRowFacts(row);
    const value = (label: string) => facts.find((f) => f.label === label)?.value ?? "";
    expect(value("类别")).toBe("商家经营销售");
    expect(value("来源")).toBe("手工录入");
    expect(value("授权")).toBe("已同意");
    expect(value("标签")).toBe("vip/重点");
  });

  it("shows honest fallbacks for blank fields", () => {
    const facts = contactRowFacts({ ...row, tags: "", consent_status: "", assigned_member_id: undefined });
    const value = (label: string) => facts.find((f) => f.label === label)?.value ?? "";
    expect(value("授权")).toBe("未记录");
    expect(value("标签")).toBe("无标签");
  });
});

describe("contacts page red-line shape", () => {
  const src = readFileSync("src/app/(shell)/contacts/page.tsx", "utf8");

  it("keeps exactly one page primary control", () => {
    expect(src.split('data-page-primary="true"').length - 1).toBe(1);
  });

  it("has no bare table and no bare button elements", () => {
    expect(src).not.toContain("<table");
    expect(src).not.toContain("<button");
  });

  it("wires the shared detail drawer", () => {
    expect(src).toContain("DetailDrawer");
    expect(src).toContain("/api/contacts/${");
  });
});
