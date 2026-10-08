import { describe, expect, it } from "vitest";
import { failureText, productError } from "@/lib/productShell";

// HUI-2626 fix2（gate-r2 #2）：错误面不得泄漏裸后端文案。
// 规则：中文产品文案可透传；机器串（英文错误、HTTP 码、JSON 片段）一律映射为
// 产品语句，404 给出「不存在或不在当前工作范围」的下一步语义。

describe("failureText 错误态产品语句", () => {
  it("后端中文文案原样透传（同族诚实中文语气）", () => {
    expect(failureText({ message: "活动还没有门店，不能提交。" }, 400)).toBe("活动还没有门店，不能提交。");
  });

  it("裸英文后端错误（record not found）映射为产品语句", () => {
    expect(failureText({ error: "record not found" }, 404)).toBe("这条记录不存在，或不在当前工作范围。");
    expect(failureText({ message: "record not found" }, 404)).toBe("这条记录不存在，或不在当前工作范围。");
  });

  it("404 状态码即服务端无文案时也映射产品语句", () => {
    expect(failureText(null, 404)).toBe("这条记录不存在，或不在当前工作范围。");
    expect(failureText({}, 404)).toBe("这条记录不存在，或不在当前工作范围。");
  });

  it("401/403 映射为权限产品语句", () => {
    expect(failureText({ error: "not_member" }, 403)).toBe("当前身份没有做这一步的权限。");
    expect(failureText(null, 401)).toBe("当前身份没有做这一步的权限。");
  });

  it("5xx 映射为服务暂不可用产品语句", () => {
    expect(failureText({ message: "internal server error" }, 500)).toBe("服务暂时没有响应，请稍后再试。");
    expect(failureText(null, 502)).toBe("服务暂时没有响应，请稍后再试。");
  });

  it("技术串（fetch failed 等）不透传", () => {
    expect(productError("fetch failed")).toBe("这一步没有完成，请稍后重试。");
  });
});
