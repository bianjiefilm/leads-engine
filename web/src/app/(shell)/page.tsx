"use client";

// 获客唯一首页：today-next。HUI-2626 只换这一页的视觉外壳，不要另做首页。

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import {
  HOME_SURFACE,
  NARROW_ACTIONS,
  TODAY_GROUPS,
  TODAY_LABELS,
  acceptToday,
  allowedContactText,
  billingCaption,
  jointChainLine,
  modelAdviceLine,
  centsText,
  moneyView,
  questionsFor,
  recallToday,
  receptionFactLine,
  rememberToday,
  todaySaveReady,
  renderLeadFacts,
  scopeCaption,
  serviceDraftClick,
  serviceDraftControl,
  visibleOutreach,
  type DeskItem,
  type MoneyFacts,
  type TodayDraft,
  type TodayGroup,
  type TodayMemory,
} from "@/lib/workbench";

interface JointChain {
  status?: string;
  label?: string;
}

interface DeskResponse {
  scope?: string;
  today?: Partial<Record<TodayGroup, DeskItem[]>>;
  money?: MoneyFacts;
  billing?: { ordinary_crm_charge_cents?: number };
  automation?: { auto_call?: boolean; auto_message?: boolean; create_order?: boolean };
  joint_chain?: JointChain;
  outreach_submitted?: boolean;
  message?: string;
  error?: string;
}

const EMPTY_MONEY: MoneyFacts = {
  customer_deal_cents: null,
  painuo_service_order_cents: null,
  platform_tool_spend_cents: null,
};

const MEMORY_KEY = "leads_today_memory";

function emptyDraft(): TodayDraft {
  return { note: "", nextAt: "", disposition: "next", changed: [] };
}

function readMemory(): TodayMemory {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(MEMORY_KEY);
    return raw ? (JSON.parse(raw) as TodayMemory) : {};
  } catch {
    return {};
  }
}

function writeMemory(memory: TodayMemory) {
  window.localStorage.setItem(MEMORY_KEY, JSON.stringify(memory));
}

