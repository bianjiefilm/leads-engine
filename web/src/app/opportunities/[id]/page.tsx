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
import {
  REVOKE_ACCEPTED_COPY,
  canCreateServiceDraft,
  handoffStatusLine,
  missingText,
  serviceDraftActions,
  type ServiceDraftAssetInput,
  type ServiceDraftHandoff,
  type ServiceDraftPreview,
} from "@/lib/serviceDraft";

// 商机详情(HUI-1693):阶段时间线(审计链)+ 金额来源标识 + 按权限显隐的
// 阶段操作按钮。按钮显隐只是 UI 镜像;服务端对每次转换重新鉴权(非 assignee
// 的记录对本角色直接 404 不可见)。
//
// 服务需求草稿交接(HUI-1749):「创建服务需求草稿」按钮只对
// creative_service + 有更新权限的用户渲染(merchant_customer 商机上这个
// 动作不存在);预览 → 用户显式确认 → 幂等快照投递。无 AI 自动触发。

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

interface AssetRow {
  asset_ref: string;
  sha256: string;
  size_bytes: string;
  media_type: string;
}

const emptyAsset: AssetRow = { asset_ref: "", sha256: "", size_bytes: "", media_type: "" };

export default function OpportunityDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const [opp, setOpp] = useState<OpportunityDetail | null>(null);
  const [history, setHistory] = useState<StageEvent[]>([]);
  const [me, setMe] = useState<WhoamiBody | null>(null);
  const [msg, setMsg] = useState<string>("");
  const [busy, setBusy] = useState(false);

  // 服务需求草稿状态
  const [draft, setDraft] = useState<ServiceDraftHandoff | null>(null);
  const [preview, setPreview] = useState<ServiceDraftPreview | null>(null);
  const [draftMsg, setDraftMsg] = useState<string>("");
  const [summary, setSummary] = useState("");
  const [serviceCategory, setServiceCategory] = useState("");
  const [budgetYuan, setBudgetYuan] = useState("");
  const [deadline, setDeadline] = useState("");
  const [assets, setAssets] = useState<AssetRow[]>([{ ...emptyAsset }]);

  const draftAllowed = !!opp && canCreateServiceDraft(me ?? {}, opp);

  const loadDraft = useCallback(async () => {
    try {
      const res = await fetch(`/api/opportunities/${id}/service-draft`);
      if (res.ok) {
        const body = await res.json();
        setDraft(body.handoff ?? null);
      } else {
        setDraft(null); // 404:尚未确认过(或功能未开启)
      }
    } catch {
      setDraft(null);
    }
  }, [id]);

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

  useEffect(() => {
    if (id && draftAllowed) loadDraft();
  }, [id, draftAllowed, loadDraft]);

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

  const buildInput = (confirm: boolean) => {
    const cents = budgetYuan.trim() === "" ? null : Math.round(parseFloat(budgetYuan) * 100);
    const kept = assets.filter((a) => a.asset_ref.trim() !== "" || a.sha256.trim() !== "");
    return {
      confirm,
      summary,
      service_category: serviceCategory,
      budget_cents: cents !== null && !Number.isNaN(cents) ? cents : null,
      deadline,
      assets: kept.map(
        (a): ServiceDraftAssetInput => ({
          asset_ref: a.asset_ref,
          sha256: a.sha256,
          size_bytes: Number(a.size_bytes) || 0,
          media_type: a.media_type,
        }),
      ),
    };
  };

  const draftCall = async (
    path: string,
    init: { method: string; body?: string },
  ): Promise<Record<string, unknown> | null> => {
    setBusy(true);
    setDraftMsg("");
    try {
      const res = await fetch(`/api/opportunities/${id}/service-draft${path}`, {
        method: init.method,
        headers: { "content-type": "application/json" },
        body: init.body,
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        setDraftMsg(body.message ?? body.error ?? `HTTP ${res.status}`);
        return null;
      }
      return body as Record<string, unknown>;
    } catch (e) {
      setDraftMsg((e as Error).message);
      return null;
    } finally {
      setBusy(false);
    }
  };

  const doPreview = async () => {
    setPreview(null);
    const body = await draftCall("-intent", { method: "POST", body: JSON.stringify(buildInput(false)) });
    if (body) setPreview(body.preview as ServiceDraftPreview);
  };

  const doConfirm = async () => {
    const body = await draftCall("-intent", { method: "POST", body: JSON.stringify(buildInput(true)) });
    if (body) {
      setPreview(null);
      if (body.handoff) setDraft(body.handoff as ServiceDraftHandoff);
      const note = typeof body.note === "string" ? body.note : "";
      setDraftMsg(body.duplicate ? `同一内容已确认过:返回既有交接引用(幂等)。${note}` : note);
    }
  };

  const doDraftAction = async (action: "refresh" | "retry" | "revoke") => {
    const body = await draftCall(`/${action}`, { method: "POST", body: "{}" });
    if (body?.handoff) {
      setDraft(body.handoff as ServiceDraftHandoff);
      if (typeof body.note === "string") setDraftMsg(body.note);
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
  const draftActions = draft ? serviceDraftActions(draft) : null;

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

      {draftAllowed ? (
        <div>
          <h2>创建服务需求草稿</h2>
          <p className="muted">
            把已确认的需求事实交接给接单应用生成草稿;只携带你在下方显式确认的内容,缺失字段按缺失交接,绝不从 CRM 备注猜测。投递成功不代表成交。
          </p>
          <div>
            <p>
              <label>
                需求摘要(必填)
                <br />
                <textarea value={summary} onChange={(e) => setSummary(e.target.value)} rows={3} cols={60} />
              </label>
            </p>
            <p>
              <label>
                服务类别(如 video-editing)
                <br />
                <input value={serviceCategory} onChange={(e) => setServiceCategory(e.target.value)} size={40} />
              </label>
            </p>
            <p>
              <label>
                预算(元,可留空=缺失)
                <input value={budgetYuan} onChange={(e) => setBudgetYuan(e.target.value)} size={12} />
              </label>
              {"  "}
              <label>
                截止日期(可留空=缺失)
                <input type="date" value={deadline} onChange={(e) => setDeadline(e.target.value)} />
              </label>
            </p>
            <p className="muted">品牌/素材资产:每一项都必须填写完整引用(含 sha256),缺 hash 的引用一律拒绝,系统绝不代填。</p>
            {assets.map((a, i) => (
              <p key={i}>
                <input placeholder="资产引用" value={a.asset_ref} onChange={(e) => setAssets(assets.map((x, j) => (j === i ? { ...x, asset_ref: e.target.value } : x)))} size={24} />
                {" "}
                <input placeholder="sha256(64 位十六进制)" value={a.sha256} onChange={(e) => setAssets(assets.map((x, j) => (j === i ? { ...x, sha256: e.target.value } : x)))} size={66} />
                {" "}
                <input placeholder="字节数" value={a.size_bytes} onChange={(e) => setAssets(assets.map((x, j) => (j === i ? { ...x, size_bytes: e.target.value } : x)))} size={10} />
                {" "}
                <input placeholder="媒体类型" value={a.media_type} onChange={(e) => setAssets(assets.map((x, j) => (j === i ? { ...x, media_type: e.target.value } : x)))} size={14} />
                {assets.length > 1 ? <button disabled={busy} onClick={() => setAssets(assets.filter((_, j) => j !== i))}>移除</button> : null}
              </p>
            ))}
            <button disabled={busy} onClick={() => setAssets([...assets, { ...emptyAsset }])}>添加资产行</button>
          </div>
          <p>
            <button disabled={busy} onClick={doPreview}>生成预览</button>
          </p>
          {preview ? (
            <div>
              <h3>预览(尚未提交)</h3>
              <ul>
                <li>摘要:{preview.summary || "(空)"}</li>
                <li>服务类别:{preview.service_category || "(空)"}</li>
                <li>预算:{preview.budget_cents !== null && preview.budget_cents !== undefined ? `¥${(preview.budget_cents / 100).toFixed(2)}` : "(空)"}</li>
                <li>截止:{preview.deadline || "(空)"}</li>
                <li>资产:{preview.assets.length > 0 ? preview.assets.map((a) => a.asset_ref).join("、") : "(无)"}</li>
                <li className="muted">{missingText(preview.missing)}</li>
                <li className="muted">接收方:{preview.receiver?.target_app};授权范围:{(preview.receiver?.scopes ?? []).join("、")}</li>
                <li className="muted">{preview.note}</li>
              </ul>
              <button disabled={busy} onClick={doConfirm}>确认创建草稿交接</button>
            </div>
          ) : null}
          {draft ? (
            <div>
              <h3>交接状态</h3>
              <p>{handoffStatusLine(draft)}</p>
              <p className="muted">
                handoff_id:{draft.handoff_id};版本 v{draft.source_version};接单侧状态:{draft.target_status || "(未知)"}
              </p>
              <p>
                {draftActions?.canRefresh ? <button disabled={busy} onClick={() => doDraftAction("refresh")}>刷新接单侧状态</button> : null}
                {" "}
                {draftActions?.canRetry ? <button disabled={busy} onClick={() => doDraftAction("retry")}>重试投递</button> : null}
                {" "}
                {draftActions?.canRevoke ? <button disabled={busy} onClick={() => doDraftAction("revoke")}>撤销交接</button> : null}
              </p>
              {draftActions && !draftActions.canRevoke && draft.local_status !== "revoked" ? (
                <p className="muted">{REVOKE_ACCEPTED_COPY}</p>
              ) : null}
            </div>
          ) : null}
          {draftMsg ? <p className="muted">{draftMsg}</p> : null}
        </div>
      ) : null}

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

