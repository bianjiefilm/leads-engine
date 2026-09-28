"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { centsLabel, presentSubscription, rechargeReturn, type SubscriptionFacts } from "@/lib/subscription";

interface SubscriptionResponse extends SubscriptionFacts {
  crm_opened?: boolean;
  opened_by_wallet?: boolean;
  merchant_revenue_in_wallet?: boolean;
  ledgers?: {
    crm_subscription_cents?: number | null;
    ai_usage_cents?: number | null;
    merchant_deal_cents?: number | null;
    wallet_cents?: number | null;
  };
  message?: string;
  error?: string;
}

export default function SubscriptionPage() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState(scope.tenantId ?? "");
  const [facts, setFacts] = useState<SubscriptionFacts>({ status: "unconfigured" });
  const [err, setErr] = useState("");
  const view = presentSubscription(facts);
  const back = rechargeReturn({
    reason: view.advancedLimited ? "quote_stale" : undefined,
    billing_center: "billing_center",
    return_to: "/subscription",
    live_charge: 0,
  });

  useEffect(() => {
    setTenant(scope.tenantId ?? "");
    if (!scope.tenantId) return;
    const tenantId = scope.tenantId;
    let cancelled = false;
    fetch("/api/subscription", { headers: { "x-tenant-id": tenantId } })
      .then(async (res) => {
        const body = (await res.json()) as SubscriptionResponse;
        if (cancelled) return;
        if (!res.ok) {
          setErr(body.message ?? body.error ?? `HTTP ${res.status}`);
          setFacts({ status: "unconfigured" });
          return;
        }
        setErr("");
        setFacts({
          status: body.status,
          wallet_balance_cents: body.ledgers?.wallet_cents ?? null,
          crm_subscription_cents: body.ledgers?.crm_subscription_cents ?? null,
          ai_usage_cents: body.ledgers?.ai_usage_cents ?? null,
          merchant_deal_cents: body.ledgers?.merchant_deal_cents ?? null,
          history_retained: body.history_retained,
          data_lost: body.data_lost,
        });
      })
      .catch((e: Error) => {
        if (!cancelled) setErr(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenantId]);

  return (
    <main>
      <h1>订阅与用量</h1>
      <p className="muted">
        <Link href="/">返回工作台</Link>
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
            if (!value) return;
            if (value !== scope.tenantId) commitCrmTenant(value);
          }}
        >
          读取
        </button>
        <p className="muted">钱包余额不代表 CRM 套餐已开通。商家成交额不进入平台钱包。这里不会扣款、外呼或发消息。</p>
        {err ? <p className="muted">{err}</p> : null}
      </div>
      <div className="card">
        <h2>三本账分开</h2>
        <ul>
          {view.lines.map((line) => (
            <li key={line.key} data-testid={`subscription-${line.key}`}>
              {line.label}：{centsLabel(line.cents)}
            </li>
          ))}
        </ul>
        <p data-testid="subscription-wallet">平台钱包观察值：{centsLabel(view.walletCents)}</p>
        <p data-testid="subscription-plan">{view.crmOpened ? "CRM 套餐：已开通" : "CRM 套餐：未开通"}</p>
        <p data-testid="subscription-history">
          {view.dataLost ? "历史被标成丢失" : "联系人、跟进和商机历史仍在"}
          {view.advancedLimited ? "。高级功能已停用。" : ""}
        </p>
      </div>
      <div className="card">
        <h2>余额不足</h2>
        <p>
          充值只走统一入口{" "}
          <span data-testid="subscription-billing-entry">{back.entry || "billing_center"}</span>
          ，回来后先重读报价，再回到 <span data-testid="subscription-return">{back.returnTo || "/subscription"}</span>。
        </p>
      </div>
    </main>
  );
}
