"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { SurfaceState } from "@/components/workbench/chrome";
import { Button } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, listTenantHeader, pagePrimary, productError } from "@/lib/productShell";
import { outboundResultLine, presentOutbound, receiptLabel, type OutboundCapability } from "@/lib/outboundCall";
import {
  campaignChoice,
  consentChoices,
  contactChoices,
  outboundSubmit,
  type ScopeConsent,
  type ScopeContact,
} from "@/lib/scopePick";

interface OutboundReceipt {
  id: string;
  kind: string;
  status: string;
  simulation?: boolean;
  dial_succeeded?: boolean;
  real_connected?: boolean;
}

interface OutboundTask {
  id: string;
  state?: string;
  simulation?: boolean;
  dial_succeeded?: boolean;
  real_connected?: boolean;
  empty_number_detected?: boolean;
  cost?: string;
  error?: string;
  receipts?: OutboundReceipt[];
}

const EMPTY_VIEW = presentOutbound(null);

function consentLabel(item: ScopeConsent): string {
  const purpose = item.purpose || "授权";
  return item.source_channel ? `${purpose} · ${item.source_channel}` : purpose;
}

export default function OutboundPage() {
  const scope = useCrmScope();
  const tenant = scope.tenant ?? "";
  const [view, setView] = useState(EMPTY_VIEW);
  const [error, setError] = useState("");
  const [contacts, setContacts] = useState<ScopeContact[] | null>(null);
  const [contactId, setContactId] = useState("");
  const [consents, setConsents] = useState<ScopeConsent[] | null>(null);
  const [consentId, setConsentId] = useState("");
  const [task, setTask] = useState<OutboundTask | null>(null);
  const [busy, setBusy] = useState(false);
  const loadSeq = useRef(0);
  const consentSeq = useRef(0);
  const tenantRef = useRef(tenant);
  tenantRef.current = tenant;

  const headers = useCallback(
    () => ({ "content-type": "application/json", ...listTenantHeader(tenant) }),
    [tenant],
  );

  const load = useCallback(async (tenant: string, seq: number) => {
    setError("");
    setView(EMPTY_VIEW);
    if (!tenant.trim()) {
      setContacts([]);
      return;
    }
    const h = listTenantHeader(tenant.trim()) ?? {};
    try {
      const [capRes, contactRes] = await Promise.all([
        fetch("/api/outbound/capability", { headers: h }),
        fetch("/api/contacts", { headers: h }),
      ]);
      if (loadSeq.current !== seq) return;
      const body = (await capRes.json()) as OutboundCapability & { message?: string; error?: string };
      setView(presentOutbound(body));
      if (!capRes.ok) setError(failureText(body, capRes.status));
      let nextContacts: ScopeContact[] = [];
      if (contactRes.ok) {
        const contactBody = await contactRes.json().catch(() => null);
        nextContacts = contactChoices(contactBody?.items);
      }
      setContacts(nextContacts);
    } catch (e) {
      if (loadSeq.current !== seq) return;
      setError(e instanceof Error ? productError(e.message) : "加载失败");
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
    setTask(null);
    setBusy(false);
    void load(scope.tenant ?? "", seq);
  }, [scope.epoch, scope.tenant, load]);

  async function loadConsents(tenant: string, id: string) {
    const seq = ++consentSeq.current;
    setConsentId("");
    setConsents(null);
    if (!tenant.trim() || !id) return;
    try {
      const res = await fetch(`/api/contacts/${encodeURIComponent(id)}/consents`, {
        headers: listTenantHeader(tenant.trim()) ?? {},
      });
      const body = await res.json().catch(() => null);
      if (consentSeq.current !== seq) return;
      setConsents(res.ok ? consentChoices(body?.items) : []);
    } catch {
      if (consentSeq.current !== seq) return;
      setConsents([]);
    }
  }

  async function post(path: string, payload: object) {
    const tenantAtSubmit = tenant;
    const seqAtSubmit = loadSeq.current;
    setBusy(true);
    setError("");
    try {
      const res = await fetch(path, { method: "POST", headers: headers(), body: JSON.stringify(payload) });
      const data = (await res.json()) as OutboundTask & { message?: string };
      if (tenantRef.current !== tenantAtSubmit || loadSeq.current !== seqAtSubmit) return data;
      if (typeof data.id === "string") setTask(data);
      if (!res.ok) setError(failureText(data, res.status));
      return data;
    } catch (e) {
      if (tenantRef.current === tenantAtSubmit) setError(e instanceof Error ? productError(e.message) : "提交失败");
      return null;
    } finally {
      if (tenantRef.current === tenantAtSubmit) setBusy(false);
    }
  }

  const selected = contacts?.find((item) => item.id === contactId) ?? null;
  const selectedConsent = consents?.find((item) => item.id === consentId) ?? null;
  const campaignId = campaignChoice(selected);
  return (
    <main data-page="outbound">
      <header className="page-head">
        <h1>外呼安全门</h1>
        <Link className="btn" href="/">我的工作</Link>
      </header>
      {!tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <div className="card">
        <p data-testid="outbound-note" data-tone="automation">{view.note}</p>
        <p className="muted">
          <span data-tone="automation">自动化状态</span> 生产自动外呼：{view.productionAuto ? "开启" : "关闭"}。真实线路：{view.realLine ? "已接入" : "没有"}。
          费用：{view.cost === "unknown" ? "未知" : view.cost}。本次记录：{view.simulation ? "模拟" : "未标明"}。
        </p>
      </div>
      <div className="card">
        <h2>隔离演练</h2>
        <p className="muted">只写入模拟任务，不会自动发送，也不会扣费。公开号码和高分都不会变成拨打成功。拒绝之后换活动也不能再打。</p>
        <p>
          <label>
            客户{" "}
            <select
              value={contactId}
              onChange={(e) => {
                const id = e.target.value;
                setContactId(id);
                void loadConsents(tenant, id);
              }}
            >
              <option value="">选择客户</option>
              {contacts?.map((item) => (
                <option key={item.id} value={item.id}>{item.name}</option>
              ))}
            </select>
          </label>
        </p>
        {tenant && contacts && contacts.length === 0 ? <p className="muted">还没有可选的客户</p> : null}
        <p>
          <label>
            语音营销授权{" "}
            <select value={consentId} onChange={(e) => setConsentId(e.target.value)} disabled={!consents || consents.length === 0}>
              <option value="">不选择授权</option>
              {consents?.map((item) => (
                <option key={item.id} value={item.id}>{consentLabel(item)}</option>
              ))}
            </select>
          </label>
        </p>
        {selected && consents && consents.length === 0 ? <p className="muted">还没有可选择的授权</p> : null}
        {selected && !campaignId ? <p className="muted">还没有可选的活动</p> : null}
        {/* HUI-2626 fix2（gate-r2 #6 主行动收敛）：主行动只在可执行时出现；
            取消/转人工只在对已存在任务有意义时出现，不再是一排等权灰按钮。 */}
        {selected && campaignId ? (
          <Button
            variant="primary" size="sm"
            type="button"
            data-page-primary="true"
            disabled={busy}
            onClick={() => {
              const payload = outboundSubmit({
                contact: selected,
                consent: selectedConsent,
                newKey: () => crypto.randomUUID(),
              });
              if (!payload) return;
              void post("/api/outbound/tasks", payload);
            }}
          >
            {pagePrimary("outbound")}
          </Button>
        ) : (
          <p className="muted">选择客户后，这里记录一次模拟提交。</p>
        )}
        {task ? (
          <>
            {" "}
            <Button
              variant="secondary" size="sm"
              type="button"
              disabled={busy}
              onClick={() => void post(`/api/outbound/tasks/${encodeURIComponent(task.id)}/cancel`, {})}
            >
              取消
            </Button>{" "}
            <Button
              variant="secondary" size="sm"
              type="button"
              disabled={busy}
              onClick={() => void post(`/api/outbound/tasks/${encodeURIComponent(task.id)}/transfer`, {})}
            >
              转人工
            </Button>
          </>
        ) : null}
        {error ? <SurfaceState kind="error" title="外呼没有记下" detail={error} /> : null}
      </div>
      <div className="card">
        <h2>回执</h2>
        <p className="muted" data-testid="outbound-result">
          {/* HUI-2626 fix2（gate-r2 #7）：部分成功/部分失败是一态，由 lib 同一 composer 出词。 */}
          {outboundResultLine(task)}
        </p>
        <ul>
          {task?.receipts?.map((item) => (
            <li key={item.id}>
              {receiptLabel(item.kind, item.status)}
              {item.simulation ? " · 模拟" : ""}
            </li>
          ))}
        </ul>
      </div>
    </main>
  );
}
