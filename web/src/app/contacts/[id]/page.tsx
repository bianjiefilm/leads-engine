"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import {
  canDeleteContact,
  canEditContact,
  consentStatusLabel,
  marketingSummaryLine,
  parseTags,
  type ConsentRow,
  type ConsentSummary,
  type FollowupRow,
} from "@/lib/contact";

// 客户档案详情(HUI-1691 / FEAT-0192):基本信息/联系方式/标签/备注、
// consent 来源明细(每来源独立可查、独立撤销,撤销后显示「已撤销(不可恢复)」,
// 界面绝不提供恢复授权入口)、跟进时间线(只追加)。
// 编辑 / 停止营销按钮按 whoami(role×assignee)显隐 —— 仅 UI 镜像,
// 服务端对每个动作重新鉴权(非 assignee 的档案对本角色直接 404)。

interface ContactDetail {
  id: string;
  name: string;
  phone: string;
  email: string;
  business_category: string;
  source_type: string;
  consent_status: string;
  notes: string;
  tags: string;
  assigned_member_id?: string;
  created_at: string;
}

interface Whoami {
  role?: string;
  member_id?: string;
  enabled?: boolean;
  error?: string;
}

const CATEGORY_TEXT: Record<string, string> = {
  merchant_customer: "商家经营销售",
  creative_service: "创意服务",
};

const SOURCE_TEXT: Record<string, string> = {
  manual: "手工录入",
  form: "表单",
  touch_campaign: "活动触达",
};

