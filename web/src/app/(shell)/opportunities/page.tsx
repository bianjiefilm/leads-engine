"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { RecordList, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { DetailDrawer } from "@/components/workbench/detailDrawer";
import { SegmentedControl } from "@/vendor/painuo/react/v1/src/index";
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
// HUI-2626 finish-r1（票面 A/B 组）：类别用 renderer SegmentedControl，
// 行内抽屉读真实 /api/opportunities/{id}（票号防迟到响应），完整编辑仍在详情页。

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

const CATEGORY_CHOICES = BUSINESS_CATEGORIES.map((cat) => ({
  value: cat as string,
  label: CATEGORY_LABELS[cat],
}));

export default function OpportunitiesPage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const [category, setCategory] = useState<BusinessCategory>("merchant_customer");
  const [items, setItems] = useState<OpportunityRow[] | null>(null);
  const [stats, setStats] = useState<OpportunityStatsBody | null>(null);
  const [error, setError] = useState<string>("");

  // 详情抽屉（真实数据 + 票号：切范围/迟到响应一律丢弃）。
  const [drawerId, setDrawerId] = useState<string | null>(null);
  const [drawerBody, setDrawerBody] = useState<OpportunityRow | null>(null);
  const [drawerBusy, setDrawerBusy] = useState(false);
  const [drawerErr, setDrawerErr] = useState("");
  const drawerGen = useRef({ seq: 0, tenantId: "" });

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
    setDrawerId(null);
    drawerGen.current = { seq: drawerGen.current.seq + 1, tenantId: scope.tenantId ?? "" };
    load(category);
  }, [category, load, scope.epoch]);

  const openDrawer = (id: string) => {
    const tenantId = scope.tenantId ?? "";
    const ticket = { seq: drawerGen.current.seq + 1, tenantId };
    drawerGen.current = ticket;
    setDrawerId(id);
    setDrawerBody(null);
    setDrawerErr("");
    setDrawerBusy(true);
    void (async () => {
      try {
        const res = await fetch(`/api/opportunities/${id}`, scopeInit(scope.tenantId));
        const body = await res.json();
        if (drawerGen.current !== ticket || scope.tenantId !== ticket.tenantId) return;
        if (!res.ok) {
          setDrawerErr(failureText(body, res.status));
          return;
        }
        setDrawerBody(body as OpportunityRow);
      } catch (e) {
        if (drawerGen.current === ticket) setDrawerErr(productError((e as Error).message));
      } finally {
        if (drawerGen.current === ticket) setDrawerBusy(false);
      }
    })();
  };

  const closeDrawer = () => {
    drawerGen.current = { seq: drawerGen.current.seq + 1, tenantId: scope.tenantId ?? "" };
    setDrawerId(null);
    setDrawerBody(null);
    setDrawerErr("");
  };

  const oppFacts = (o: OpportunityRow) => {
    const amt = amountView(o.amount_cents, o.amount_source);
    return [
      { label: "阶段", value: STAGE_LABELS[o.stage as keyof typeof STAGE_LABELS] ?? o.stage },
      { label: "金额", value: amt.unknown ? amt.text : `${amt.text}（${amt.sourceLabel}）` },
      { label: "把握", value: probabilityText(o.probability) },
      { label: "期望成交", value: o.expected_close_at || "未定" },
    ];
  };

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

      <div className="card" data-list-filter="opportunities">
        <SegmentedControl
          label="商机类别（分域隔离，不做跨类别合计）"
          items={CATEGORY_CHOICES}
          value={category}
          onValueChange={(next) => {
            if (next && isBusinessCategory(next)) setCategory(next);
          }}
        />
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
          rows={items.map((o) => ({
            id: o.id,
            title: o.title,
            action: "查看详情",
            onOpen: () => openDrawer(o.id),
            facts: oppFacts(o),
          }))}
        />
      ) : null}

      <DetailDrawer
        open={Boolean(drawerId)}
        onClose={closeDrawer}
        title={drawerBody?.title || "商机详情"}
        busy={drawerBusy}
        width={width}
        footer={drawerId ? <Link className="btn" href={`/opportunities/${drawerId}`}>打开完整商机</Link> : null}
      >
        {drawerErr ? <SurfaceState kind="error" title="商机没有打开" detail={drawerErr} /> : null}
        {!drawerErr && !drawerBody && drawerBusy ? (
          <SurfaceState kind="loading" title="正在打开商机" detail="阶段、金额和下一步马上就位。" />
        ) : null}
        {drawerBody ? (
          <>
            <ul data-facts="chips">
              {oppFacts(drawerBody).map((fact) => (
                <li key={fact.label}>
                  <span>{fact.label}</span>
                  <strong>{fact.value}</strong>
                </li>
              ))}
            </ul>
            <p className="muted">阶段推进、摘要与预算编辑在完整商机页操作。</p>
          </>
        ) : null}
      </DetailDrawer>

      <p>
        <Link href="/">返回首页</Link>
        {!isBusinessCategory(category) ? " (类别未知)" : ""}
      </p>
    </main>
  );
}
