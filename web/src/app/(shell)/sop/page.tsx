"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { SurfaceState } from "@/components/workbench/chrome";
import { Button, Input } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, pagePrimary, productError } from "@/lib/productShell";
import { presentSOP, recordLabel, type SOPCapability } from "@/lib/sopReach";
import {
  consentChoices,
  contactChoices,
  sopSubmit,
  type ScopeConsent,
  type ScopeContact,
} from "@/lib/scopePick";

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

function consentLabel(item: ScopeConsent): string {
  const purpose = item.purpose || "授权";
  return item.source_channel ? `${purpose} · ${item.source_channel}` : purpose;
}

export default function SOPPage() {
  const scope = useCrmScope();
  const tenantId = scope.tenantId ?? "";
  const [view, setView] = useState(EMPTY_VIEW);
  const [items, setItems] = useState<SOPAction[] | null>(null);
  const [error, setError] = useState("");
  const [contacts, setContacts] = useState<ScopeContact[] | null>(null);
  const [contactId, setContactId] = useState("");
  const [consents, setConsents] = useState<ScopeConsent[] | null>(null);
  const [consentId, setConsentId] = useState("");
  const [channel, setChannel] = useState("sms");
  const [recipient, setRecipient] = useState("");
  const [purpose, setPurpose] = useState("follow_up");
  const [body, setBody] = useState("");
  const [draftId, setDraftId] = useState("");
  const [busy, setBusy] = useState(false);
  const loadSeq = useRef(0);
  const consentSeq = useRef(0);
  const tenantRef = useRef(tenantId);
  tenantRef.current = tenantId;

  const headers = useCallback(
    () => ({ "content-type": "application/json", "x-tenant-id": tenantId }),
    [tenantId],
  );

  const load = useCallback(async (tenantID: string, seq: number) => {
    setError("");
    setItems(null);
    setView(EMPTY_VIEW);
    if (!tenantID.trim()) {
      setItems([]);
      setContacts([]);
      return;
    }
    const h = { "x-tenant-id": tenantID.trim() };
    try {
      const [capRes, listRes, contactRes] = await Promise.all([
        fetch("/api/sop/capability", { headers: h }),
        fetch("/api/sop/actions", { headers: h }),
        fetch("/api/contacts", { headers: h }),
      ]);
      if (loadSeq.current !== seq) return;
      const capBody = (await capRes.json()) as SOPCapability & { message?: string; error?: string };
      const listBody = await listRes.json();
      setView(presentSOP(capBody));
      let nextContacts: ScopeContact[] = [];
      if (contactRes.ok) {
        const contactBody = await contactRes.json().catch(() => null);
        nextContacts = contactChoices(contactBody?.items);
      }
      setContacts(nextContacts);
      if (!listRes.ok) {
        setError(failureText(listBody, listRes.status));
        setItems([]);
        return;
      }
      if (!capRes.ok) setError(failureText(capBody, capRes.status));
      setItems((listBody.items ?? []) as SOPAction[]);
    } catch (e) {
      if (loadSeq.current !== seq) return;
      setError(e instanceof Error ? productError(e.message) : "加载失败");
      setItems([]);
      setContacts([]);
    }
  }, []);

  useEffect(() => {
    const seq = ++loadSeq.current;
    consentSeq.current += 1;
    setContactId("");
    setConsentId("");
    setConsents(null);
    setContacts(null);
    setDraftId("");
    setBusy(false);
    void load(scope.tenantId ?? "", seq);
  }, [scope.epoch, scope.tenantId, load]);

  async function loadConsents(tenantID: string, id: string) {
    const seq = ++consentSeq.current;
    setConsentId("");
    setConsents(null);
    if (!tenantID.trim() || !id) {
      setPurpose("follow_up");
      return;
    }
    try {
      const res = await fetch(`/api/contacts/${encodeURIComponent(id)}/consents`, {
        headers: { "x-tenant-id": tenantID.trim() },
      });
      const body = await res.json().catch(() => null);
      if (consentSeq.current !== seq) return;
      const choices = res.ok ? consentChoices(body?.items) : [];
      setConsents(choices);
      if (choices.length === 0) setPurpose("follow_up");
    } catch {
      if (consentSeq.current !== seq) return;
      setConsents([]);
      setPurpose("follow_up");
    }
  }

  async function post(path: string, payload: object) {
    const tenantAtSubmit = tenantId;
    const seqAtSubmit = loadSeq.current;
    setBusy(true);
    setError("");
    try {
      const res = await fetch(path, { method: "POST", headers: headers(), body: JSON.stringify(payload) });
      const data = await res.json();
      if (tenantRef.current !== tenantAtSubmit || loadSeq.current !== seqAtSubmit) return data as { id?: string };
      if (!res.ok) {
        setError(failureText(data, res.status));
        return data as { id?: string };
      }
      if (data.kind === "pending_draft" && typeof data.id === "string" && data.id.trim()) setDraftId(data.id);
      await load(tenantAtSubmit, seqAtSubmit);
      return data as { id?: string; kind?: string };
    } catch (e) {
      if (tenantRef.current === tenantAtSubmit) setError(e instanceof Error ? productError(e.message) : "提交失败");
      return {};
    } finally {
      if (tenantRef.current === tenantAtSubmit) setBusy(false);
    }
  }

  const selected = contacts?.find((item) => item.id === contactId) ?? null;
  const selectedConsent = consents?.find((item) => item.id === consentId) ?? null;
  const payload = sopSubmit({
    contact: selected,
    purpose,
    consent: selectedConsent,
    channel,
    recipient,
    body,
  });
  const marketingOpen = (consents?.length ?? 0) > 0;

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
            客户{" "}
            <select
              value={contactId}
              onChange={(e) => {
                const id = e.target.value;
                setContactId(id);
                void loadConsents(tenantId, id);
              }}
            >
              <option value="">选择客户</option>
              {contacts?.map((item) => (
                <option key={item.id} value={item.id}>{item.name}</option>
              ))}
            </select>
          </label>
        </p>
        {tenantId && contacts && contacts.length === 0 ? <p className="muted">还没有可选的客户</p> : null}
        <p>
          <label>
            授权{" "}
            <select value={consentId} onChange={(e) => setConsentId(e.target.value)} disabled={!marketingOpen}>
              <option value="">不选择授权</option>
              {consents?.map((item) => (
                <option key={item.id} value={item.id}>{consentLabel(item)}</option>
              ))}
            </select>
          </label>
        </p>
        {selected && consents && consents.length === 0 ? <p className="muted">还没有可选择的授权</p> : null}
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
            <select
              value={purpose}
              onChange={(e) => {
                const next = e.target.value;
                if (next === "marketing" && !marketingOpen) return;
                setPurpose(next);
              }}
            >
              <option value="follow_up">跟进</option>
              <option value="marketing" disabled={!marketingOpen}>营销</option>
            </select>
          </label>
        </p>
        <p>
          <label>
            <Input label="收件人" value={recipient} onChange={(e) => setRecipient(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            内容{" "}
            <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={3} />
          </label>
        </p>
        <Button
          variant="primary" size="sm"
          type="button"
          data-page-primary="true"
          disabled={busy || !tenantId || !payload}
          onClick={() => {
            if (!payload) return;
            void post("/api/sop/reminders", payload);
          }}
        >
          {pagePrimary("sop")}
        </Button>{" "}
        <Button
          variant="secondary" size="sm"
          type="button"
          disabled={busy || !tenantId || !payload}
          onClick={() => {
            if (!payload) return;
            void post("/api/sop/drafts", payload);
          }}
        >
          保存回复草稿
        </Button>
        {error ? <SurfaceState kind="error" title="跟进没有记下" detail={error} /> : null}
      </div>
      <div className="card">
        <h2>人工确认</h2>
        <p className="muted">确认后停在待发送或未送达。没有渠道回执时不会写成已送达。</p>
        <p className="muted">{draftId ? "已选用一条草稿，可以人工确认。" : "先保存草稿，或在下面的记录里选用。"}</p>
        <Button
          variant="secondary" size="sm"
          type="button"
          disabled={busy || !draftId.trim()}
          onClick={() => void post(`/api/sop/drafts/${encodeURIComponent(draftId.trim())}/confirm`, {})}
        >
          人工确认
        </Button>
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
                  <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => setDraftId(item.id)}>
                    选用
                  </Button>
                </>
              ) : null}
            </li>
          ))}
        </ul>
      </div>
    </main>
  );
}