export default function ContactDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const [contact, setContact] = useState<ContactDetail | null>(null);
  const [consents, setConsents] = useState<ConsentRow[]>([]);
  const [summary, setSummary] = useState<ConsentSummary | null>(null);
  const [followups, setFollowups] = useState<FollowupRow[]>([]);
  const [me, setMe] = useState<Whoami | null>(null);
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const [followupNote, setFollowupNote] = useState("");
  const [edit, setEdit] = useState({ name: "", phone: "", email: "", notes: "", tags: "" });

  const load = useCallback(async () => {
    try {
      const [whoRes, cRes, consRes, fuRes] = await Promise.all([
        fetch("/api/whoami"),
        fetch(`/api/contacts/${id}`),
        fetch(`/api/contacts/${id}/consents`),
        fetch(`/api/contacts/${id}/followups`),
      ]);
      if (whoRes.ok) setMe(await whoRes.json());
      const cBody = await cRes.json();
      if (!cRes.ok) {
        setMsg(cBody.message ?? `HTTP ${cRes.status}`);
        setContact(null);
        return;
      }
      setContact(cBody);
      setEdit({ name: cBody.name ?? "", phone: cBody.phone ?? "", email: cBody.email ?? "",
        notes: cBody.notes ?? "", tags: cBody.tags ?? "" });
      if (consRes.ok) {
        const b = await consRes.json();
        setConsents(b.items ?? []);
        setSummary(b.summary ?? null);
      }
      if (fuRes.ok) {
        const b = await fuRes.json();
        setFollowups(b.items ?? []);
      }
    } catch (e) {
      setMsg((e as Error).message);
    }
  }, [id]);

  useEffect(() => {
    if (id) load();
  }, [id, load]);

  const post = async (path: string, body: unknown) => {
    setBusy(true);
    setMsg("");
    try {
      const res = await fetch(`/api/contacts/${id}${path}`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(body),
      });
      const b = await res.json();
      if (!res.ok) {
        setMsg(b.message ?? b.error ?? `HTTP ${res.status}`);
        return;
      }
      await load();
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const patchProfile = async () => {
    setBusy(true);
    setMsg("");
    try {
      const res = await fetch(`/api/contacts/${id}`, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(edit),
      });
      const b = await res.json();
      if (!res.ok) {
        setMsg(b.message ?? b.error ?? `HTTP ${res.status}`);
        return;
      }
      setMsg("档案已更新");
      await load();
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const removeProfile = async () => {
    if (!window.confirm("确认删除该客户档案?删除后所有角色不可再访问,授权与跟进记录将依法保留最小审计。")) return;
    setBusy(true);
    try {
      const res = await fetch(`/api/contacts/${id}`, { method: "DELETE" });
      const b = await res.json();
      if (!res.ok) {
        setMsg(b.message ?? b.error ?? `HTTP ${res.status}`);
        return;
      }
      setMsg("已删除(软删除:档案脱敏,授权/跟进记录保留审计)");
      setContact(null);
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (!contact && !msg) return <main><p className="muted">加载中…</p></main>;
  if (!contact) {
    return (
      <main>
        <h1>客户档案详情</h1>
        <p className="muted">不可见或不存在:{msg}(权限外档案一律 404,不泄露存在性)</p>
        <Link href="/contacts">返回客户档案列表</Link>
      </main>
    );
  }

  const allowed = canEditContact(me ?? {}, contact);
  const isOwner = canDeleteContact(me ?? {});
  const tags = parseTags(contact.tags);

  return (
    <main>
      <h1>{contact.name}</h1>
      <p className="muted">
        类别:{CATEGORY_TEXT[contact.business_category] ?? contact.business_category} · 来源:{SOURCE_TEXT[contact.source_type] ?? contact.source_type} ·
        档案级授权:{contact.consent_status}
      </p>

      <div className="card">
        <h2>基本信息与联系方式</h2>
        <ul>
          <li>姓名:{contact.name}</li>
          <li>手机号:{contact.phone || "(未填)"}</li>
          <li>邮箱:{contact.email || "(未填)"}</li>
          <li>标签:{tags.length > 0 ? tags.join(" / ") : "(无)"}</li>
          <li>备注:{contact.notes || "(无)"}</li>
          <li className="muted">创建于 {contact.created_at}</li>
        </ul>
        <p className="muted">联系方式仅存档于本商家租户,不作为平台账号注册依据;日志与 URL 均不承载明文。</p>
      </div>

      {allowed ? (
        <div className="card">
          <h2>编辑档案</h2>
          <div>
            <input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="姓名" />
            <input value={edit.phone} onChange={(e) => setEdit({ ...edit, phone: e.target.value })} placeholder="手机号" />
            <input value={edit.email} onChange={(e) => setEdit({ ...edit, email: e.target.value })} placeholder="邮箱" />
          </div>
          <div>
            <input value={edit.tags} onChange={(e) => setEdit({ ...edit, tags: e.target.value })} placeholder="标签(逗号分隔)" />
          </div>
          <textarea value={edit.notes} onChange={(e) => setEdit({ ...edit, notes: e.target.value })} placeholder="备注" />
          <div>
            <button disabled={busy} onClick={patchProfile}>保存修改</button>{" "}
            <button disabled={busy} onClick={() => post("/revoke-marketing", { reason: "商家在档案页停止营销" })}>
              停止营销(撤销全部来源授权)
            </button>
          </div>
          <p className="muted">停止营销对全部来源生效且不可恢复:此后重放任何历史授权事件都不会恢复营销许可。</p>
        </div>
      ) : (
        <p className="muted">无编辑权限(仅档案负责人或租户 owner 可修改)。</p>
      )}

      <div className="card">
        <h2>授权(consent)来源明细</h2>
        {summary ? <p className="muted">{marketingSummaryLine(summary)}</p> : null}
        {consents.length === 0 ? <p className="muted">暂无来源授权记录。</p> : null}
        <ul>
          {consents.map((c) => (
            <li key={c.id}>
              来源提交 <code>{c.source_submission_ref}</code>(渠道 {c.source_channel || "未填"}) ·
              告知版本 {c.notice_version || "未填"} · 用途 {c.purpose} ·{" "}
              <strong>{consentStatusLabel(c)}</strong>
              {c.revoked_at ? `(撤销于 ${c.revoked_at},原因:${c.revoked_reason || "未记录"})` : null}
              {allowed && !c.revoked_at ? (
                <>
                  {" "}
                  <button disabled={busy} onClick={() => post(`/consents/${c.id}/revoke`, { reason: "档案页撤销该来源授权" })}>
                    撤销此来源
                  </button>
                </>
              ) : null}
            </li>
          ))}
        </ul>
        {allowed ? (
          <p className="muted">撤销是持久标记:同来源提交的事件再次到达只会原样返回,不能恢复授权;新授权需新的来源提交。</p>
        ) : null}
      </div>

      <div className="card">
        <h2>跟进时间线</h2>
        <ol>
          {followups.map((f) => (
            <li key={f.id}>
              {f.created_at} · 成员 <code>{f.member_id}</code>: {f.note}
            </li>
          ))}
        </ol>
        {followups.length === 0 ? <p className="muted">暂无跟进记录。</p> : null}
        {allowed ? (
          <div>
            <textarea value={followupNote} onChange={(e) => setFollowupNote(e.target.value)} placeholder="追加跟进(只追加,不可修改;全文不入日志)" />
            <div>
              <button disabled={busy || !followupNote.trim()} onClick={async () => { await post("/followups", { note: followupNote }); setFollowupNote(""); }}>
                追加跟进
              </button>
            </div>
          </div>
        ) : null}
      </div>

      {isOwner ? (
        <div className="card">
          <h2>删除档案(owner)</h2>
          <p className="muted">
            软删除:档案脱敏占位并对所有角色不可见;授权与跟进记录依法保留最小审计。
            与「停止营销」相互独立:删除不复权,停止营销不删档。
          </p>
          <button disabled={busy} onClick={removeProfile}>删除该档案</button>
        </div>
      ) : null}

      {msg ? <p className="muted">{msg}</p> : null}
      <p>
        <Link href="/contacts">返回客户档案列表</Link>
      </p>
    </main>
  );
}
