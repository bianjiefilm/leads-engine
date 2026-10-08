"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { RecordFrame, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { Button } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, pagePrimary, productError } from "@/lib/productShell";
import {
  NARROW_ACTIONS,
  allowedContactText,
  deskState,
  jointChainLine,
  modelAdviceLine,
  nextLine,
  ownerLine,
  sourceText,
  syncLine,
  visibleOutreach,
  type StatusFacts,
} from "@/lib/workbench";
import { CampaignMotionPanel } from "@/components/CampaignMotionPanel";
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
  owner_label?: string;
  assignment_reason?: string;
  allowed_contacts?: string[];
  sync?: { crm?: string };
  outreach_notice?: string;
  joint_chain?: { status?: string; label?: string };
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
      setErr("");
      return;
    }
    const tenantId = scope.tenantId;
    let cancelled = false;
    fetch(`/api/leads/${id}/timeline`, { headers: { "x-tenant-id": tenantId } })
      .then(async (res) => {
        const payload = (await res.json()) as TimelineResponse;
        if (cancelled) return;
        if (!res.ok) {
          setErr(failureText(payload, res.status));
          setBody(null);
          setLoadedFor(tenantId);
          return;
        }
        setBody(payload);
        setLoadedFor(tenantId);
      })
      .catch((e: Error) => {
        if (!cancelled) setErr(productError(e.message));
      });
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenantId, id]);

  const width = useShellWidth();
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
        setMsg(failureText(out, res.status));
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
      setMsg(productError((e as Error).message));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main data-page="lead-detail">
      <header className="page-head">
        <h1>线索</h1>
        <Link className="btn" href="/">返回工作台</Link>
      </header>
      {!scope.tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {err ? <SurfaceState kind="error" title="这条线索没有打开" detail={err} /> : null}
      {scope.tenantId && visible === null && !err ? <SurfaceState kind="loading" title="正在打开线索" detail="来源、负责人和下一步马上就位。" /> : null}
      {visible ? (
        <>
          <RecordFrame
            width={width}
            tone="human"
            title="这条线索"
            facts={[
              { label: "来源", value: sourceText(visible.source) },
              { label: "负责人", value: ownerLine(visible.owner_label, visible.assignment_reason) },
              { label: "允许的联系方式", value: allowedContactText(visible.allowed_contacts) },
              { label: "状态", value: `状态：${deskState(visible.statuses, visible.sync)}` },
              { label: "同步", value: syncLine(visible.sync) },
              { label: "下一步", value: nextLine(visible.next) },
              { label: "建议", value: modelAdviceLine() },
              ...(visibleOutreach(visible.outreach_notice) ? [{ label: "触达", value: visibleOutreach(visible.outreach_notice) }] : []),
              ...(visible.next?.at ? [{ label: "时间", value: visible.next.at }] : []),
            ]}
          >
            <p data-joint-chain="incomplete">{jointChainLine(visible.joint_chain)}</p>
          </RecordFrame>
          <div className="card">
            <h2>时间线</h2>
            {(visible.events ?? []).length === 0 ? <SurfaceState kind="empty" title="还没有记录" detail="记一次跟进后，时间线会出现在这里。" /> : null}
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
          {id ? <CampaignMotionPanel campaignId={id} /> : null}
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
              <Link className="btn" href={`/leads/${id}`} data-desk-action={NARROW_ACTIONS[0]}>{NARROW_ACTIONS[0]}</Link>
              <Button variant="primary" type="submit" size="sm" data-page-primary="true" data-desk-action={NARROW_ACTIONS[1]} disabled={busy || !note.trim()}>
                {pagePrimary("lead-detail")}
              </Button>
              <Button variant="secondary" type="submit" size="sm" data-desk-action={NARROW_ACTIONS[2]} disabled={busy || !note.trim() || !nextAt}>
                {NARROW_ACTIONS[2]}
              </Button>
              {channel ? (
                <Button variant="ghost" type="button" size="sm" disabled={busy || !note.trim() || !nextAt} onClick={() => void submit(true)}>
                  安排渠道内跟进
                </Button>
              ) : null}
            </div>
            {msg ? <p className="muted">{msg}</p> : null}
          </form>
        </>
      ) : null}
    </main>
  );
}
