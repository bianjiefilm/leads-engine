// 公共留资表单镜像规则测试(HUI-1679 / FEAT-0180):web 层只做展示守卫,
// 服务端是唯一真源;这里验证镜像规则与载荷白名单。
import { describe, expect, it } from "vitest";
import {
  buildPayload,
  collectableFields,
  formPublicPath,
  formSubmitPath,
  guardSubmission,
  isValidCnMobile,
  normalizePhone,
  type PublicFormDescriptor,
} from "../src/lib/publicForm";

const DESC: PublicFormDescriptor = {
  form_id: "frm_x",
  version: 1,
  status: "published",
  schema: {
    schema_version: 1,
    consent_required: true,
    fields: [
      { name: "name", type: "text", required: true },
      { name: "phone", type: "phone_cn", required: true },
      { name: "wechat", type: "text", required: false },
    ],
  },
  notice_version: "privacy-2026-v3",
  purpose: "留资咨询与活动回访",
  marketing_prompt: "我同意接收营销信息",
  marketing_default: false,
  revoke_hint: "hint",
};

describe("phone mirror rules", () => {
  it("normalizes like the server", () => {
    expect(normalizePhone("139 0000 0001")).toBe("13900000001");
    expect(normalizePhone("+8613900000001")).toBe("13900000001");
    expect(normalizePhone("")).toBe("");
  });
  it("accepts only CN mobiles", () => {
    expect(isValidCnMobile("13900000001")).toBe(true);
    expect(isValidCnMobile("12345")).toBe(false);
    expect(isValidCnMobile("12300000000")).toBe(false);
  });
});

describe("field collection follows the form schema", () => {
  it("always includes name+phone; wechat only when the form collects it", () => {
    expect(collectableFields(DESC).map((f) => f.name)).toEqual(["name", "phone", "wechat"]);
    const noWechat: PublicFormDescriptor = {
      ...DESC,
      schema: { ...DESC.schema, fields: DESC.schema.fields.filter((f) => f.name !== "wechat") },
    };
    expect(collectableFields(noWechat).map((f) => f.name)).toEqual(["name", "phone"]);
  });
});

describe("submission guard mirrors the server whitelist", () => {
  it("flags empty name/phone itemized", () => {
    const issues = guardSubmission(DESC, { name: "  ", phone: "", wechat: "" });
    expect(issues.map((i) => i.field).sort()).toEqual(["name", "phone"]);
  });
  it("flags a malformed phone and overlong name", () => {
    const issues = guardSubmission(DESC, { name: "x".repeat(101), phone: "12345", wechat: "" });
    expect(issues.map((i) => i.field).sort()).toEqual(["name", "phone"]);
  });
  it("passes a valid submission", () => {
    expect(guardSubmission(DESC, { name: "张三", phone: "139 0000 0001", wechat: "" })).toEqual([]);
  });
});

describe("payload is a strict whitelist", () => {
  it("carries marketing_allowed default-off and provenance, never tenant", () => {
    const p = buildPayload(DESC, { name: "张三", phone: "13900000001", wechat: "wx" }, false, {
      source: "douyin",
      sourceRef: "lead-001",
    });
    expect(Object.keys(p).sort()).toEqual([
      "marketing_allowed", "name", "phone", "source", "source_ref", "wechat",
    ]);
    expect(p.marketing_allowed).toBe(false);
    expect(p.source).toBe("douyin");
    expect(JSON.stringify(p)).not.toMatch(/tenant/i);
  });
  it("drops wechat when the form does not collect it (无自由字段)", () => {
    const noWechat: PublicFormDescriptor = {
      ...DESC,
      schema: { ...DESC.schema, fields: DESC.schema.fields.filter((f) => f.name !== "wechat") },
    };
    const p = buildPayload(noWechat, { name: "张三", phone: "13900000001", wechat: "wx" }, true, {
      source: "s",
      sourceRef: "r",
    });
    expect("wechat" in p).toBe(false);
    expect(p.marketing_allowed).toBe(true);
  });
});

describe("paths carry no contact data", () => {
  it("public paths contain only the form id", () => {
    expect(formPublicPath("frm_x")).toBe("/api/public/forms/frm_x");
    expect(formSubmitPath("frm_x")).toBe("/api/public/forms/frm_x/submissions");
  });
});
