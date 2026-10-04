"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { RecordList, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { MISSING_SCOPE, failureText, pagePrimary, productError } from "@/lib/productShell";
import {
  BUSINESS_CATEGORIES,
  CATEGORY_LABELS,
  OPPORTUNITY_STAGES,
  STAGE_LABELS,
  amountView,
  isBusinessCategory,
  probabilityText,
  statsLine,
  type BusinessCategory,
  type OpportunityStatsBody,
} from "@/lib/opportunity";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";

// 商机列表(HUI-1693):类别 tab 完全隔离——每个 tab 只请求自己的
// business_category(列表与统计端点都强制必填 category,无跨类别视图)。
// BFF 只转发,业务判断全在 Go 服务端。

interface OpportunityRow {
  id: string;
  title: string;
  stage: string;
  amount_cents: number | null;
  amount_source: string;
  probability: number;
  expected_close_at: string | null;
  assigned_member_id?: string;
}

export default function OpportunitiesPage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const [category, setCategory] = useState<BusinessCategory>("merchant_customer");
  const [items, setItems] = useState<OpportunityRow[] | null>(null);
  const [stats, setStats] = useState<OpportunityStatsBody | null>(null);
  const [error, setError] = useState<string>("");

  const load = useCallback(async (cat: BusinessCategory) => {
    setError("");
    setItems(null);
    setStats(null);
    try {
      const [listRes, statsRes] = await Promise.all([
        fetch(`/api/opportunities?category=${cat}`, scopeInit(scope.tenantId)),
        fetch(`/api/opportunities/stats?category=${cat}`, scopeInit(scope.tenantId)),
      ]);
      const listBody = await listRes.json();
      if (!listRes.ok) {
        setError(failureText(listBody, listRes.status));
        return;
      }
      setItems(listBody.items ?? []);
      if (statsRes.ok) setStats(await statsRes.json());
    } catch (e) {
      setError(productError((e as Error).message));
    }
  }, [scope.tenantId]);

  useEffect(() => {
    setItems(null);
    setStats(null);
    load(category);
  }, [category, load, scope.epoch]);

  return (
    <main data-page="opportunities">
      <header className="page-head">
        <h1>商机管理</h1>
        <Link className="btn primary" data-page-primary="true" href={items && items[0] ? `/opportunities/${items[0].id}` : "/opportunities"}>
          {pagePrimary("opportunities")}
        </Link>
      </header>
      {!scope.tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <p className="muted">
        商家经营销售与创意服务分域管理:成交额、漏斗与后续动作按类别隔离,不做跨类别合计。
        「人工标记成交」仅为销售判断,并非收款事实;金额缺失时显示「未知」。
      </p>

      <div className="tabs">
        {BUSINESS_CATEGORIES.map((cat) => (
          <button
            key={cat}
            className={cat === category ? "tab active" : "tab"}
            onClick={() => setCategory(cat)}
          >
            {CATEGORY_LABELS[cat]}
          </button>
        ))}
      </div>

      {error ? <SurfaceState kind="error" title="商机没有载入" detail={error} /> : null}

      {stats ? <p className="muted">{statsLine(stats)}</p> : null}
      {stats ? (
        <ul className="funnel">
          {OPPORTUNITY_STAGES.map((st) => (
            <li key={st}>
              {STAGE_LABELS[st]}:{stats.funnel[st] ?? 0}
            </li>
          ))}
        </ul>
      ) : null}

      {items === null && !error ? <SurfaceState kind="loading" title="正在读取商机" detail="阶段和金额马上就位。" /> : null}
      {items !== null && items.length === 0 ? (
        <SurfaceState kind="empty" title="这个类别还没有商机" detail="看不到别人名下的商机。换一个类别再看。" />
      ) : null}
      {items && items.length > 0 ? (
        <RecordList
          width={width}
          rows={items.map((o) => {
            const amt = amountView(o.amount_cents, o.amount_source);
            return {
              id: o.id,
              title: o.title,
              href: `/opportunities/${o.id}`,
              action: "打开",
              facts: [
                { label: "阶段", value: STAGE_LABELS[o.stage as keyof typeof STAGE_LABELS] ?? o.stage },
                { label: "金额", value: amt.unknown ? amt.text : `${amt.text}（${amt.sourceLabel}）` },
                { label: "把握", value: probabilityText(o.probability) },
              ],
            };
          })}
        />
      ) : null}

      <p>
        <Link href="/">返回首页</Link>
        {!isBusinessCategory(category) ? " (类别未知)" : ""}
      </p>
    </main>
  );
}
