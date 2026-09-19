// 公共落地页(HUI-1679 / FEAT-0180 首版固定表单):
//   - 渲染服务端公共描述符的固定字段(name/phone/可选 wechat)+ 单独营销勾选框
//     (缺省不勾:没有同意不进营销池);
//   - 经 BFF /api/public/forms/{id}/submissions 提交(联系方式只进 POST body,
//     绝不进 URL;来源 source/source_ref 只作留痕,不能改变接收租户);
//   - 成功页展示撤销渠道文案;逐项展示服务端 400 details;429/410 显式提示。
"use client";

import { useParams, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import {
  buildPayload,
  collectableFields,
  formPublicPath,
  formSubmitPath,
  guardSubmission,
  REVOKE_HINT_FALLBACK,
  type FieldIssue,
  type PublicFormDescriptor,
  type SubmissionSuccess,
} from "@/lib/publicForm";

const FIELD_LABELS: Record<string, string> = {
  name: "称呼",
  phone: "手机号",
  wechat: "微信号(选填)",
};

function newPageRef(): string {
  // 页面级 source_ref:同一次访问的重试共享(幂等),刷新/新访问产生新引用。
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID().replace(/-/g, "").slice(0, 24);
  }
  return "page-" + Date.now().toString(36);
}

function Landing() {
  const params = useParams<{ id: string }>();
  const search = useSearchParams();
  const formId = typeof params?.id === "string" ? params.id : "";

  const [desc, setDesc] = useState<PublicFormDescriptor | null>(null);
  const [loadError, setLoadError] = useState<{ status: number; message: string } | null>(null);
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [wechat, setWechat] = useState("");
  const [marketing, setMarketing] = useState(false); // 缺省不勾
  const [issues, setIssues] = useState<FieldIssue[]>([]);
  const [serverError, setServerError] = useState<string>("");
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState<SubmissionSuccess | null>(null);

  const source = useMemo(() => (search.get("source") ?? "landing").slice(0, 64), [search]);
  const pageRef = useMemo(newPageRef, []); // eslint-disable-line react-hooks/exhaustive-deps
  const sourceRef = useMemo(() => {
    const q = search.get("ref");
    if (q && /^[A-Za-z0-9._:@-]{1,128}$/.test(q)) return q;
    return pageRef;
  }, [search, pageRef]);

  useEffect(() => {
    if (!formId) return;
    fetch(formPublicPath(formId))
      .then(async (res) => {
        const body = await res.json().catch(() => ({}));
        if (!res.ok) {
          setLoadError({ status: res.status, message: body.message ?? body.error ?? `HTTP ${res.status}` });
          return;
        }
        setDesc(body as PublicFormDescriptor);
      })
      .catch((e: Error) => setLoadError({ status: 0, message: e.message }));
  }, [formId]);

  const submit = useCallback(async () => {
    setServerError("");
    const local = guardSubmission(desc, { name, phone, wechat });
    setIssues(local);
    if (local.length > 0) return;
    setSubmitting(true);
    try {
      const res = await fetch(formSubmitPath(formId), {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(buildPayload(desc, { name, phone, wechat }, marketing, { source, sourceRef })),
      });
      const body = await res.json().catch(() => ({}));
      if (res.ok) {
        setDone(body as SubmissionSuccess);
        return;
      }
      if (body.details && Array.isArray(body.details)) {
        setIssues(body.details as FieldIssue[]);
        return;
      }
      setServerError(body.message ?? `提交失败(HTTP ${res.status})`);
    } catch (e) {
      setServerError((e as Error).message);
    } finally {
      setSubmitting(false);
    }
  }, [desc, formId, name, phone, wechat, marketing, source, sourceRef]);

  if (loadError) {
    const gone = loadError.status === 410;
    return (
      <main>
        <h1>表单不可用</h1>
        <p className="muted">
          {gone
            ? "该表单已停止收集或已过期,感谢您的关注。"
            : "表单不存在或暂不可用,请核对链接后重试。"}
        </p>
      </main>
    );
  }
  if (!desc) {
    return (
      <main>
        <h1>加载中…</h1>
      </main>
    );
  }
  if (done) {
    return (
      <main>
        <h1>提交成功</h1>
        <p>
          感谢您的留言,我们会尽快与您联系。
          {done.duplicate ? "(检测到重复提交,已保留您此前的一次提交)" : ""}
        </p>
        <div className="card">
          <h2>关于您的授权</h2>
          <ul>
            <li>告知版本:{done.notice_version || desc.notice_version}</li>
            <li>
              营销信息:{done.marketing_allowed ? "已同意接收,可随时撤销" : "未开通(您未勾选营销同意,我们不会将您用于营销触达)"}
            </li>
          </ul>
          <p className="muted">{done.revoke_hint || REVOKE_HINT_FALLBACK}</p>
        </div>
      </main>
    );
  }

  const fields = collectableFields(desc);
  return (
    <main>
      <h1>咨询留言</h1>
      <p className="muted">
        用途:{desc.purpose || "处理您的咨询"} · 告知版本:{desc.notice_version}
      </p>
      <div className="card">
        {fields.map((f) => (
          <div key={f.name} style={{ marginBottom: 12 }}>
            <label htmlFor={`f-${f.name}`}>{FIELD_LABELS[f.name] ?? f.name}</label>
            <br />
            <input
              id={`f-${f.name}`}
              type="text"
              value={f.name === "name" ? name : f.name === "phone" ? phone : wechat}
              onChange={(e) => {
                const v = e.target.value;
                if (f.name === "name") setName(v);
                else if (f.name === "phone") setPhone(v);
                else setWechat(v);
              }}
              maxLength={f.name === "name" ? 100 : f.name === "phone" ? 32 : 64}
              style={{ width: "100%" }}
            />
          </div>
        ))}
        <label>
          <input type="checkbox" checked={marketing} onChange={(e) => setMarketing(e.target.checked)} />
          {desc.marketing_prompt || "我同意接收该商家的营销信息(可随时撤销)"}
        </label>
        {issues.map((iss, i) => (
          <p key={i} style={{ color: "#b00" }}>
            {FIELD_LABELS[iss.field] ?? iss.field}:{iss.message}
          </p>
        ))}
        {serverError ? <p style={{ color: "#b00" }}>{serverError}</p> : null}
        <button type="button" onClick={submit} disabled={submitting}>
          {submitting ? "提交中…" : "提交"}
        </button>
        <p className="muted">{REVOKE_HINT_FALLBACK}</p>
      </div>
    </main>
  );
}

export default function FormLandingPage() {
  return (
    <Suspense fallback={<main><h1>加载中…</h1></main>}>
      <Landing />
    </Suspense>
  );
}
