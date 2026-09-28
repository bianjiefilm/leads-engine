import { describe, expect, it } from "vitest";
import { brandIsDisplayOnly, exportConfirmHeader } from "../src/lib/crmIsolation";

describe("crm tenant isolation copy", () => {
  it("treats a brand name as display, not as the tenant", () => {
    const text = brandIsDisplayOnly("白标品牌");
    expect(text).toContain("白标品牌");
    expect(text).toContain("不能代替租户");
    expect(text).not.toContain("tenant_id");
  });

  it("asks for a second authorization header before download", () => {
    expect(exportConfirmHeader()).toEqual({ "x-export-confirm": "1" });
  });
});
