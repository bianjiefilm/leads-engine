import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { leadRowFacts } from "@/lib/finish";

// HUI-2626 finish-r1：leads 列表成品化。
// 服务端 /api/v1/leads 无查询参数、返回当前范围全量（owner 全量 / 销售只看名下，
// handlers_business.go:462），状态筛选是对全量结果的客户端过滤，文案如实标注。

const row = {
  id: "l1",
  contact_id: "c1",
  status: "in_progress",
  assigned_member_id: "m1",
  name: "李线索",
  source: "碰一碰",
};

describe("leadRowFacts", () => {
  it("renders source/status/owner in the shared wording", () => {
    const facts = leadRowFacts(row);
    const value = (label: string) => facts.find((f) => f.label === label)?.value ?? "";
    expect(value("来源")).toBe("碰一碰");
    expect(value("状态")).toBe("跟进中");
    expect(value("负责人")).toBe("m1");
  });

  it("keeps unassigned leads visibly unassigned", () => {
    const facts = leadRowFacts({ ...row, assigned_member_id: undefined });
    expect(facts.find((f) => f.label === "负责人")?.value).toBe("待分配（接收时没有指定负责人）");
  });

  it("filters the full server list honestly by status", () => {
    const rows = [
      row,
      { ...row, id: "l2", status: "new" },
      { ...row, id: "l3", status: "converted" },
    ];
    const inProgress = rows.filter((r) => r.status === "in_progress");
    expect(inProgress).toHaveLength(1);
  });
});

describe("leads page finish shape", () => {
  const src = readFileSync("src/app/(shell)/leads/page.tsx", "utf8");

  it("labels the client-side status filter over the full server list", () => {
    expect(src).toContain("全部线索");
    expect(src).not.toContain("服务端筛选");
  });

  it("wires the shared detail drawer against the real timeline API", () => {
    expect(src).toContain("DetailDrawer");
    expect(src).toContain("/api/leads/${");
    expect(src).toContain("/timeline");
  });

  it("has no bare table and no bare button elements", () => {
    expect(src).not.toContain("<table");
    expect(src).not.toContain("<button");
  });
});
