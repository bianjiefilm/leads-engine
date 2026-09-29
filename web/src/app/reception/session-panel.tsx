"use client";

import { useCallback, useEffect, useState } from "react";
import {
  MODE_TEXT,
  REPLY_STATUS_TEXT,
  approveCall,
  assistCall,
  closeCall,
  followUpCall,
  followUpSubmitAllowed,
  humanReplyCall,
  leadCall,
  leadSubmitAllowed,
  newStaffToken,
  nextFollowUpRFC3339,
  releaseCall,
  replyApprovable,
  replySendable,
  sendCall,
  takeoverCall,
  type DeskCall,
  type LeadFields,
} from "@/lib/reception";

interface SessionView {
  id: string;
  mode: string;
  epoch: number;
  version: number;
  status: string;
  owner_member_id?: string;
  lead_id?: string;
  contact_id?: string;
  pending_reason?: string;
}

interface ReplyView {
  id: string;
  epoch: number;
  session_version?: number;
  kind: string;
  body: string;
  status: string;
  created_at?: string;
}

interface ModelView {
  task_id?: string;
  cost_cents?: number;
  billing_verdict?: string;
}

interface MessageView {
  id: string;
  body: string;
  created_at?: string;
}

const EMPTY_LEAD: LeadFields = {
  purpose: "",
  allowContact: false,
  contactName: "",
  phone: "",
  noticeVersion: "",
};

