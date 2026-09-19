"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import {
  OPPORTUNITY_STAGES,
  STAGE_LABELS,
  amountView,
  canTransitionStage,
  expectedCloseText,
  probabilityText,
  stageActionsFor,
  timelineLabel,
  type CallerView,
  type StageEvent,
} from "@/lib/opportunity";

// 商机详情(HUI-1693):阶段时间线(审计链)+ 金额来源标识 + 按权限显隐的
// 阶段操作按钮。按钮显隐只是 UI 镜像;服务端对每次转换重新鉴权(非 assignee
// 的记录对本角色直接 404 不可见)。

interface OpportunityDetail {
  id: string;
  title: string;
  stage: string;
  business_category: string;
  amount_cents: number | null;
  amount_source: string;
  probability: number;
  expected_close_at: string | null;
  assigned_member_id?: string;
}

interface WhoamiBody extends CallerView {
  error?: string;
}

export default function OpportunityDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const [opp, setOpp] = useState<OpportunityDetail | null>(null);
  const [history, setHistory] = useState<StageEvent[]>([]);
  const [me, setMe] = useState<WhoamiBody | null>(null);
  const [msg, setMsg] = useState<string>("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const [whoRes, oppRes, histRes] = await Promise.all([
        fetch("/api/whoami"),
        fetch(`/api/opportunities/${id}`),
        fetch(`/api/opportunities/${id}/stage-history`),
      ]);
      if (whoRes.ok) setMe(await whoRes.json());
      const oppBody = await oppRes.json();
      if (!oppRes.ok) {
        setMsg(oppBody.message ?? `HTTP ${oppRes.status}`);
        return;
      }
      setOpp(oppBody);
      if (histRes.ok) {
        const histBody = await histRes.json();
        setHistory(histBody.items ?? []);
      }
    } catch (e) {
      setMsg((e as Error).message);
    }
  }, [id]);

  useEffect(() => {
    if (id) load();
  }, [id, load]);

  const transition = async (to: string) => {
    setBusy(true);
    setMsg("");
    try {
      const res = await fetch(`/api/opportunities/${id}/stage`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ to_stage: to }),
      });
      const body = await res.json();
      if (!res.ok) {
        setMsg(body.message ?? body.error ?? `HTTP ${res.status}`);
      } else {
        setMsg(`已更新为「${STAGE_LABELS[to as keyof typeof STAGE_LABELS] ?? to}」`);
        await load();
      }
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (!opp && !msg) return <main><p className="muted">加载中…</p></main>;
  if (!opp) {
    return (
      <main>
        <h1>商机详情</h1>
        <p className="muted">不可见或不存在:{msg}(权限外记录一律 404,不泄露存在性)</p>
        <Link href="/opportunities">返回商机列表</Link>
      </main>
    );
  }

  const stage = opp.stage as keyof typeof STAGE_LABELS;
  const amt = amountView(opp.amount_cents, opp.amount_source);
  const allowed = canTransitionStage(me ?? {}, opp);
  const actions = stageActionsFor(opp.stage as (typeof OPPORTUNITY_STAGES)[number], allowed);

  return (
    <main>
      <h1>{opp.title}</h1>
      <p className="muted">类别:{opp.business_category}(按类别隔离展示与统计)</p>
      <ul>
        <li>当前阶段:{STAGE_LABELS[stage] ?? opp.stage}{opp.stage === "won" ? "(仅人工标记,非收款事实)" : ""}</li>
        <li>金额:{amt.text}{amt.unknown ? "" : `(${amt.sourceLabel})`}</li>
        <li>{probabilityText(opp.probability)}</li>
        <li>{expectedCloseText(opp.expected_close_at)}</li>
      </ul>

      {allowed ? (
        <div>
          <h2>阶段操作</h2>
          {actions.map((a) => (
            <button key={a.to} disabled={busy} onClick={() => transition(a.to)}>
              {a.label}
            </button>
          ))}
          <p className="muted">每次转换都会写入审计历史;重复点击当前阶段为幂等操作,不产生新历史。</p>
        </div>
      ) : (
        <p className="muted">无阶段操作权限(仅记录负责人或租户 owner 可转换阶段)。</p>
      )}

      {msg ? <p className="muted">{msg}</p> : null}

      <h2>阶段时间线</h2>
      <ol>
        {history.map((e) => (
          <li key={e.changed_at + e.to_stage}>
            {timelineLabel(e)} — {e.changed_at}
          </li>
        ))}
      </ol>

      <p>
        <Link href="/opportunities">返回商机列表</Link>
      </p>
    </main>
  );
}
