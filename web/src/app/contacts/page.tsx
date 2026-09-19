"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { canExportContacts, searchGuard } from "@/lib/contact";

// 客户档案列表(HUI-1691 / FEAT-0192)。搜索只按姓名/标签 —— 手机号不作为
// 搜索参数、不进 URL(服务端对疑似手机号输入一律 400,前端同规则先行拦截);
// 导出为 owner 专属动作(服务端重新鉴权);BFF 零业务判断。

interface ContactRow {
  id: string;
  name: string;
  business_category: string;
  source_type: string;
  consent_status: string;
  tags: string;
  assigned_member_id?: string;
}

interface Whoami {
  role?: string;
  enabled?: boolean;
  error?: string;
}

const CATEGORY_TEXT: Record<string, string> = {
  merchant_customer: "商家经营销售",
  creative_service: "创意服务",
};

export default function ContactsPage() {
  const [items, setItems] = useState<ContactRow[] | null>(null);
  const [me, setMe] = useState<Whoami | null>(null);
  const [name, setName] = useState("");
  const [tag, setTag] = useState("");
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({
    name: "", phone: "", email: "", business_category: "merchant_customer",
    source_type: "manual", consent_status: "pending", notes: "", tags: "",
  });

  const load = useCallback(async (q: string) => {
    setError("");
    try {
      const res = await fetch(`/api/contacts${q}`);
      const body = await res.json();
      if (!res.ok) {
        setError(body.message ?? body.error ?? `HTTP ${res.status}`);
        setItems([]);
        return;
      }
      setItems(body.items ?? []);
    } catch (e) {
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    load("");
    fetch("/api/whoami")
      .then(async (res) => (res.ok ? setMe(await res.json()) : setMe({})))
      .catch(() => setMe({}));
  }, [load]);

  const search = () => {
    const guard = searchGuard(name, tag);
    if (guard) {
      setError(guard);
      return;
    }
    const params = new URLSearchParams();
    if (name.trim()) params.set("name", name.trim());
    if (tag.trim()) params.set("tag", tag.trim());
    const q = params.toString();
    load(q ? `?${q}` : "");
  };

  const create = async () => {
    setCreating(true);
    setError("");
    try {
      const res = await fetch("/api/contacts", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(form),
      });
      const body = await res.json();
      if (!res.ok) {
        setError(body.message ?? body.error ?? `HTTP ${res.status}`);
        return;
      }
      setForm({ ...form, name: "", phone: "", email: "", notes: "", tags: "" });
      await load("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setCreating(false);
    }
  };

  const exportCsv = () => {
    // GET 导出走 BFF;format=csv 必填。owner 专属,服务端重新鉴权。
    window.location.href = "/api/contacts/export?format=csv";
  };

  return (
    <main>
      <h1>客户档案</h1>
      <p className="muted">
        档案、联系方式、来源、标签、授权(consent)与跟进均归属当前商家租户;
        服务端同时核验租户成员资格与销售记录权限。平台账号与 CRM 联系人是不同对象:
        联系人手机号不会自动注册平台账号。搜索只按姓名/标签,不按手机号;查档请用联系人链接。
      </p>

      <div className="card">
        <h2>搜索</h2>
        <input placeholder="按姓名搜索" value={name} onChange={(e) => setName(e.target.value)} />
        <input placeholder="按标签搜索(如 vip)" value={tag} onChange={(e) => setTag(e.target.value)} />
        <button onClick={search}>搜索</button>{" "}
        <button onClick={() => { setName(""); setTag(""); load(""); }}>重置</button>
        {me && canExportContacts(me) ? (
          <>
            {" "}
            <button onClick={exportCsv}>导出 CSV(owner)</button>
          </>
        ) : null}
        <p className="muted">不支持按手机号搜索或导出筛选;导出仅 owner 可用且会在服务端留痕。</p>
      </div>

      {error ? <p className="muted">{error}</p> : null}

      <div className="card">
        <h2>新建档案</h2>
        <p className="muted">
          来源类型为 form / touch_campaign 时必须提供来源留痕(source_app/source_ref),否则服务端 400。
        </p>
        <div>
          <input placeholder="姓名(必填)" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <input placeholder="手机号(仅存档,不用于搜索)" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} />
          <input placeholder="邮箱" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
        </div>
        <div>
          <select value={form.business_category} onChange={(e) => setForm({ ...form, business_category: e.target.value })}>
            <option value="merchant_customer">商家经营销售</option>
            <option value="creative_service">创意服务</option>
          </select>
          <select value={form.source_type} onChange={(e) => setForm({ ...form, source_type: e.target.value })}>
            <option value="manual">手工录入</option>
            <option value="form">表单</option>
            <option value="touch_campaign">活动触达</option>
          </select>
          <select value={form.consent_status} onChange={(e) => setForm({ ...form, consent_status: e.target.value })}>
            <option value="pending">授权:待确认</option>
            <option value="granted">授权:已同意</option>
            <option value="denied">授权:已拒绝</option>
          </select>
        </div>
        <div>
          <input placeholder="标签(逗号分隔,如 vip,重点)" value={form.tags} onChange={(e) => setForm({ ...form, tags: e.target.value })} />
        </div>
        <textarea placeholder="备注(不超过 2000 字)" value={form.notes} onChange={(e) => setForm({ ...form, notes: e.target.value })} />
        <div>
          <button disabled={creating || !form.name.trim()} onClick={create}>创建档案</button>
        </div>
      </div>

      {items === null ? <p className="muted">加载中…</p> : null}
      {items !== null && items.length === 0 && !error ? (
        <p className="muted">暂无档案(或无权查看他人名下档案:非 assignee 一律不可见)。</p>
      ) : null}
      <ul>
        {(items ?? []).map((c) => (
          <li key={c.id}>
            <Link href={`/contacts/${c.id}`}>{c.name}</Link> — {CATEGORY_TEXT[c.business_category] ?? c.business_category} ·{" "}
            {parseLocalTags(c.tags)} · 授权:{c.consent_status}
          </li>
        ))}
      </ul>

      <p>
        <Link href="/">返回首页</Link> · <Link href="/opportunities">商机管理</Link>
      </p>
    </main>
  );
}

function parseLocalTags(tags: string): string {
  const list = tags.split(",").map((t) => t.trim()).filter(Boolean);
  return list.length > 0 ? `标签:${list.join("/")}` : "无标签";
}