export default function SessionPanel({
  tenant,
  sessionId,
  onChanged,
}: {
  tenant: string;
  sessionId: string;
  onChanged: () => void;
}) {
  const [session, setSession] = useState<SessionView | null>(null);
  const [messages, setMessages] = useState<MessageView[]>([]);
  const [replies, setReplies] = useState<ReplyView[]>([]);
  const [model, setModel] = useState<ModelView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [humanText, setHumanText] = useState("");
  const [lead, setLead] = useState<LeadFields>(EMPTY_LEAD);
  const [note, setNote] = useState("");
  const [nextAt, setNextAt] = useState("");

  const load = useCallback(async () => {
    const res = await fetch(`/api/reception/sessions/${sessionId}`, {
      headers: { "x-tenant-id": tenant },
    });
    const body = await res.json();
    if (!res.ok) {
      setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      setSession(null);
      return;
    }
    setSession(body.session);
    setMessages(body.messages ?? []);
    setReplies(body.replies ?? []);
    setModel(body.model ?? null);
  }, [sessionId, tenant]);

  useEffect(() => {
    setError("");
    setLead(EMPTY_LEAD);
    setNote("");
    setNextAt("");
    setHumanText("");
    void load().catch((e: Error) => setError(e.message));
  }, [load]);

  async function run(call: DeskCall | null, refused: string) {
    if (!call) {
      setError(refused);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const res = await fetch(call.path, {
        method: call.method,
        headers: { "content-type": "application/json", "x-tenant-id": tenant },
        body: JSON.stringify(call.body),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(body.message ?? body.error ?? `HTTP ${res.status}`);
        return;
      }
      await load();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败");
    } finally {
      setBusy(false);
    }
  }

  async function sendHuman() {
    if (!session) return;
    const clientReplyId = newStaffToken("reply-");
    const created = humanReplyCall(session.id, clientReplyId, humanText);
    if (!created) {
      setError("先写人工回复");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const res = await fetch(created.path, {
        method: "POST",
        headers: { "content-type": "application/json", "x-tenant-id": tenant },
        body: JSON.stringify(created.body),
      });
      const body = await res.json();
      if (!res.ok) {
        setError(body.message ?? body.error ?? `HTTP ${res.status}`);
        return;
      }
      const replyId = body.reply?.id as string | undefined;
      if (!replyId) {
        setError("服务端没有返回回复");
        return;
      }
      const send = sendCall(session.id, replyId, newStaffToken("rcpt-"));
      const sent = await fetch(send!.path, {
        method: "POST",
        headers: { "content-type": "application/json", "x-tenant-id": tenant },
        body: JSON.stringify(send!.body),
      });
      const sentBody = await sent.json();
      if (!sent.ok) {
        setError(sentBody.message ?? sentBody.error ?? `HTTP ${sent.status}`);
        await load();
        return;
      }
      setHumanText("");
      await load();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "发送失败");
    } finally {
      setBusy(false);
    }
  }

  if (!session) {
    return <section className="card">{error ? <p className="error">{error}</p> : <p className="muted">正在读取会话。</p>}</section>;
  }

  const open = session.status === "open";
  const turns = [
    ...messages.map((message) => ({ key: message.id, at: message.created_at ?? "", who: "访客", body: message.body })),
    ...replies.map((reply) => ({ key: reply.id, at: reply.created_at ?? "", who: reply.kind === "human" ? "人工" : "AI", body: reply.body })),
  ].sort((a, b) => a.at.localeCompare(b.at));

  return (
    <section className="card" aria-label="会话操作">
      <h2>会话 <code>{session.id}</code></h2>
      <p className="muted">
        {MODE_TEXT[session.mode] ?? session.mode} · 轮次 {session.epoch} · 版本 {session.version} · {session.status === "open" ? "进行中" : "已结案"}
        {session.owner_member_id ? ` · 负责人 ${session.owner_member_id}` : ""}
        {session.lead_id ? ` · 线索 ${session.lead_id}` : " · 尚未受权留资"}
      </p>
      {error ? <p className="error">{error}</p> : null}
      <div className="chat-log" aria-label="会话记录">
        {turns.map((turn) => <div key={turn.key} className="bubble shop"><p>{turn.who}：{turn.body}</p></div>)}
        {turns.length === 0 ? <p className="muted">还没有对话。</p> : null}
      </div>
      <ul className="desk-list">
        {replies.map((reply) => {
          const stale = reply.epoch !== session.epoch && reply.status !== "sent";
          return (
            <li key={reply.id}>
              <span>{REPLY_STATUS_TEXT[reply.status] ?? reply.status}</span>
              <span> · 轮次 {reply.epoch} · 版本 {reply.session_version ?? ""}</span>
              {stale ? <span> · 旧轮次，文本和音频都不能再发送</span> : null}
              <div className="queue-actions">
                {replyApprovable(session.mode, reply.epoch, session.epoch, reply.status) ? (
                  <button type="button" disabled={busy} onClick={() => void run(approveCall(session.id, reply.id), "不能批准")}>批准</button>
                ) : null}
                {replySendable(reply.epoch, session.epoch, reply.status) ? (
                  <button type="button" disabled={busy} onClick={() => void run(sendCall(session.id, reply.id, newStaffToken("rcpt-")), "不能发送")}>发送</button>
                ) : null}
              </div>
            </li>
          );
        })}
      </ul>
      <p className="muted">
        {model?.billing_verdict === "recorded" && model.task_id
          ? `任务 ${model.task_id} · 成本 ${model.cost_cents ?? 0} 分`
          : "文本降级，真实模型未完成"}
      </p>
      <div className="queue-actions">
        {open && session.mode === "ai" ? (
          <button type="button" disabled={busy} onClick={() => void run(assistCall(session.id, session.epoch), "还没有服务端轮次")}>改为协助</button>
        ) : null}
        {open && session.mode !== "human" ? (
          <button className="primary" type="button" disabled={busy} onClick={() => void run(takeoverCall(session.id, session.epoch), "还没有服务端轮次")}>接管</button>
        ) : null}
        {open && session.mode === "human" ? (
          <button type="button" disabled={busy} onClick={() => void run(releaseCall(session.id, session.epoch), "还没有服务端轮次")}>交回 AI</button>
        ) : null}
        {open ? <button type="button" disabled={busy} onClick={() => void run(closeCall(session.id), "不能结案")}>结案</button> : null}
      </div>
      {open && session.mode === "human" ? (
        <form className="stack-form" onSubmit={(event) => { event.preventDefault(); void sendHuman(); }}>
          <label>
            人工回复
            <textarea aria-label="人工回复" value={humanText} onChange={(event) => setHumanText(event.target.value)} maxLength={2000} />
          </label>
          <button className="primary" type="submit" disabled={busy || humanText.trim() === ""}>发送人工回复</button>
        </form>
      ) : null}
      {open && !session.lead_id ? (
        <form className="stack-form" onSubmit={(event) => { event.preventDefault(); void run(leadCall(session.id, lead), "售后和一般问答不建线索"); }}>
          <h3>受权留资</h3>
          <p className="muted">售后和一般问答不建线索。销售跟进要由你勾选允许联系，并填写姓名、电话和告知版本。</p>
          <label>
            目的
            <select aria-label="目的" value={lead.purpose} onChange={(event) => setLead({ ...lead, purpose: event.target.value })}>
              <option value="">请选择</option>
              <option value="sales_followup">销售跟进</option>
              <option value="after_sales">售后</option>
              <option value="general_qa">一般问答</option>
            </select>
          </label>
          <label>
            <span>
              <input
                aria-label="允许联系"
                type="checkbox"
                checked={lead.allowContact}
                onChange={(event) => setLead({ ...lead, allowContact: event.target.checked })}
              />
              允许联系
            </span>
          </label>
          <label>
            姓名
            <input aria-label="姓名" value={lead.contactName} onChange={(event) => setLead({ ...lead, contactName: event.target.value })} />
          </label>
          <label>
            电话
            <input aria-label="电话" value={lead.phone} onChange={(event) => setLead({ ...lead, phone: event.target.value })} />
          </label>
          <label>
            告知版本
            <input aria-label="告知版本" value={lead.noticeVersion} onChange={(event) => setLead({ ...lead, noticeVersion: event.target.value })} placeholder="例如 reception-notice-v1" />
          </label>
          <button className="primary" type="submit" disabled={busy || !leadSubmitAllowed(lead)}>建立受权线索</button>
        </form>
      ) : null}
      {session.lead_id ? (
        <form className="stack-form" onSubmit={(event) => {
          event.preventDefault();
          const next = nextFollowUpRFC3339(nextAt);
          if (next === null) {
            setError("下一次跟进时间无效");
            return;
          }
          void run(followUpCall(session.id, session.lead_id, note, next), "跟进要先有受权线索");
        }}>
          <h3>跟进</h3>
          <label>
            跟进备注
            <textarea aria-label="跟进备注" value={note} onChange={(event) => setNote(event.target.value)} />
          </label>
          <label>
            下一次跟进
            <input aria-label="下一次跟进" type="datetime-local" value={nextAt} onChange={(event) => setNextAt(event.target.value)} />
          </label>
          <button className="primary" type="submit" disabled={busy || !followUpSubmitAllowed(session.lead_id, note)}>记录跟进</button>
        </form>
      ) : <p className="muted">跟进要先有受权线索。</p>}
    </section>
  );
}
