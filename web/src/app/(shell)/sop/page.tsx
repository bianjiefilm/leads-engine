"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { SurfaceState } from "@/components/workbench/chrome";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, pagePrimary, productError } from "@/lib/productShell";
import { presentSOP, recordLabel, type SOPCapability } from "@/lib/sopReach";

interface SOPAction {
  id: string;
  kind: string;
  status: string;
  purpose: string;
  channel: string;
  body?: string;
  refusal?: string;
  delivered?: boolean;
}

const EMPTY_VIEW = presentSOP(null);

export default function SOPPage() {
  const scope = useCrmScope();
  const tenantId = scope.tenantId ?? "";
  const [view, setView] = useState(EMPTY_VIEW);
  const [items, setItems] = useState<SOPAction[] | null>(null);
  const [error, setError] = useState("");
  const [contactId, setContactId] = useState("");
  const [consentId, setConsentId] = useState("");
  const [channel, setChannel] = useState("sms");
  const [recipient, setRecipient] = useState("");
  const [purpose, setPurpose] = useState("follow_up");
  const [body, setBody] = useState("");
  const [draftId, setDraftId] = useState("");
  const [busy, setBusy] = useState(false);

  const headers = useCallback(
    () => ({ "content-type": "application/json", "x-tenant-id": tenantId }),
    [tenantId],
  );

  const load = useCallback(async (tenantID: string) => {
    setError("");
    setItems(null);
    setView(EMPTY_VIEW);
    if (!tenantID.trim()) {
      setItems([]);
      return;
    }
    const h = { "x-tenant-id": tenantID.trim() };
    try {
      const [capRes, listRes] = await Promise.all([
        fetch("/api/sop/capability", { headers: h }),
        fetch("/api/sop/actions", { headers: h }),
      ]);
      const capBody = (await capRes.json()) as SOPCapability & { message?: string; error?: string };
      const listBody = await listRes.json();
      setView(presentSOP(capBody));
      if (!listRes.ok) {
        setError(failureText(listBody, listRes.status));
        setItems([]);
        return;
      }
      setItems((listBody.items ?? []) as SOPAction[]);
    } catch (e) {
      setError(e instanceof Error ? productError(e.message) : "加载失败");
      setItems([]);
    }
  }, []);

  useEffect(() => {
    void load(scope.tenantId ?? "");
  }, [scope.epoch, scope.tenantId, load]);

  async function post(path: string, payload: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      const res = await fetch(path, { method: "POST", headers: headers(), body: JSON.stringify(payload) });
      const data = await res.json();
      if (!res.ok) {
        setError(failureText(data, res.status));
        return data as { id?: string };
      }
      if (data.kind === "pending_draft" && typeof data.id === "string") setDraftId(data.id);
      await load(tenantId);
      return data as { id?: string; kind?: string };
    } catch (e) {
      setError(e instanceof Error ? productError(e.message) : "提交失败");
      return {};
    } finally {
      setBusy(false);
    }
  }

  const bound = {
    contact_id: contactId.trim(),
    consent_id: consentId.trim(),
    channel,
    recipient: recipient.trim(),
    purpose,
    content_version: 1,
    budget_cents: 0,
    body: body.trim(),
  };

  return (
    <main data-page="sop">
      <header className="page-head">
        <h1>下一次跟进</h1>
        <Link className="btn" href="/">我的工作</Link>
      </header>
      {!tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <div className="card">
        <p data-tone="automation">{view.note}</p>
        <p className="muted">
          <span data-tone="automation">自动化状态</span> 渠道状态：{view.verified ? "已接通" : "未验证"}。无人值守：{view.unattended ? "已明确开启" : "关闭"}。
          这里只做内部提醒、回复草稿和人工确认，不会自动发短信、邮件或企微，也不会扣费。
        </p>
      </div>
      <div className="card">
        <h2>提醒和草稿</h2>
        <p>
          <label>
            联系人 <input value={contactId} onChange={(e) => setContactId(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            授权 <input value={consentId} onChange={(e) => setConsentId(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            渠道{" "}
            <select value={channel} onChange={(e) => setChannel(e.target.value)}>
              <option value="sms">短信</option>
              <option value="email">邮件</option>
              <option value="wecom">企微</option>
            </select>
          </label>{" "}
          <label>
            用途{" "}
            <select value={purpose} onChange={(e) => setPurpose(e.target.value)}>
              <option value="follow_up">跟进</option>
              <option value="marketing">营销</option>
            </select>
          </label>
        </p>
        <p>
          <label>
            收件人 <input value={recipient} onChange={(e) => setRecipient(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            内容{" "}
            <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={3} />
          </label>
        </p>
        <button className="primary" type="button" data-page-primary="true" disabled={busy || !tenantId} onClick={() => void post("/api/sop/reminders", bound)}>
          {pagePrimary("sop")}
        </button>{" "}
        <button className="btn" type="button" disabled={busy || !tenantId} onClick={() => void post("/api/sop/drafts", bound)}>
          保存回复草稿
        </button>
        {error ? <SurfaceState kind="error" title="跟进没有记下" detail={error} /> : null}
      </div>
      <div className="card">
        <h2>人工确认</h2>
        <p className="muted">确认后停在待发送或未送达。没有渠道回执时不会写成已送达。</p>
        <label>
          草稿{" "}
          <input value={draftId} onChange={(e) => setDraftId(e.target.value)} placeholder="草稿 id" />
        </label>{" "}
        <button
          className="btn"
          type="button"
          disabled={busy || !draftId.trim()}
          onClick={() => void post(`/api/sop/drafts/${encodeURIComponent(draftId.trim())}/confirm`, {})}
        >
          人工确认
        </button>
      </div>
      <div className="card">
        <h2>记录</h2>
        {items === null ? <SurfaceState kind="loading" title="正在读取提醒" detail="草稿和人工确认马上就位。" /> : null}
        {items?.length === 0 ? <SurfaceState kind="empty" title="还没有提醒或草稿" detail="记下内部提醒后会出现在这里。" /> : null}
        <ul>
          {items?.map((item) => (
            <li key={item.id}>
              {recordLabel(item.kind, item.delivered ? "delivered" : item.status)} · {item.channel} · {item.purpose}
              {item.body ? ` · ${item.body}` : ""}
              {item.kind === "pending_draft" ? (
                <>
                  {" "}
                  <button className="btn" type="button" disabled={busy} onClick={() => setDraftId(item.id)}>
                    选用
                  </button>
                </>
              ) : null}
            </li>
          ))}
        </ul>
      </div>
    </main>
  );
}
