"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { RecordList, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { DetailDrawer } from "@/components/workbench/detailDrawer";
import { RendererSelect } from "@/components/workbench/rendererSelect";
import { Button, Input, Textarea } from "@/vendor/painuo/react/v1/src/index";
import { canExportContacts, searchGuard } from "@/lib/contact";
import { contactRowFacts, consentText } from "@/lib/finish";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, pagePrimary, productError } from "@/lib/productShell";

// 客户档案列表(HUI-1691 / FEAT-0192)。搜索只按姓名/标签 —— 手机号不作为
// 搜索参数、不进 URL(服务端对疑似手机号输入一律 400,前端同规则先行拦截);
// 导出为 owner 专属动作(服务端重新鉴权);BFF 零业务判断。
// HUI-2626 finish-r1（票面 A/B 组）：列表 + 筛选/搜索 + 抽屉详情统一模式；
// 行内抽屉读真实 /api/contacts/{id}（BFF→Go），带票号防迟到响应；开关抽屉
// 不路由跳转，列表筛选与滚动位置保留；焦点返回由 DetailDrawer finalFocus 处理。

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

interface DrawerContact extends ContactRow {
  phone: string;
  email: string;
  notes: string;
  created_at: string;
}

interface DrawerFollowup {
  id: string;
  member_id: string;
  note: string;
  created_at: string;
}

const CATEGORY_CHOICES = [
  { value: "merchant_customer", label: "商家经营销售" },
  { value: "creative_service", label: "创意服务" },
];

const SOURCE_CHOICES = [
  { value: "manual", label: "手工录入" },
  { value: "form", label: "表单" },
  { value: "touch_campaign", label: "活动触达" },
];

const CONSENT_CHOICES = [
  { value: "pending", label: "待确认" },
  { value: "granted", label: "已同意" },
  { value: "denied", label: "已拒绝" },
];

const EMPTY_FORM = {
  name: "", phone: "", email: "", business_category: "merchant_customer",
  source_type: "manual", consent_status: "pending", notes: "", tags: "",
};

