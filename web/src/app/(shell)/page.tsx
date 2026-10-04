"use client";

// 获客唯一首页：today-next。HUI-2626 只换这一页的视觉外壳，不要另做首页。

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { RecordFrame, SurfaceState, ToneBadge, useShellWidth } from "@/components/workbench/chrome";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, factTone, failureText, listTenantHeader, pagePrimary, productError } from "@/lib/productShell";
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
  followNote,
  questionsFor,
  recallToday,
  reviseBody,
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
  const width = useShellWidth();
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
    setLoadedFor(null);
    setGroups(null);
    setMoney(EMPTY_MONEY);
    setJointChain(null);
    const headers = listTenantHeader(scope.tenantId);
    if (!headers) {
      setErr("");
      setGroups(acceptToday(null, null));
      setLoadedFor("");
      return;
    }
    const tenantId = headers["x-tenant-id"];
    let cancelled = false;
    fetch("/api/workbench", { headers })
      .then(async (res) => {
        const body = (await res.json()) as DeskResponse;
        if (cancelled) return;
        if (!res.ok) {
          setErr(failureText(body, res.status));
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
        if (!cancelled) setErr(productError(e.message));
      });
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenantId, reload]);

  const visible = loadedFor === (scope.tenantId ?? "") ? groups : null;
  const lines = moneyView(visible ? money : EMPTY_MONEY);
  const primaryId = focusId || (visible ? TODAY_GROUPS.flatMap((key) => visible[key]).find((item) => item.lead_id)?.id ?? "" : "");

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
    const headers = listTenantHeader(scope.tenantId);
    if (!headers || !item.lead_id) return;
    const draft = draftFor(item.id);
    const note = followNote(draft.note);
    if (!note) return;
    if (mode === "schedule" && !draft.nextAt) return;
    setDraftMsg("");
    const waiting = mode === "note" && draft.disposition === "waiting_customer";
    const payload: Record<string, unknown> = {
      note,
      complete: true,
      disposition: waiting ? "waiting_customer" : "next",
    };
    if (!waiting && draft.nextAt) {
      payload.next_follow_up_at = new Date(draft.nextAt).toISOString().replace(/\.\d{3}Z$/, "Z");
    }
    const res = await fetch(`/api/leads/${item.lead_id}/follow-through`, {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify(payload),
    });
    const out = await res.json();
    if (!res.ok) {
      setDraftMsg(failureText(out, res.status));
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
    const headers = listTenantHeader(scope.tenantId);
    if (!headers) return;
    const body = reviseBody(draftFor(item.id).draftBody, item.draft_body).trim();
    if (!body) return;
    const res = await fetch(`/api/workbench/drafts/${item.id}/revise`, {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({ body }),
    });
    const out = await res.json();
    if (!res.ok || out.sent === true) {
      setDraftMsg(!res.ok ? failureText(out, res.status) : (out.message ?? out.error ?? "没有修改"));
      return;
    }
    setDraftMsg("草稿已修改，尚未发送");
    setReload((n) => n + 1);
  }

  async function ignoreDraft(item: DeskItem) {
    const headers = listTenantHeader(scope.tenantId);
    if (!headers) return;
    const res = await fetch(`/api/workbench/drafts/${item.id}/ignore`, {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({}),
    });
    const out = await res.json();
    if (!res.ok || out.sent === true) {
      setDraftMsg(!res.ok ? failureText(out, res.status) : (out.message ?? out.error ?? "没有忽略"));
      return;
    }
    setDraftMsg("已忽略草稿，没有发送");
    setReload((n) => n + 1);
  }

  return (
    <main data-today-home={HOME_SURFACE} data-page="today">
      <header className="page-head">
        <h1>今天要完成的客户工作</h1>
      </header>
      <div className="card">
        <p>
          <ToneBadge tone="human" /> {scopeLabel ? `当前视图：${scopeLabel}。` : ""}
          {billingCaption()}。
        </p>
        <p data-tone="automation">
          <ToneBadge tone="automation" /> 手工安排的下一步优先。这里不会自动外呼、发消息或创建订单。
        </p>
        <p data-tone="ai">
          <ToneBadge tone="ai" /> {modelAdviceLine()}。意向分级只给下一步建议，不自动触达。
        </p>
        <p data-joint-chain="incomplete">{jointChainLine(jointChain)}</p>
      </div>
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
      {!scope.tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {scope.tenantId && err ? <SurfaceState kind="error" title="今天的工作没有载入" detail={err} /> : null}
      {scope.tenantId && !err && visible === null ? <SurfaceState kind="loading" title="正在整理今天的工作" detail="分组和金额马上就位。" /> : null}
      {scope.tenantId && !err && visible
        ? TODAY_GROUPS.map((key) => (
          <section className="card" key={key}>
            <h2>
              {TODAY_LABELS[key]}（{visible[key].length}）
            </h2>
            {visible[key].length === 0 ? (
              <SurfaceState kind="empty" title="没有待办" detail={`${TODAY_LABELS[key]}这一组是空的。`} />
            ) : (
              <ul className="desk-list">
                {visible[key].map((item) => (
                  <TodayRow
                    key={`${key}-${item.id}`}
                    width={width}
                    primaryRow={item.id === primaryId}
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
        : null}
      <p className="muted">
        <Link href="/leads">线索</Link> · <Link href="/contacts">客户档案</Link> · <Link href="/opportunities">商机</Link> ·{" "}
        <Link href="/reception">接待</Link> · <Link href="/intent">意向分级</Link> · <Link href="/attribution">来源与费用</Link> · <Link href="/subscription">订阅与用量</Link> ·{" "}
        <Link href="/isolation">租户隔离与导出</Link>
      </p>
    </main>
  );
}

function TodayRow({
  width,
  primaryRow,
  item,
  jointChain,
  draft,
  onDraft,
  onFollow,
  onRevise,
  onIgnore,
  onService,
}: {
  width: number;
  primaryRow: boolean;
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
  const questions = questionsFor(confirmed, item.context?.ask ?? item.ask ?? [], []);
  const tone = factTone({
    kind: item.kind,
    auto: item.next?.auto_call === true || item.next?.auto_message === true || item.next?.create_order === true,
  });
  const factRows = [
    ...(receptionLine ? [{ label: "接待", value: receptionLine }] : []),
    { label: "来源", value: facts.source },
    { label: "已确认", value: `${item.context?.customer || "客户未记录"}${item.context?.business ? ` · ${item.context.business}` : ""}${confirmed.length ? ` · ${confirmed.join("、")}` : ""}` },
    { label: "负责人", value: facts.owner },
    { label: "状态", value: `状态：${facts.state}` },
    { label: "允许的联系方式", value: allowedContactText(item.allowed_contacts) },
    { label: "最近一次互动", value: `${item.last_interaction?.summary || "还没有互动"}${item.last_interaction?.at ? ` · ${item.last_interaction.at}` : ""}` },
    { label: "下一步", value: facts.next },
    { label: "依据", value: item.basis || modelAdviceLine() },
    { label: "同步", value: facts.sync },
    ...(outreach ? [{ label: "触达", value: outreach }] : []),
    ...(item.reason && item.kind !== "reception" ? [{ label: "原因", value: item.reason }] : []),
    ...(questions.length > 0 ? [{ label: "待补充", value: questions.join("、") }] : []),
    ...(item.kind === "ai_draft" ? [{ label: "草稿", value: item.draft_body || "未记录" }] : []),
  ];
  const followDisabled = !draft.note.trim();
  const primaryFollow = (
    <button
      className="primary"
      type="button"
      data-page-primary="true"
      data-desk-action={NARROW_ACTIONS[1]}
      disabled={followDisabled}
      onClick={() => onFollow("note")}
    >
      {pagePrimary("today")}
    </button>
  );
  const secondaryFollow = (
    <button
      className="btn"
      type="button"
      data-page-primary="false"
      data-desk-action={NARROW_ACTIONS[1]}
      disabled={followDisabled}
      onClick={() => onFollow("note")}
    >
      {pagePrimary("today")}
    </button>
  );
  return (
    <li id={`today-item-${item.id}`}>
      <RecordFrame width={width} tone={tone} title={item.context?.customer || facts.next} facts={factRows} primary={primaryRow && item.lead_id ? primaryFollow : undefined}>
        {item.kind === "reception" ? <p data-reception-fact="owner">{receptionLine}</p> : null}
        <p data-joint-chain="incomplete">{facts.chain}</p>
        <div className="queue-actions">
          {item.kind === "reception" && !item.lead_id ? (
            <Link className="btn" href="/reception">打开接待，本屏尚未完成这一步</Link>
          ) : item.kind === "reception" || item.session_id ? (
            <Link className="btn" href="/reception">打开接待</Link>
          ) : null}
          {item.lead_id ? <Link className="btn" href={`/leads/${item.lead_id}`} data-desk-action={NARROW_ACTIONS[0]}>{NARROW_ACTIONS[0]}</Link> : null}
          {item.opportunity_id ? <Link className="btn" href={`/opportunities/${item.opportunity_id}`}>打开商机</Link> : null}
          {service.present ? (
            <button type="button" className="btn" disabled={!service.enabled} onClick={onService}>
              创建服务需求草稿
            </button>
          ) : null}
        </div>
        {service.present && !service.enabled ? <p className="muted">{service.reason}</p> : null}
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
              下一次（北京时间）
              <input type="datetime-local" value={draft.nextAt} onChange={(e) => onDraft({ nextAt: e.target.value })} />
            </label>
            <div className="queue-actions">
              {primaryRow ? null : secondaryFollow}
              <button className="btn" type="button" data-desk-action={NARROW_ACTIONS[2]} disabled={!draft.note.trim() || !draft.nextAt} onClick={() => onFollow("schedule")}>
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
              <textarea value={reviseBody(draft.draftBody, item.draft_body)} onChange={(e) => onDraft({ draftBody: e.target.value })} placeholder="改成要保留的措辞" />
            </label>
            <div className="queue-actions">
              <button className="btn" type="submit">保存修改</button>
              <button className="btn" type="button" onClick={onIgnore}>忽略</button>
            </div>
          </form>
        ) : null}
      </RecordFrame>
    </li>
  );
}
