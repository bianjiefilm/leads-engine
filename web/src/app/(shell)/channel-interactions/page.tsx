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
import { RecordFrame, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { Button, Checkbox, Input } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, productError } from "@/lib/productShell";

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
  const tenantId = scope.tenantId ?? "";
  const width = useShellWidth();
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

  const headers = useCallback(() => ({ "content-type": "application/json", "x-tenant-id": tenantId }), [tenantId]);

  const load = useCallback(async (tenantID: string) => {
    setError("");
    setItems(null);
    setView(presentCapability(null));
    if (!tenantID.trim()) {
      setItems([]);
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
        setError(failureText(listBody, listRes.status));
        setItems([]);
        return;
      }
      setItems((listBody.items ?? []) as InteractionItem[]);
    } catch (e) {
      setError(productError((e as Error).message));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    if (scope.tenantId) void load(scope.tenantId);
    else setItems([]);
  }, [scope.epoch, scope.tenantId, load]);

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
      setError(failureText(body, res.status));
      return;
    }
    await load(tenantId);
  };

  const saveEvent = async () => {
    setError("");
    const res = await fetch("/api/channel-interactions", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify({
        target_tenant_id: tenantId,
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
      setError(failureText(body, res.status));
      return;
    }
    setText("");
    await load(tenantId);
  };

  const confirm = async (candidateId: string) => {
    setError("");
    const res = await fetch(`/api/channel-candidates/${candidateId}/confirm`, {
      method: "POST",
      headers: headers(),
    });
    const body = await res.json();
    if (!res.ok) {
      setError(failureText(body, res.status));
      return;
    }
    setLeadId(typeof body.lead_id === "string" ? body.lead_id : "");
    await load(tenantId);
  };

  return (
    <main data-page="channel">
      <header className="page-head">
        <h1>授权互动</h1>
        <Link className="btn" href="/">返回工作台</Link>
      </header>
      {!tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <div className="card">
        <p>
          <strong>{view.label}</strong>。{view.note}
        </p>
        <p className="muted" data-tone="automation">发布授权不会带来私信权限。高分不会自动外呼、发短信或让两个机器人一起回复。</p>
      </div>
      {error ? <SurfaceState kind="error" title="互动没有载入" detail={error} /> : null}
      {leadId ? (
        <p>
          已确认到本租户线索。<Link href={leadHref(leadId)}>打开线索</Link>
        </p>
      ) : null}
      <div className="card">
        <h2>连接器适配</h2>
        <p className="muted">这只是未验证的适配登记，不会连接真实平台。</p>
        <p>
          <Input label="渠道标识" value={provider} onChange={(e) => setProvider(e.target.value)} />
          <Input label="账号" value={accountId} onChange={(e) => setAccountId(e.target.value)} />
          <Input label="授权应用" value={appId} onChange={(e) => setAppId(e.target.value)} />
        </p>
        <p>
          <Checkbox label="评论读取" checked={commentRead} onCheckedChange={(checked) => setCommentRead(checked)} />
          <Checkbox label="私信读取" checked={messageRead} onCheckedChange={(checked) => setMessageRead(checked)} />
          <Checkbox label="回复" checked={reply} onCheckedChange={(checked) => setReply(checked)} />
          <Checkbox label="发布（不含私信）" checked={publish} onCheckedChange={(checked) => setPublish(checked)} />
        </p>
        <Button variant="secondary" size="sm" type="button" onClick={saveGrant}>
          登记授权
        </Button>
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
            <Input label="事件 ID" value={eventId} onChange={(e) => setEventId(e.target.value)} />
          </label>{" "}
          <label>
            <Input label="对方 ID" value={subjectId} onChange={(e) => setSubjectId(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            <Input label="昵称" value={nickname} onChange={(e) => setNickname(e.target.value)} />
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
          <Checkbox label="明确意向" checked={intent} onCheckedChange={(checked) => setIntent(checked)} />
        </p>
        <p>
          <label>
            <Input label="正文" value={text} onChange={(e) => setText(e.target.value)} />
          </label>
        </p>
        <Button variant="primary" size="sm" type="button" data-page-primary="true" onClick={saveEvent}>
          接入
        </Button>
      </div>
      <div className="card">
        {items === null ? (
          <SurfaceState kind="loading" title="正在读取互动" detail="授权和回执马上就位。" />
        ) : items.length === 0 ? (
          <SurfaceState kind="empty" title="还没有授权互动" detail="登记授权并接入一条互动后会出现在这里。" />
        ) : (
          <div className="record-list">
            {items.map((row) => {
              const actions = rowActions(row);
              const marketing = marketingLabels({ phone: "", phone_marketing: row.phone_marketing, sms_marketing: row.sms_marketing });
              const candidate = row.candidate_status === "open" ? "待确认" : row.candidate_status === "confirmed" ? "已确认" : row.candidate_status === "withdrawn" ? "已撤回" : "不是线索";
              return (
                <RecordFrame
                  key={row.id}
                  width={width}
                  tone="human"
                  title={row.nickname || "未命名"}
                  facts={[
                    { label: "类型", value: KIND_TEXT[row.kind] ?? row.kind },
                    { label: "正文", value: row.body || "无" },
                    { label: "用途", value: PURPOSE_TEXT[row.purpose] ?? row.purpose },
                    { label: "回执", value: `${receiptLine(row)}${row.retracted ? " · 已撤回" : ""}` },
                    { label: "候选", value: candidate },
                    ...(marketing.length > 0 ? [{ label: "营销", value: marketing.join("、") }] : []),
                  ]}
                  secondary={actions.confirm && row.candidate_id ? (
                    <Button variant="secondary" size="sm" type="button" onClick={() => confirm(row.candidate_id!)}>
                      确认线索
                    </Button>
                  ) : undefined}
                >
                  {actions.reach.length === 0 ? null : <p data-tone="automation">触达未开放</p>}
                </RecordFrame>
              );
            })}
          </div>
        )}
      </div>
    </main>
  );
}