export default function ContactsPage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const [items, setItems] = useState<ContactRow[] | null>(null);
  const [me, setMe] = useState<Whoami | null>(null);
  const [name, setName] = useState("");
  const [tag, setTag] = useState("");
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState(EMPTY_FORM);

  // 详情抽屉（真实数据 + 票号：切范围/迟到响应一律丢弃）。
  const [drawerId, setDrawerId] = useState<string | null>(null);
  const [drawerContact, setDrawerContact] = useState<DrawerContact | null>(null);
  const [drawerFollowups, setDrawerFollowups] = useState<DrawerFollowup[]>([]);
  const [drawerBusy, setDrawerBusy] = useState(false);
  const [drawerErr, setDrawerErr] = useState("");
  const drawerGen = useRef({ seq: 0, tenant: "" });
  // 列表票号：hydrate 前的空头请求、切范围后的迟到响应，一律不允许覆盖新状态。
  const listGen = useRef({ seq: 0, tenant: "" });

  const load = useCallback(async (q: string) => {
    const tenant = scope.tenant ?? "";
    const ticket = { seq: listGen.current.seq + 1, tenant: tenant };
    listGen.current = ticket;
    setError("");
    if (!tenant) {
      setItems([]);
      return;
    }
    setItems(null);
    try {
      const res = await fetch(`/api/contacts${q}`, scopeInit(tenant));
      const body = await res.json();
      if (listGen.current !== ticket || scope.tenant !== ticket.tenant) return;
      if (!res.ok) {
        setError(failureText(body, res.status));
        setItems([]);
        return;
      }
      setItems(body.items ?? []);
    } catch (e) {
      if (listGen.current !== ticket || scope.tenant !== ticket.tenant) return;
      setError(productError((e as Error).message));
      setItems([]);
    }
  }, [scope.tenant]);

  useEffect(() => {
    setName("");
    setTag("");
    setItems(null);
    setDrawerId(null);
    drawerGen.current = { seq: drawerGen.current.seq + 1, tenant: scope.tenant ?? "" };
    load("");
    fetch("/api/whoami", scopeInit(scope.tenant))
      .then(async (res) => (res.ok ? setMe(await res.json()) : setMe({})))
      .catch(() => setMe({}));
  }, [load, scope.epoch, scope.tenant]);

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
    const tenant = scope.tenant ?? "";
    const ticket = listGen.current;
    setCreating(true);
    setError("");
    try {
      const res = await fetch("/api/contacts", scopeInit(tenant, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(form),
      }));
      const body = await res.json();
      if (listGen.current !== ticket || scope.tenant !== ticket.tenant) return;
      if (!res.ok) {
        setError(failureText(body, res.status));
        return;
      }
      setForm({ ...EMPTY_FORM });
      await load("");
    } catch (e) {
      if (listGen.current !== ticket || scope.tenant !== ticket.tenant) return;
      setError(productError((e as Error).message));
    } finally {
      setCreating(false);
    }
  };

  const exportCsv = () => {
    // GET 导出走 BFF;format=csv 必填。owner 专属,服务端重新鉴权。
    window.location.href = "/api/contacts/export?format=csv";
  };

  const openDrawer = (id: string) => {
    const tenant = scope.tenant ?? "";
    const ticket = { seq: drawerGen.current.seq + 1, tenant };
    drawerGen.current = ticket;
    setDrawerId(id);
    setDrawerContact(null);
    setDrawerFollowups([]);
    setDrawerErr("");
    setDrawerBusy(true);
    void (async () => {
      try {
        const [cRes, fuRes] = await Promise.all([
          fetch(`/api/contacts/${id}`, scopeInit(scope.tenant)),
          fetch(`/api/contacts/${id}/followups`, scopeInit(scope.tenant)),
        ]);
        const cBody = await cRes.json();
        if (drawerGen.current !== ticket || scope.tenant !== ticket.tenant) return;
        if (!cRes.ok) {
          setDrawerErr(failureText(cBody, cRes.status));
          return;
        }
        setDrawerContact(cBody as DrawerContact);
        if (fuRes.ok) {
          const fuBody = await fuRes.json();
          if (drawerGen.current !== ticket) return;
          setDrawerFollowups((fuBody.items ?? []) as DrawerFollowup[]);
        }
      } catch (e) {
        if (drawerGen.current === ticket) setDrawerErr(productError((e as Error).message));
      } finally {
        if (drawerGen.current === ticket) setDrawerBusy(false);
      }
    })();
  };

  const closeDrawer = () => {
    drawerGen.current = { seq: drawerGen.current.seq + 1, tenant: scope.tenant ?? "" };
    setDrawerId(null);
    setDrawerContact(null);
    setDrawerErr("");
  };

  const retryDrawer = () => {
    if (drawerId) openDrawer(drawerId);
  };

  return (
    <main data-page="contacts">
      <header className="page-head">
        <h1>客户档案</h1>
      </header>
      {!scope.tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <p className="muted">
        档案、联系方式、来源、标签和授权都留在当前商家。联系人手机号不会自动注册平台账号。搜索只按姓名或标签，不按手机号。
      </p>

      <div className="card" data-list-filter="contacts">
        <h2>搜索</h2>
        <Input label="按姓名搜索" value={name} onChange={(e) => setName(e.target.value)} />
        <Input label="按标签搜索(如 vip)" value={tag} onChange={(e) => setTag(e.target.value)} />
        <div className="queue-actions">
          <Button variant="secondary" type="button" onClick={search}>搜索</Button>
          <Button variant="ghost" type="button" onClick={() => { setName(""); setTag(""); load(""); }}>重置</Button>
          {me && canExportContacts(me) ? (
            <Button variant="secondary" type="button" onClick={exportCsv}>导出 CSV(owner)</Button>
          ) : null}
        </div>
        <p className="muted">不支持按手机号搜索或导出筛选;导出仅 owner 可用且会在服务端留痕。</p>
      </div>

      {error ? <SurfaceState kind="error" title="这一步没有完成" detail={error} /> : null}

      <div className="card">
        <h2>新建档案</h2>
        <p className="muted">表单或活动来源需要留下活动名称，否则建不成档案。保存失败时这里会保留你填的内容。</p>
        <div className="stack-form">
          <Input label="姓名(必填)" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <Input label="手机号(仅存档,不用于搜索)" type="tel" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} />
          <Input label="邮箱" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
          <RendererSelect
            label="类别"
            value={form.business_category}
            onValueChange={(v) => setForm({ ...form, business_category: v })}
            choices={CATEGORY_CHOICES}
          />
          <RendererSelect
            label="来源"
            value={form.source_type}
            onValueChange={(v) => setForm({ ...form, source_type: v })}
            choices={SOURCE_CHOICES}
          />
          <RendererSelect
            label="授权"
            value={form.consent_status}
            onValueChange={(v) => setForm({ ...form, consent_status: v })}
            choices={CONSENT_CHOICES}
          />
          <Input
            label="标签(逗号分隔,如 vip,重点)"
            value={form.tags}
            onChange={(e) => setForm({ ...form, tags: e.target.value })}
          />
          <Textarea label="备注(不超过 2000 字)" value={form.notes} onChange={(e) => setForm({ ...form, notes: e.target.value })} />
          <div className="queue-actions">
            <Button
              variant="primary"
              type="button"
              data-page-primary="true"
              loading={creating}
              disabled={!form.name.trim()}
              disabledReason="填了姓名才能建档"
              onClick={() => void create()}
            >
              {pagePrimary("contacts")}
            </Button>
          </div>
        </div>
      </div>

      {items === null && !error ? <SurfaceState kind="loading" title="正在读取客户" detail="姓名、来源和授权马上就位。" /> : null}
      {items !== null && items.length === 0 && !error ? (
        <SurfaceState kind="empty" title="还没有客户" detail="这个范围里还没有你能查看的档案。别人名下的档案不会出现在这里。" />
      ) : null}
      {items && items.length > 0 ? (
        <RecordList
          width={width}
          rows={items.map((c) => ({
            id: c.id,
            title: c.name,
            action: "查看详情",
            onOpen: () => openDrawer(c.id),
            facts: contactRowFacts(c),
          }))}
        />
      ) : null}

      <DetailDrawer
        open={Boolean(drawerId)}
        onClose={closeDrawer}
        title={drawerContact?.name || "客户档案详情"}
        busy={drawerBusy}
        width={width}
        footer={
          drawerId ? (
            <Link className="btn" href={`/contacts/${drawerId}`}>打开完整档案</Link>
          ) : null
        }
      >
        {drawerErr ? (
          <SurfaceState
            kind="error"
            title="档案没有打开"
            detail={drawerErr}
            action={<Button variant="secondary" type="button" onClick={retryDrawer}>重试</Button>}
          />
        ) : null}
        {!drawerErr && !drawerContact && drawerBusy ? (
          <SurfaceState kind="loading" title="正在打开档案" detail="联系方式、授权和跟进马上就位。" />
        ) : null}
        {drawerContact ? (
          <>
            <ul data-facts="chips">
              {contactRowFacts(drawerContact).map((fact) => (
                <li key={fact.label}>
                  <span>{fact.label}</span>
                  <strong>{fact.value}</strong>
                </li>
              ))}
            </ul>
            <p className="muted">
              手机号:{drawerContact.phone || "(未填)"} · 邮箱:{drawerContact.email || "(未填)"}
            </p>
            <p className="muted">档案级授权:{consentText(drawerContact.consent_status)} · 创建于 {drawerContact.created_at}</p>
            <h3>最近跟进</h3>
            {drawerFollowups.length === 0 ? (
              <p className="muted">暂无跟进记录。跟进在完整档案页追加。</p>
            ) : (
              <ol className="desk-list">
                {drawerFollowups.slice(0, 5).map((f) => (
                  <li key={f.id}>
                    <p>{f.note}</p>
                    <p className="muted">{f.created_at} · 成员 {f.member_id}</p>
                  </li>
                ))}
              </ol>
            )}
            <p className="muted">编辑、授权明细与撤销在完整档案页操作。</p>
          </>
        ) : null}
      </DetailDrawer>

      <p>
        <Link href="/">返回首页</Link> · <Link href="/opportunities">商机管理</Link>
      </p>
    </main>
  );
}
