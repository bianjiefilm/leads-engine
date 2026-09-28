"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { presentOutbound, receiptLabel, type OutboundCapability } from "@/lib/outboundCall";

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

export default function OutboundPage() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState("");
  const [view, setView] = useState(EMPTY_VIEW);
  const [error, setError] = useState("");
  const [contactId, setContactId] = useState("");
  const [consentId, setConsentId] = useState("");
  const [taskKey, setTaskKey] = useState("");
  const [campaignId, setCampaignId] = useState("camp-a");
  const [task, setTask] = useState<OutboundTask | null>(null);
  const [busy, setBusy] = useState(false);

  const headers = useCallback(
    () => ({ "content-type": "application/json", "x-tenant-id": tenant.trim() }),
    [tenant],
  );

  const load = useCallback(async (tenantID: string) => {
    setError("");
    setView(EMPTY_VIEW);
    if (!tenantID.trim()) {
      setError("先填写当前租户");
      return;
    }
    try {
      const res = await fetch("/api/outbound/capability", { headers: { "x-tenant-id": tenantID.trim() } });
      const body = (await res.json()) as OutboundCapability & { message?: string; error?: string };
      setView(presentOutbound(body));
      if (!res.ok) setError(body.message ?? body.error ?? `HTTP ${res.status}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "加载失败");
    }
  }, []);

  useEffect(() => {
    setTenant(scope.tenantId ?? "");
    void load(scope.tenantId ?? "");
  }, [scope.epoch, scope.tenantId, load]);

  async function post(path: string, payload: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      const res = await fetch(path, { method: "POST", headers: headers(), body: JSON.stringify(payload) });
      const data = (await res.json()) as OutboundTask & { message?: string };
      if (typeof data.id === "string") setTask(data);
      if (!res.ok) setError(data.message ?? data.error ?? `HTTP ${res.status}`);
      return data;
    } catch (e) {
      setError(e instanceof Error ? e.message : "提交失败");
      return null;
    } finally {
      setBusy(false);
    }
  }

  const dial = {
    task_key: taskKey.trim(),
    contact_id: contactId.trim(),
    consent_id: consentId.trim(),
    campaign_id: campaignId.trim(),
    mode: "isolation",
    budget_cents: 0,
    op: "dial",
  };

  return (
    <main>
      <h1>外呼安全门</h1>
      <p>
        <Link href="/">我的工作</Link>
        {" · "}
        <Link href="/contacts">客户档案</Link>
        {" · "}
        <Link href="/sop">跟进提醒</Link>
      </p>
      <div className="card">
        <p data-testid="outbound-note">{view.note}</p>
        <p className="muted">
          生产自动外呼：{view.productionAuto ? "开启" : "关闭"}。真实线路：{view.realLine ? "已接入" : "没有"}。
          费用：{view.cost === "unknown" ? "未知" : view.cost}。本次记录：{view.simulation ? "模拟" : "未标明"}。
        </p>
        <label>
          当前租户{" "}
          <input value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="租户 id" />
        </label>{" "}
        <button
          type="button"
          onClick={() => {
            const value = tenant.trim();
            if (!value) {
              setError("先填写当前租户");
              return;
            }
            if (value === scope.tenantId) void load(value);
            else commitCrmTenant(value);
          }}
        >
          加载
        </button>
      </div>
      <div className="card">
        <h2>隔离演练</h2>
        <p className="muted">只写入模拟任务。公开号码和高分都不会变成拨打成功。拒绝之后换活动也不能再打。</p>
        <p>
          <label>
            联系人 <input value={contactId} onChange={(e) => setContactId(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            语音营销授权 <input value={consentId} onChange={(e) => setConsentId(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            任务键 <input value={taskKey} onChange={(e) => setTaskKey(e.target.value)} />
          </label>{" "}
          <label>
            活动 <input value={campaignId} onChange={(e) => setCampaignId(e.target.value)} />
          </label>
        </p>
        <button type="button" disabled={busy} onClick={() => void post("/api/outbound/tasks", dial)}>
          记录模拟提交
        </button>{" "}
        <button
          type="button"
          disabled={busy || !task?.id}
          onClick={() => void post(`/api/outbound/tasks/${encodeURIComponent(task?.id ?? "")}/cancel`, {})}
        >
          取消
        </button>{" "}
        <button
          type="button"
          disabled={busy || !task?.id}
          onClick={() => void post(`/api/outbound/tasks/${encodeURIComponent(task?.id ?? "")}/transfer`, {})}
        >
          转人工
        </button>
        {error ? <p>{error}</p> : null}
      </div>
      <div className="card">
        <h2>回执</h2>
        <p className="muted" data-testid="outbound-result">
          {task
            ? `模拟：${task.simulation ? "是" : "否"}。拨打成功：${task.dial_succeeded ? "是" : "否"}。真实接通：${task.real_connected ? "是" : "否"}。费用：${task.cost === "unknown" || !task.cost ? "未知" : task.cost}。`
            : "还没有任务。"}
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
