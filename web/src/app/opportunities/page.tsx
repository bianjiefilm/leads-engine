"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
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
        fetch(`/api/opportunities?category=${cat}`),
        fetch(`/api/opportunities/stats?category=${cat}`),
      ]);
      const listBody = await listRes.json();
      if (!listRes.ok) {
        setError(listBody.message ?? listBody.error ?? `HTTP ${listRes.status}`);
        return;
      }
      setItems(listBody.items ?? []);
      if (statsRes.ok) setStats(await statsRes.json());
    } catch (e) {
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    load(category);
  }, [category, load]);

  return (
    <main>
      <h1>商机管理</h1>
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

      {error ? <p className="muted">加载失败:{error}</p> : null}

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

      {items === null && !error ? <p className="muted">加载中…</p> : null}
      {items !== null && items.length === 0 ? (
        <p className="muted">该类别下暂无商机(或无权查看他人商机)。</p>
      ) : null}
      <ul>
        {(items ?? []).map((o) => {
          const amt = amountView(o.amount_cents, o.amount_source);
          return (
            <li key={o.id}>
              <Link href={`/opportunities/${o.id}`}>{o.title}</Link>{" "}
              — {STAGE_LABELS[o.stage as keyof typeof STAGE_LABELS] ?? o.stage} · 金额:{amt.text}
              {!amt.unknown ? `(${amt.sourceLabel})` : ""} · {probabilityText(o.probability)}
            </li>
          );
        })}
      </ul>

      <p>
        <Link href="/">返回首页</Link>
        {!isBusinessCategory(category) ? " (类别未知)" : ""}
      </p>
    </main>
  );
}
