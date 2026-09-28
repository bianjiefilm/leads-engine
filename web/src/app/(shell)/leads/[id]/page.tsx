"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { NARROW_ACTIONS, sourceText, statusLine, type StatusFacts } from "@/lib/workbench";
import { LightCopyPanel } from "@/components/LightCopyPanel";

interface TimelineEvent {
  at: string;
  kind: string;
  summary: string;
  contact_plaintext?: string;
}

interface TimelineResponse {
  source?: { form?: string; activity?: string; channel?: string; at?: string };
  statuses?: StatusFacts;
  next?: { kind?: string; source?: string; label?: string; at?: string };
  events?: TimelineEvent[];
  message?: string;
  error?: string;
}

const KIND_LABEL: Record<string, string> = {
  source: "来源",
  assignment: "分配",
  follow_up: "跟进",
  opportunity: "商机",
  consent: "授权",
  system: "系统",
};

export default function LeadDeskPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const scope = useCrmScope();
  const [loadedFor, setLoadedFor] = useState("");
  const [body, setBody] = useState<TimelineResponse | null>(null);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [nextAt, setNextAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");

  useEffect(() => {
    setBody(null);
    setLoadedFor("");
    setErr("");
    if (!scope.tenantId || !id) {
      setErr("先填写当前租户");
      return;
    }
    const tenantId = scope.tenantId;
    let cancelled = false;
    fetch(`/api/leads/${id}/timeline`, { headers: { "x-tenant-id": tenantId } })
      .then(async (res) => {
        const payload = (await res.json()) as TimelineResponse;
        if (cancelled) return;
        if (!res.ok) {
          setErr(payload.message ?? payload.error ?? `HTTP ${res.status}`);
          setBody(null);
          setLoadedFor(tenantId);
          return;
        }
        setBody(payload);
        setLoadedFor(tenantId);
      })
      .catch((e: Error) => {
        if (!cancelled) setErr(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenantId, id]);

  const visible = loadedFor === scope.tenantId ? body : null;
  const channel = visible?.next?.kind === "channel_follow_up";

  async function submit(channelMode: boolean) {
    if (!scope.tenantId) return;
    setBusy(true);
    setMsg("");
    const when = nextAt ? new Date(nextAt).toISOString().replace(/\.\d{3}Z$/, "Z") : "";
    const payload: Record<string, unknown> = { note, complete: true };
    if (when) payload.next_follow_up_at = when;
    if (channelMode) payload.channel = "in_channel";
    try {
      const res = await fetch(`/api/leads/${id}/follow-through`, {
        method: "POST",
        headers: { "content-type": "application/json", "x-tenant-id": scope.tenantId },
        body: JSON.stringify(payload),
      });
      const out = await res.json();
      if (!res.ok) {
        setMsg(out.message ?? out.error ?? `HTTP ${res.status}`);
        return;
      }
      setNote("");
      setNextAt("");
      setMsg(out.next?.label ?? "已记下");
      setLoadedFor("");
      const again = await fetch(`/api/leads/${id}/timeline`, { headers: { "x-tenant-id": scope.tenantId } });
      const nextBody = await again.json();
      if (again.ok) setBody(nextBody);
      setLoadedFor(scope.tenantId);
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main>
      <p>
        <Link href="/">返回工作台</Link>
      </p>
      <h1>线索</h1>
      {err ? <p className="muted">{err}</p> : null}
      {visible === null && !err ? <p className="muted">加载中…</p> : null}
      {visible ? (
        <>
          <div className="card">
            <p>来源：{sourceText(visible.source)}</p>
            <p>状态：{statusLine(visible.statuses ?? {}) || "尚无权威状态"}</p>
            <p>下一步：{visible.next?.source === "manual" ? "手工安排" : "建议"} · {visible.next?.label || "安排下一次"}</p>
            {visible.next?.at ? <p className="muted">时间 {visible.next.at}</p> : null}
          </div>
          <div className="card">
            <h2>时间线</h2>
            {(visible.events ?? []).length === 0 ? <p className="muted">还没有记录。</p> : null}
            <ol className="desk-list">
              {(visible.events ?? []).map((event, index) => (
                <li key={`${event.kind}-${event.at}-${index}`}>
                  <p>
                    {KIND_LABEL[event.kind] ?? event.kind} · {event.summary}
                  </p>
                  <p className="muted">{event.at}</p>
                  {event.contact_plaintext ? <p>联系方式 {event.contact_plaintext}</p> : null}
                </li>
              ))}
            </ol>
          </div>
          {id ? <LightCopyPanel subjectKind="campaign" subjectId={id} /> : null}
          <form
            className="card stack-form"
            onSubmit={(e) => {
              e.preventDefault();
              void submit(false);
            }}
          >
            <h2>跟进</h2>
            <label>
              记录
              <textarea value={note} onChange={(e) => setNote(e.target.value)} placeholder="这次跟进了什么" />
            </label>
            <label>
              下一次
              <input type="datetime-local" value={nextAt} onChange={(e) => setNextAt(e.target.value)} />
            </label>
            <div className="queue-actions">
              <Link href={`/leads/${id}`}>{NARROW_ACTIONS[0]}</Link>
              <button className="primary" type="submit" disabled={busy || !note.trim()}>
                {NARROW_ACTIONS[1]}
              </button>
              <button className="primary" type="submit" disabled={busy || !note.trim() || !nextAt}>
                {NARROW_ACTIONS[2]}
              </button>
              {channel ? (
                <button className="primary" type="button" disabled={busy || !note.trim() || !nextAt} onClick={() => void submit(true)}>
                  安排渠道内跟进
                </button>
              ) : null}
            </div>
            {msg ? <p className="muted">{msg}</p> : null}
          </form>
        </>
      ) : null}
    </main>
  );
}
