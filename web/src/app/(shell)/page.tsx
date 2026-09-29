"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import {
  BUCKETS,
  BUCKET_LABELS,
  acceptDesk,
  allowedContactText,
  billingCaption,
  centsText,
  jointChainLine,
  modelAdviceLine,
  moneyView,
  receptionFactLine,
  renderLeadFacts,
  scopeCaption,
  serviceDraftClick,
  serviceDraftControl,
  visibleOutreach,
  type BucketKey,
  type DeskItem,
  type MoneyFacts,
} from "@/lib/workbench";

interface JointChain {
  status?: string;
  label?: string;
}

interface DeskResponse {
  scope?: string;
  buckets?: Partial<Record<BucketKey, DeskItem[]>>;
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

export default function Home() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState("");
  const [reload, setReload] = useState(0);
  const [loadedFor, setLoadedFor] = useState<string | null>(null);
  const [buckets, setBuckets] = useState<Record<BucketKey, DeskItem[]> | null>(null);
  const [money, setMoney] = useState<MoneyFacts>(EMPTY_MONEY);
  const [scopeLabel, setScopeLabel] = useState("");
  const [jointChain, setJointChain] = useState<JointChain | null>(null);
  const [err, setErr] = useState("");
  const [draftMsg, setDraftMsg] = useState("");

  useEffect(() => {
    setTenant(scope.tenantId ?? "");
    setLoadedFor(null);
    setBuckets(null);
    setMoney(EMPTY_MONEY);
    setJointChain(null);
    if (!scope.tenantId) {
      setErr("先填写当前租户");
      setBuckets(acceptDesk(null, null));
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
          setBuckets(acceptDesk(tenantId, null));
          setJointChain(body.joint_chain ?? null);
          setLoadedFor(tenantId);
          return;
        }
        setErr("");
        setScopeLabel(scopeCaption(body.scope));
        setBuckets(acceptDesk(tenantId, body.buckets ?? null));
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

  const visible = loadedFor === (scope.tenantId ?? "") ? buckets : null;
  const lines = moneyView(visible ? money : EMPTY_MONEY);

  return (
    <main>
      <h1>我的工作</h1>
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
              setBuckets(null);
              setLoadedFor(null);
              setJointChain(null);
              return;
            }
            if (value === scope.tenantId) {
              setLoadedFor(null);
              setBuckets(null);
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
        BUCKETS.map((key) => (
          <section className="card" key={key}>
            <h2>
              {BUCKET_LABELS[key]}（{visible[key].length}）
            </h2>
            {visible[key].length === 0 ? (
              <p className="muted">没有待办。</p>
            ) : (
              <ul className="desk-list">
                {visible[key].map((item) => {
                  const draft = serviceDraftControl(item.service_draft);
                  const facts = renderLeadFacts(item, jointChain);
                  const outreach = visibleOutreach(item.outreach_notice);
                  const receptionLine = receptionFactLine(item);
                  return (
                  <li key={`${key}-${item.id}`}>
                    {item.kind === "reception" ? (
                      <p data-reception-fact="owner">{receptionLine}</p>
                    ) : (
                      <>
                        <p>来源：{facts.source}</p>
                        <p>负责人：{facts.owner}</p>
                        <p>状态：{facts.state}</p>
                        <p>允许的联系方式：{allowedContactText(item.allowed_contacts)}</p>
                        <p>下一步：{facts.next}</p>
                        <p>{facts.sync}</p>
                      </>
                    )}
                    <p data-joint-chain="incomplete">{facts.chain}</p>
                    {outreach ? <p className="muted">{outreach}</p> : null}
                    <p className="muted">{modelAdviceLine()}</p>
                    {item.reason && item.kind !== "reception" ? <p className="muted">{item.reason}</p> : null}
                    <div className="queue-actions">
                      {item.kind === "reception" ? <Link href="/reception">打开接待</Link> : null}
                      {item.lead_id ? <Link href={`/leads/${item.lead_id}`} data-desk-action="查看新线索">查看新线索</Link> : null}
                      {item.opportunity_id ? <Link href={`/opportunities/${item.opportunity_id}`}>打开商机</Link> : null}
                      {draft.present ? (
                        <button type="button" disabled={!draft.enabled} onClick={() => setDraftMsg(serviceDraftClick().message)}>
                          创建服务需求草稿
                        </button>
                      ) : null}
                    </div>
                    {draft.present && !draft.enabled ? <p className="muted">{draft.reason}</p> : null}
                  </li>
                  );
                })}
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
