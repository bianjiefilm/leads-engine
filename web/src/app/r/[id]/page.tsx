"use client";

import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { MODE_TEXT, PENDING_TEXT, RECEPTION_GREETING, newVisitorKey } from "@/lib/reception";

// 自有 H5 接待（HUI-1688）。只打本站 BFF。访客钥匙留在本机，不注册账号。

interface PublicWidget {
  id: string;
  language: string;
  greeting: string;
}

interface PublicSession {
  id: string;
  mode: string;
  status: string;
  pending_reason?: string;
  language: string;
}

interface TranscriptReply {
  id: string;
  body: string;
  status: string;
  created_at?: string;
  citations?: Array<{ source_id?: string; version?: number; kind?: string }>;
  gaps?: Array<{ code?: string }>;
}

interface TranscriptMessage {
  id: string;
  body: string;
  created_at?: string;
}

const GAP_TEXT: Record<string, string> = {
  fact_missing: "没有实时依据",
  fact_expired: "依据已过期",
  fact_timeout: "实时接口超时",
  unknown: "知识不足",
  untrusted_input: "已忽略注入指令",
  unapproved_tool: "不能执行写入",
  model_unavailable: "模型不可用",
};

export default function ReceptionH5Page() {
  const params = useParams<{ id: string }>();
  const widgetID = typeof params?.id === "string" ? params.id : "";
  const [widget, setWidget] = useState<PublicWidget | null>(null);
  const [session, setSession] = useState<PublicSession | null>(null);
  const [messages, setMessages] = useState<TranscriptMessage[]>([]);
  const [replies, setReplies] = useState<TranscriptReply[]>([]);
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);

  const visitorKey = useCallback(() => {
    const storageKey = "reception-visitor:" + widgetID;
    const existing = window.sessionStorage.getItem(storageKey);
    if (existing) return existing;
    const created = newVisitorKey();
    window.sessionStorage.setItem(storageKey, created);
    return created;
  }, [widgetID]);

  const loadTranscript = useCallback(async (sessionID: string, key: string, quiet = false) => {
    const res = await fetch(`/api/public/reception/sessions/${sessionID}?visitor_key=${encodeURIComponent(key)}`);
    const body = await res.json();
    if (!res.ok) {
      if (!quiet) setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    setSession(body.session);
    setMessages(body.messages ?? []);
    setReplies(body.replies ?? []);
  }, []);

  useEffect(() => {
    if (!session?.id) return;
    const timer = window.setInterval(() => {
      void loadTranscript(session.id, visitorKey(), true).catch(() => undefined);
    }, 2500);
    return () => window.clearInterval(timer);
  }, [session?.id, visitorKey, loadTranscript]);

  useEffect(() => {
    if (!widgetID) return;
    let cancelled = false;
    (async () => {
      setError("");
      const widgetRes = await fetch(`/api/public/reception/widgets/${widgetID}`);
      const widgetBody = await widgetRes.json();
      if (!widgetRes.ok) {
        if (!cancelled) setError(widgetBody.message ?? widgetBody.error ?? `HTTP ${widgetRes.status}`);
        return;
      }
      if (cancelled) return;
      setWidget(widgetBody);
      const key = visitorKey();
      const openRes = await fetch(`/api/public/reception/widgets/${widgetID}/sessions`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ visitor_key: key }),
      });
      const openBody = await openRes.json();
      if (!openRes.ok) {
        if (!cancelled) setError(openBody.message ?? openBody.error ?? `HTTP ${openRes.status}`);
        return;
      }
      if (cancelled) return;
      setSession(openBody.session);
      await loadTranscript(openBody.session.id, key);
    })().catch((e: Error) => setError(e.message));
    return () => {
      cancelled = true;
    };
  }, [widgetID, visitorKey, loadTranscript]);

  const send = async () => {
    if (!session || session.status === "closed" || !text.trim()) return;
    setSending(true);
    setError("");
    try {
      const key = visitorKey();
      const res = await fetch(`/api/public/reception/sessions/${session.id}/messages`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          visitor_key: key,
          client_msg_id: "web-" + Date.now().toString(36) + "-" + Math.random().toString(16).slice(2, 8),
          text: text.trim(),
        }),
      });
      const body = await res.json();
      if (!res.ok) {
        setError(body.message ?? body.error ?? `HTTP ${res.status}`);
        return;
      }
      setText("");
      if (body.session) setSession(body.session);
      await loadTranscript(session.id, key);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSending(false);
    }
  };

  const mode = session?.mode ?? "ai";
  return (
    <main className="chat-shell">
      <p className="muted"><a href="/">返回工作台</a></p>
      <h1>在线接待</h1>
      <p className="muted">{widget?.greeting || RECEPTION_GREETING}</p>
      <div className="chat-meta">
        <span className="badge">{MODE_TEXT[mode] ?? mode}</span>
        {session?.status === "closed" ? <span className="badge">已结案</span> : null}
        {session?.pending_reason ? <span className="badge warn">{PENDING_TEXT[session.pending_reason] ?? session.pending_reason}</span> : null}
      </div>
      {error ? <p className="error">{error}</p> : null}
      <div className="chat-log" aria-live="polite">
        {[
          ...messages.map((message) => ({ key: message.id, side: "visitor" as const, at: message.created_at ?? "", body: message.body, reply: undefined as TranscriptReply | undefined })),
          ...replies.filter((reply) => reply.status === "sent" || reply.status === "generated").map((reply) => ({ key: reply.id, side: "shop" as const, at: reply.created_at ?? "", body: reply.body, reply })),
        ]
          .sort((a, b) => a.at.localeCompare(b.at))
          .map((turn) => (
            <div key={turn.key} className={turn.side === "visitor" ? "bubble visitor" : "bubble shop"}>
              <p>{turn.body}</p>
              {turn.reply?.citations?.length ? (
                <p className="fine">依据 {turn.reply.citations.map((item) => `${item.kind ?? "faq"} ${item.source_id ?? ""} v${item.version ?? ""}`).join("、")}</p>
              ) : null}
              {turn.reply?.gaps?.length ? (
                <p className="fine">{turn.reply.gaps.map((gap) => GAP_TEXT[gap.code ?? ""] ?? gap.code).join(" · ")}</p>
              ) : null}
            </div>
          ))}
        {messages.length === 0 ? <p className="muted">可以直接提问。价格和订单不会凭文档编造。</p> : null}
      </div>
      <form className="composer" onSubmit={(event) => { event.preventDefault(); void send(); }}>
        <input
          value={text}
          onChange={(event) => setText(event.target.value)}
          placeholder="输入问题"
          aria-label="输入问题"
          maxLength={2000}
          disabled={session?.status === "closed"}
        />
        <button className="primary" type="submit" disabled={sending || !session || session.status === "closed"}>
          {sending ? "发送中" : "发送"}
        </button>
        <button
          type="button"
          disabled={!session}
          onClick={() => {
            if (!session) return;
            void loadTranscript(session.id, visitorKey()).catch((e: Error) => setError(e.message));
          }}
        >
          刷新对话
        </button>
      </form>
    </main>
  );
}
