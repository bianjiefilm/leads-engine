// 公共留资表单纯函数(HUI-1679 / FEAT-0180 首版固定表单)。落地页组件只做
// 渲染;所有校验与归属判断的真源在 Go 服务端,这里仅镜像服务端规则用于
// 提交前守卫(逐项提示),服务端对每次提交重新校验。
//
// 语义红线(镜像服务端):
//   - 无自由字段:载荷键只有 name/phone/wechat + marketing_allowed/source/source_ref;
//   - marketing_allowed 独立勾选,缺省 false(没有同意不进营销池);
//   - 联系方式只进 POST body,绝不进 URL/全局 Context(来源 source 只作留痕);
//   - 接收租户由表单决定,页面与载荷不携带任何租户信息。

export const REVOKE_HINT_FALLBACK =
  "如需停止接收营销信息,可联系商家,或在商家 CRM 客户档案页对该来源授权执行「撤销/停止营销」。撤销不可恢复。";

export interface FormFieldSpec {
  name: "name" | "phone" | "wechat";
  type: string;
  required: boolean;
}

export interface PublicFormDescriptor {
  form_id: string;
  version: number;
  status: string;
  schema: { schema_version: number; consent_required: boolean; fields: FormFieldSpec[] };
  notice_version?: string;
  purpose?: string;
  marketing_prompt?: string;
  marketing_default: boolean;
  revoke_hint?: string;
}

export interface SubmissionSuccess {
  submission_id: string;
  contact_id: string;
  lead_id: string;
  class: string;
  duplicate: boolean;
  duplicate_kind?: string;
  marketing_allowed: boolean;
  notice_version?: string;
  revoke_hint?: string;
}

export interface FieldIssue {
  field: string;
  message: string;
}

// ---- 归一化与守卫(镜像 server store.NormalizePhone / phoneProblem) ----------

export function normalizePhone(phone: string): string {
  const p = (phone ?? "").trim();
  if (!p) return "";
  let s = [...p].filter((ch) => !" -().　－（）".includes(ch)).join("");
  s = s.replace(/^\+/, "");
  if (s.length === 13 && s.startsWith("86")) s = s.slice(2);
  return s;
}

export function isValidCnMobile(phone: string): boolean {
  const norm = normalizePhone(phone);
  return /^1[3-9]\d{9}$/.test(norm);
}

export function formPublicPath(formId: string): string {
  // BFF catch-all:/api/* -> Go /api/v1/*;路径只含表单 id,无任何联系方式/租户。
  return `/api/public/forms/${encodeURIComponent(formId)}`;
}

export function formSubmitPath(formId: string): string {
  return `${formPublicPath(formId)}/submissions`;
}

// collectableFields 返回该表单实际收集的字段(name/phone 恒有)。
export function collectableFields(desc: PublicFormDescriptor | null): FormFieldSpec[] {
  if (!desc) return [];
  const fixed: FormFieldSpec[] = [
    { name: "name", type: "text", required: true },
    { name: "phone", type: "phone_cn", required: true },
  ];
  const wechat = desc.schema.fields.find((f) => f.name === "wechat");
  return wechat ? [...fixed, wechat] : fixed;
}

// guardSubmission mirrors the server's per-item validation for instant feedback.
export function guardSubmission(
  desc: PublicFormDescriptor | null,
  values: { name: string; phone: string; wechat: string },
): FieldIssue[] {
  const issues: FieldIssue[] = [];
  const collected = new Set((desc ? collectableFields(desc) : []).map((f) => f.name));
  if (!collected.has("name") || !values.name.trim()) {
    if (!values.name.trim()) issues.push({ field: "name", message: "请填写称呼" });
  }
  if (values.name.trim().length > 100) {
    issues.push({ field: "name", message: "称呼不能超过 100 个字" });
  }
  if (!values.phone.trim()) {
    issues.push({ field: "phone", message: "请填写手机号" });
  } else if (!isValidCnMobile(values.phone)) {
    issues.push({ field: "phone", message: "请填写 11 位大陆手机号(1[3-9] 开头)" });
  }
  if (collected.has("wechat") && values.wechat.trim().length > 64) {
    issues.push({ field: "wechat", message: "微信号不能超过 64 个字符" });
  }
  return issues;
}

// buildPayload assembles the whitelisted submission body. marketingAllowed
// defaults to false; wechat is dropped when the form does not collect it.
export function buildPayload(
  desc: PublicFormDescriptor | null,
  values: { name: string; phone: string; wechat: string },
  marketingAllowed: boolean,
  provenance: { source: string; sourceRef: string },
): Record<string, unknown> {
  const collected = new Set((desc ? collectableFields(desc) : []).map((f) => f.name));
  const payload: Record<string, unknown> = {
    name: values.name.trim(),
    phone: values.phone.trim(),
    marketing_allowed: marketingAllowed,
    source: provenance.source,
    source_ref: provenance.sourceRef,
  };
  if (collected.has("wechat")) payload.wechat = values.wechat.trim();
  return payload;
}
