"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import {
  leadHref,
  marketingLabels,
  presentCapability,
  receiptLine,
  rowActions,
  type CapabilityView,
} from "@/lib/channelInteraction";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";

// 授权评论/私信（HUI-1681）。只打本站 BFF。
// 没有真实渠道凭证时保持未验证，不把发布授权当成私信权限，也不因分数自动触达。

interface InteractionItem {
  id: string;
  provider: string;
  account_id: string;
  event_id: string;
  kind: string;
  nickname: string;
  body: string;
  purpose: string;
  candidate_id?: string;
  candidate_status?: string;
  path: string;
  retracted: boolean;
  auto_reach: boolean;
  receipt_platform: boolean;
  receipt_persisted: boolean;
  receipt_human: boolean;
  channel_contact: boolean;
  phone_marketing: boolean;
  sms_marketing: boolean;
  score?: number;
}

const KIND_TEXT: Record<string, string> = {
  comment: "评论",
  direct_message: "私信",
};

const PURPOSE_TEXT: Record<string, string> = {
  sales_inquiry: "购买意向",
  support: "支持",
  general_qa: "一般问答",
  aftersales: "售后",
};

export default function ChannelInteractionsPage() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState("");
  const [view, setView] = useState<CapabilityView>(presentCapability(null));
  const [items, setItems] = useState<InteractionItem[] | null>(null);
  const [error, setError] = useState("");
  const [leadId, setLeadId] = useState("");
  const [provider, setProvider] = useState("connector");
  const [accountId, setAccountId] = useState("");
  const [appId, setAppId] = useState("");
  const [commentRead, setCommentRead] = useState(true);
  const [messageRead, setMessageRead] = useState(false);
  const [reply, setReply] = useState(false);
  const [publish, setPublish] = useState(false);
  const [kind, setKind] = useState("comment");
  const [eventId, setEventId] = useState("");
  const [subjectId, setSubjectId] = useState("");
  const [nickname, setNickname] = useState("");
  const [text, setText] = useState("");
  const [intent, setIntent] = useState(false);
  const [purpose, setPurpose] = useState("general_qa");

  const headers = useCallback(() => ({ "content-type": "application/json", "x-tenant-id": tenant.trim() }), [tenant]);

  const load = useCallback(async (tenantID: string) => {
    setError("");
    setItems(null);
    setView(presentCapability(null));
    if (!tenantID.trim()) {
      setItems([]);
      setError("先填写当前租户");
      return;
    }
    const h = { "x-tenant-id": tenantID.trim() };
    try {
      const [capRes, listRes] = await Promise.all([
        fetch("/api/channel-interactions/capability", { headers: h }),
        fetch("/api/channel-interactions", { headers: h }),
      ]);
      const capBody = await capRes.json();
      const listBody = await listRes.json();
      setView(presentCapability(capBody));
      if (!listRes.ok) {
        setError(listBody.message ?? listBody.error ?? `HTTP ${listRes.status}`);
        setItems([]);
        return;
      }
      setItems((listBody.items ?? []) as InteractionItem[]);
    } catch (e) {
      setError((e as Error).message);
      setItems([]);
    }
  }, []);

  useEffect(() => {
    setTenant(scope.tenantId ?? "");
    if (scope.tenantId) load(scope.tenantId);
    else {
      setItems([]);
      setError("先填写当前租户");
    }
  }, [scope.epoch, scope.tenantId, load]);

  const saveTenant = () => {
    const value = tenant.trim();
    if (!value) {
      setError("先填写当前租户");
      setItems([]);
      return;
    }
    if (value === scope.tenantId) load(value);
    else commitCrmTenant(value);
  };

  const capabilities = [
    commentRead ? "comment.read" : "",
    messageRead ? "message.read" : "",
    reply ? "reply" : "",
    publish ? "publish" : "",
  ].filter(Boolean);

  const saveGrant = async () => {
    setError("");
    const res = await fetch("/api/channel-grants", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify({
        provider,
        account_id: accountId,
        app_id: appId,
        subject_ns: `${provider}.subject`,
        message_ns: `${provider}.message`,
        post_ns: `${provider}.post`,
        capabilities,
      }),
    });
    const body = await res.json();
    if (!res.ok) {
      setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    await load(tenant.trim());
  };

  const saveEvent = async () => {
    setError("");
    const res = await fetch("/api/channel-interactions", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify({
        target_tenant_id: tenant.trim(),
        provider,
        account_id: accountId,
        app_id: appId,
        subject_ns: `${provider}.subject`,
        message_ns: `${provider}.message`,
        post_ns: `${provider}.post`,
        event_id: eventId,
        kind,
        subject_id: subjectId,
        nickname,
        text,
        explicit_intent: intent,
        purpose,
      }),
    });
    const body = await res.json();
    if (!res.ok) {
      setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    setText("");
    await load(tenant.trim());
  };

  const confirm = async (candidateId: string) => {
    setError("");
    const res = await fetch(`/api/channel-candidates/${candidateId}/confirm`, {
      method: "POST",
      headers: headers(),
    });
    const body = await res.json();
    if (!res.ok) {
      setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    setLeadId(typeof body.lead_id === "string" ? body.lead_id : "");
    await load(tenant.trim());
  };

  return (
    <main>
      <p>
        <Link href="/">返回工作台</Link>
      </p>
      <h1>授权互动</h1>
      <div className="card">
        <p>
          <strong>{view.label}</strong>。{view.note}
        </p>
        <p className="muted">发布授权不会带来私信权限。高分不会自动外呼、发短信或让两个机器人一起回复。</p>
        <label>
          当前租户{" "}
          <input value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="租户 id" />
        </label>{" "}
        <button type="button" onClick={saveTenant}>
          加载
        </button>
      </div>
      {error ? <p className="muted">{error}</p> : null}
      {leadId ? (
        <p>
          已确认到本租户线索。<Link href={leadHref(leadId)}>打开线索</Link>
        </p>
      ) : null}
      <div className="card">
        <h2>连接器适配</h2>
        <p className="muted">这只是未验证的适配登记，不会连接真实平台。</p>
        <p>
          <label>
            渠道标识 <input value={provider} onChange={(e) => setProvider(e.target.value)} />
          </label>{" "}
          <label>
            账号 <input value={accountId} onChange={(e) => setAccountId(e.target.value)} />
          </label>{" "}
          <label>
            授权应用 <input value={appId} onChange={(e) => setAppId(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            <input type="checkbox" checked={commentRead} onChange={(e) => setCommentRead(e.target.checked)} /> 评论读取
          </label>{" "}
          <label>
            <input type="checkbox" checked={messageRead} onChange={(e) => setMessageRead(e.target.checked)} /> 私信读取
          </label>{" "}
          <label>
            <input type="checkbox" checked={reply} onChange={(e) => setReply(e.target.checked)} /> 回复
          </label>{" "}
          <label>
            <input type="checkbox" checked={publish} onChange={(e) => setPublish(e.target.checked)} /> 发布（不含私信）
          </label>
        </p>
        <button type="button" onClick={saveGrant}>
          登记授权
        </button>
      </div>
      <div className="card">
        <h2>记录一条互动</h2>
        <p>
          <label>
            类型{" "}
            <select value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="comment">评论</option>
              <option value="direct_message">私信</option>
            </select>
          </label>{" "}
          <label>
            事件 ID <input value={eventId} onChange={(e) => setEventId(e.target.value)} />
          </label>{" "}
          <label>
            对方 ID <input value={subjectId} onChange={(e) => setSubjectId(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            昵称 <input value={nickname} onChange={(e) => setNickname(e.target.value)} />
          </label>{" "}
          <label>
            用途{" "}
            <select value={purpose} onChange={(e) => setPurpose(e.target.value)}>
              <option value="general_qa">一般问答</option>
              <option value="support">支持</option>
              <option value="aftersales">售后</option>
              <option value="sales_inquiry">购买意向</option>
            </select>
          </label>{" "}
          <label>
            <input type="checkbox" checked={intent} onChange={(e) => setIntent(e.target.checked)} /> 明确意向
          </label>
        </p>
        <p>
          <label>
            正文 <input value={text} onChange={(e) => setText(e.target.value)} />
          </label>
        </p>
        <button type="button" onClick={saveEvent}>
          接入
        </button>
      </div>
      <div className="card">
        {items === null ? (
          <p className="muted">加载中…</p>
        ) : items.length === 0 ? (
          <p className="muted">这个租户还没有授权互动。</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>类型</th>
                <th>昵称</th>
                <th>正文</th>
                <th>用途</th>
                <th>回执</th>
                <th>候选</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {items.map((row) => {
                const actions = rowActions(row);
                const marketing = marketingLabels({ phone: "", phone_marketing: row.phone_marketing, sms_marketing: row.sms_marketing });
                return (
                  <tr key={row.id}>
                    <td>{KIND_TEXT[row.kind] ?? row.kind}</td>
                    <td>{row.nickname || "未命名"}</td>
                    <td>{row.body}</td>
                    <td>{PURPOSE_TEXT[row.purpose] ?? row.purpose}</td>
                    <td>{receiptLine(row)}{row.retracted ? " · 已撤回" : ""}</td>
                    <td>{row.candidate_status === "open" ? "待确认" : row.candidate_status === "confirmed" ? "已确认" : row.candidate_status === "withdrawn" ? "已撤回" : "不是线索"}</td>
                    <td>
                      {actions.confirm && row.candidate_id ? (
                        <button type="button" onClick={() => confirm(row.candidate_id!)}>
                          确认线索
                        </button>
                      ) : null}
                      {actions.reach.length === 0 ? null : <span>触达</span>}
                      {marketing.length > 0 ? <span>{marketing.join("、")}</span> : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </main>
  );
}