export default function Home() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState("");
  const [reload, setReload] = useState(0);
  const [loadedFor, setLoadedFor] = useState<string | null>(null);
  const [groups, setGroups] = useState<Record<TodayGroup, DeskItem[]> | null>(null);
  const [money, setMoney] = useState<MoneyFacts>(EMPTY_MONEY);
  const [scopeLabel, setScopeLabel] = useState("");
  const [jointChain, setJointChain] = useState<JointChain | null>(null);
  const [err, setErr] = useState("");
  const [draftMsg, setDraftMsg] = useState("");
  const [focusId, setFocusId] = useState("");
  const [drafts, setDrafts] = useState<Record<string, TodayDraft>>({});
  const appliedTenant = useRef<string | null>(null);

  useEffect(() => {
    const restored = recallToday(readMemory(), scope.tenantId);
    setDrafts(restored.drafts);
    setFocusId(restored.focusId);
    appliedTenant.current = null;
  }, [scope.tenantId]);

  useEffect(() => {
    if (!todaySaveReady(appliedTenant.current, scope.tenantId)) {
      appliedTenant.current = scope.tenantId ?? null;
      return;
    }
    writeMemory(rememberToday(readMemory(), scope.tenantId ?? "", focusId, drafts));
  }, [scope.tenantId, focusId, drafts]);

  useEffect(() => {
    setTenant(scope.tenantId ?? "");
    setLoadedFor(null);
    setGroups(null);
    setMoney(EMPTY_MONEY);
    setJointChain(null);
    if (!scope.tenantId) {
      setErr("先填写当前租户");
      setGroups(acceptToday(null, null));
      setLoadedFor("");
      return;
    }
    const tenantId = scope.tenantId;
    let cancelled = false;
    fetch("/api/workbench", { headers: { "x-tenant-id": tenantId } })
      .then(async (res) => {
        const body = (await res.json()) as DeskResponse;
        if (cancelled) return;
        if (!res.ok) {
          setErr(body.message ?? body.error ?? `HTTP ${res.status}`);
          setGroups(acceptToday(tenantId, null));
          setJointChain(body.joint_chain ?? null);
          setLoadedFor(tenantId);
          return;
        }
        setErr("");
        setScopeLabel(scopeCaption(body.scope));
        setGroups(acceptToday(tenantId, body.today ?? null));
        setMoney(body.money ?? EMPTY_MONEY);
        setJointChain(body.joint_chain ?? null);
        setLoadedFor(tenantId);
      })
      .catch((e: Error) => {
        if (!cancelled) setErr(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenantId, reload]);

  const visible = loadedFor === (scope.tenantId ?? "") ? groups : null;
  const lines = moneyView(visible ? money : EMPTY_MONEY);

  useEffect(() => {
    if (!focusId || !visible) return;
    document.getElementById(`today-item-${focusId}`)?.scrollIntoView({ block: "center" });
  }, [focusId, visible]);

  function draftFor(id: string): TodayDraft {
    return drafts[id] ?? emptyDraft();
  }

  function updateDraft(id: string, patch: Partial<TodayDraft>) {
    setFocusId(id);
    setDrafts((current) => ({ ...current, [id]: { ...draftFor(id), ...current[id], ...patch } }));
  }

  async function submitFollow(item: DeskItem, mode: "note" | "schedule") {
    if (!scope.tenantId || !item.lead_id) return;
    const draft = draftFor(item.id);
    if (!draft.note.trim()) return;
    if (mode === "schedule" && !draft.nextAt) return;
    setDraftMsg("");
    const waiting = mode === "note" && draft.disposition === "waiting_customer";
    const payload: Record<string, unknown> = {
      note: draft.note.trim(),
      complete: true,
      disposition: waiting ? "waiting_customer" : "next",
    };
    if (!waiting && draft.nextAt) {
      payload.next_follow_up_at = new Date(draft.nextAt).toISOString().replace(/\.\d{3}Z$/, "Z");
    }
    const res = await fetch(`/api/leads/${item.lead_id}/follow-through`, {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": scope.tenantId },
      body: JSON.stringify(payload),
    });
    const out = await res.json();
    if (!res.ok) {
      setDraftMsg(out.message ?? out.error ?? `HTTP ${res.status}`);
      return;
    }
    setDrafts((current) => {
      const next = { ...current };
      delete next[item.id];
      return next;
    });
    setDraftMsg(out.today_group === "scheduled_next" ? "已安排下一步，不在今天的待办里" : (out.next?.label ?? "已记下"));
    setReload((n) => n + 1);
  }

  async function reviseDraft(item: DeskItem) {
    if (!scope.tenantId) return;
    const body = draftFor(item.id).note.trim();
    if (!body) return;
    const res = await fetch(`/api/workbench/drafts/${item.id}/revise`, {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": scope.tenantId },
      body: JSON.stringify({ body }),
    });
    const out = await res.json();
    if (!res.ok || out.sent === true) {
      setDraftMsg(out.message ?? out.error ?? "没有修改");
      return;
    }
    setDraftMsg("草稿已修改，尚未发送");
    setReload((n) => n + 1);
  }

  async function ignoreDraft(item: DeskItem) {
    if (!scope.tenantId) return;
    const res = await fetch(`/api/workbench/drafts/${item.id}/ignore`, {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": scope.tenantId },
      body: JSON.stringify({}),
    });
    const out = await res.json();
    if (!res.ok || out.sent === true) {
      setDraftMsg(out.message ?? out.error ?? "没有忽略");
      return;
    }
    setDraftMsg("已忽略草稿，没有发送");
    setReload((n) => n + 1);
  }

  return (
    <main data-today-home={HOME_SURFACE}>
      <h1>今天要完成的客户工作</h1>
      <p>
        <Link href="/channel-interactions">授权互动</Link>
        {" · "}
        <Link href="/sop">跟进提醒</Link>
        {" · "}
        <Link href="/outbound">外呼安全门</Link>
      </p>
      <div className="card">
        <label>
          当前租户{" "}
          <input value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="租户 id" />
        </label>{" "}
        <button
          type="button"
          onClick={() => {
            const value = tenant.trim();
            if (!value) {
              setErr("先填写当前租户");
              setGroups(null);
              setLoadedFor(null);
              setJointChain(null);
              return;
            }
            if (value === scope.tenantId) {
              setLoadedFor(null);
              setGroups(null);
              setMoney(EMPTY_MONEY);
              setJointChain(null);
              setReload((n) => n + 1);
              return;
            }
            commitCrmTenant(value);
          }}
        >
          加载
        </button>
        <p className="muted">
          {scopeLabel ? `当前视图：${scopeLabel}。` : ""}
          手工安排的下一步优先。这里不会自动外呼、发消息或创建订单。{billingCaption()}。
          {modelAdviceLine()}。意向分级只给下一步建议，不自动触达。
        </p>
        <p data-joint-chain="incomplete">{jointChainLine(jointChain)}</p>
      </div>
      {err ? <p className="muted">{err}</p> : null}
      {draftMsg ? <p className="muted">{draftMsg}</p> : null}
      <div className="card">
        <h2>金额分开看</h2>
        <ul className="money-lines">
          {lines.lines.map((line) => (
            <li key={line.key}>
              {line.label}：{centsText(line.cents)}
            </li>
          ))}
        </ul>
      </div>
      {visible === null ? (
        <p className="muted">加载中…</p>
      ) : (
        TODAY_GROUPS.map((key) => (
          <section className="card" key={key}>
            <h2>
              {TODAY_LABELS[key]}（{visible[key].length}）
            </h2>
            {visible[key].length === 0 ? (
              <p className="muted">没有待办。</p>
            ) : (
              <ul className="desk-list">
                {visible[key].map((item) => (
                  <TodayRow
                    key={`${key}-${item.id}`}
                    item={item}
                    jointChain={jointChain}
                    draft={draftFor(item.id)}
                    onDraft={(patch) => updateDraft(item.id, patch)}
                    onFollow={(mode) => void submitFollow(item, mode)}
                    onRevise={() => void reviseDraft(item)}
                    onIgnore={() => void ignoreDraft(item)}
                    onService={() => setDraftMsg(serviceDraftClick().message)}
                  />
                ))}
              </ul>
            )}
          </section>
        ))
      )}
      <p className="muted">
        <Link href="/leads">线索</Link> · <Link href="/contacts">客户档案</Link> · <Link href="/opportunities">商机</Link> ·{" "}
        <Link href="/reception">接待</Link> · <Link href="/intent">意向分级</Link> · <Link href="/attribution">来源与费用</Link> · <Link href="/subscription">订阅与用量</Link> ·{" "}
        <Link href="/isolation">租户隔离与导出</Link>
      </p>
    </main>
  );
}

function TodayRow({
  item,
  jointChain,
  draft,
  onDraft,
  onFollow,
  onRevise,
  onIgnore,
  onService,
}: {
  item: DeskItem;
  jointChain: JointChain | null;
  draft: TodayDraft;
  onDraft: (patch: Partial<TodayDraft>) => void;
  onFollow: (mode: "note" | "schedule") => void;
  onRevise: () => void;
  onIgnore: () => void;
  onService: () => void;
}) {
  const facts = renderLeadFacts(item, jointChain);
  const outreach = visibleOutreach(item.outreach_notice);
  const receptionLine = receptionFactLine(item);
  const service = serviceDraftControl(item.service_draft);
  const confirmed = item.context?.facts ?? [];
  const questions = questionsFor(confirmed, item.context?.ask ?? item.ask ?? [], draft.changed);
  return (
    <li id={`today-item-${item.id}`}>
      {item.kind === "reception" ? (
        <p data-reception-fact="owner">{receptionLine}</p>
      ) : (
        <>
          <p>来源：{facts.source}</p>
          <p>已确认：{item.context?.customer || "客户未记录"}{item.context?.business ? ` · ${item.context.business}` : ""}{confirmed.length ? ` · ${confirmed.join("、")}` : ""}</p>
          <p>负责人：{facts.owner}</p>
          <p>状态：{facts.state}</p>
          <p>允许的联系方式：{allowedContactText(item.allowed_contacts)}</p>
          <p>最近一次互动：{item.last_interaction?.summary || "还没有互动"}{item.last_interaction?.at ? ` · ${item.last_interaction.at}` : ""}</p>
          <p>下一步：{facts.next}</p>
          <p className="muted">{item.basis || modelAdviceLine()}</p>
          <p>{facts.sync}</p>
        </>
      )}
      <p data-joint-chain="incomplete">{facts.chain}</p>
      {outreach ? <p className="muted">{outreach}</p> : null}
      <p className="muted">{modelAdviceLine()}</p>
      {item.reason && item.kind !== "reception" ? <p className="muted">{item.reason}</p> : null}
      {questions.length > 0 ? <p>待补充：{questions.join("、")}</p> : null}
      {item.kind === "ai_draft" ? <p>草稿：{item.draft_body || "未记录"}</p> : null}
      <div className="queue-actions">
        {item.kind === "reception" || item.session_id ? <Link href="/reception">打开接待</Link> : null}
        {item.lead_id ? <Link href={`/leads/${item.lead_id}`} data-desk-action={NARROW_ACTIONS[0]}>{NARROW_ACTIONS[0]}</Link> : null}
        {item.opportunity_id ? <Link href={`/opportunities/${item.opportunity_id}`}>打开商机</Link> : null}
        {service.present ? (
          <button type="button" disabled={!service.enabled} onClick={onService}>
            创建服务需求草稿
          </button>
        ) : null}
      </div>
      {service.present && !service.enabled ? <p className="muted">{service.reason}</p> : null}
      {confirmed.length > 0 ? (
        <fieldset className="stack-form">
          <legend>只改变化项</legend>
          {confirmed.map((fact) => (
            <label key={fact}>
              <input
                type="checkbox"
                checked={draft.changed.includes(fact)}
                onChange={(e) => {
                  const changed = e.target.checked
                    ? [...draft.changed, fact]
                    : draft.changed.filter((itemName) => itemName !== fact);
                  onDraft({ changed });
                }}
              />
              {fact}
            </label>
          ))}
        </fieldset>
      ) : null}
      {item.lead_id ? (
        <form
          className="stack-form"
          onSubmit={(e) => {
            e.preventDefault();
            onFollow(draft.disposition === "waiting_customer" ? "note" : "schedule");
          }}
        >
          <label>
            记录
            <textarea
              value={draft.note}
              placeholder={item.next?.label || "这次跟进了什么"}
              onFocus={() => onDraft({})}
              onChange={(e) => onDraft({ note: e.target.value })}
            />
          </label>
          <label>
            处理
            <select
              value={draft.disposition}
              onChange={(e) => onDraft({ disposition: e.target.value as TodayDraft["disposition"] })}
            >
              <option value="next">安排下一步</option>
              <option value="waiting_customer">等待客户</option>
            </select>
          </label>
          <label>
            下一次
            <input type="datetime-local" value={draft.nextAt} onChange={(e) => onDraft({ nextAt: e.target.value })} />
          </label>
          <div className="queue-actions">
            <button className="primary" type="button" data-desk-action={NARROW_ACTIONS[1]} disabled={!draft.note.trim()} onClick={() => onFollow("note")}>
              {NARROW_ACTIONS[1]}
            </button>
            <button className="primary" type="button" data-desk-action={NARROW_ACTIONS[2]} disabled={!draft.note.trim() || !draft.nextAt} onClick={() => onFollow("schedule")}>
              {NARROW_ACTIONS[2]}
            </button>
          </div>
        </form>
      ) : null}
      {item.kind === "ai_draft" ? (
        <form
          className="stack-form"
          onSubmit={(e) => {
            e.preventDefault();
            onRevise();
          }}
        >
          <label>
            修改草稿
            <textarea value={draft.note} onChange={(e) => onDraft({ note: e.target.value })} placeholder={item.draft_body || "改成要保留的措辞"} />
          </label>
          <div className="queue-actions">
            <button className="primary" type="submit">保存修改</button>
            <button type="button" onClick={onIgnore}>忽略</button>
          </div>
        </form>
      ) : null}
    </li>
  );
}
