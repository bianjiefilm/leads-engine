"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import {
  BUCKETS,
  BUCKET_LABELS,
  acceptDesk,
  centsText,
  moneyView,
  sourceText,
  type BucketKey,
  type DeskItem,
  type MoneyFacts,
} from "@/lib/workbench";

interface DeskResponse {
  scope?: string;
  buckets?: Partial<Record<BucketKey, DeskItem[]>>;
  money?: MoneyFacts;
  billing?: { ordinary_crm_charge_cents?: number };
  automation?: { auto_call?: boolean; auto_message?: boolean; create_order?: boolean };
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
  const [err, setErr] = useState("");

  useEffect(() => {
    setTenant(scope.tenantId ?? "");
    setLoadedFor(null);
    setBuckets(null);
    setMoney(EMPTY_MONEY);
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
          setLoadedFor(tenantId);
          return;
        }
        setErr("");
        setScopeLabel(body.scope === "tenant" ? "全租户" : "我的范围");
        setBuckets(acceptDesk(tenantId, body.buckets ?? null));
        setMoney(body.money ?? EMPTY_MONEY);
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
              return;
            }
            if (value === scope.tenantId) {
              setLoadedFor(null);
              setBuckets(null);
              setMoney(EMPTY_MONEY);
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
          手工安排的下一步优先。这里不会自动外呼、发消息或创建订单。普通线索入库、查看和人工跟进不逐条扣费。
        </p>
      </div>
      {err ? <p className="muted">{err}</p> : null}
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
                {visible[key].map((item) => (
                  <li key={`${key}-${item.id}`}>
                    <p>{sourceText(item.source)}</p>
                    <p>{item.next?.label || "安排下一次跟进"}</p>
                    {item.reason ? <p className="muted">{item.reason}</p> : null}
                    <div className="queue-actions">
                      {item.lead_id ? <Link href={`/leads/${item.lead_id}`}>查看新线索</Link> : null}
                      {item.opportunity_id ? <Link href={`/opportunities/${item.opportunity_id}`}>打开商机</Link> : null}
                      {item.show_service_draft && item.opportunity_id ? (
                        <Link href={`/opportunities/${item.opportunity_id}`}>创建服务需求草稿</Link>
                      ) : null}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </section>
        ))
      )}
      <p className="muted">
        <Link href="/leads">线索</Link> · <Link href="/contacts">客户档案</Link> · <Link href="/opportunities">商机</Link> ·{" "}
        <Link href="/reception">接待</Link>
      </p>
    </main>
  );
}
