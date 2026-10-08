"use client";

import { useCallback, useEffect, useState } from "react";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText } from "@/lib/productShell";
import { presentCampaignMotion } from "@/lib/campaignMotion";

interface MotionExport {
  campaign_goal?: string;
  target_audience?: string;
  approved_facts?: Array<{ key?: string; value?: string }>;
  authorized_asset_ids?: string[];
  cta?: string;
  channels?: string[];
  budget_attribution_id?: string;
  motion?: { project_id?: string; revision_id?: string; digest?: string };
}

interface MotionResponse {
  export?: MotionExport | null;
  error?: string;
  message?: string;
}

export function CampaignMotionPanel({ campaignId }: { campaignId: string }) {
  const scope = useCrmScope();
  const [hidden, setHidden] = useState(true);
  const [goal, setGoal] = useState("");
  const [audience, setAudience] = useState("");
  const [fact, setFact] = useState("");
  const [assetID, setAssetID] = useState("");
  const [cta, setCta] = useState("");
  const [channels, setChannels] = useState("");
  const [budgetID, setBudgetID] = useState("");
  const [projectID, setProjectID] = useState("");
  const [revisionID, setRevisionID] = useState("");
  const [digest, setDigest] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [recorded, setRecorded] = useState(false);
  const view = presentCampaignMotion({ channels: channels.split(/[\s,，、]+/).filter(Boolean) });

  const applyExport = useCallback((doc: MotionExport | null | undefined) => {
    if (!doc) return;
    setRecorded(true);
    setGoal(doc.campaign_goal ?? "");
    setAudience(doc.target_audience ?? "");
    setFact(doc.approved_facts?.[0]?.value ?? "");
    setAssetID(doc.authorized_asset_ids?.[0] ?? "");
    setCta(doc.cta ?? "");
    setChannels((doc.channels ?? []).join("、"));
    setBudgetID(doc.budget_attribution_id ?? "");
    setProjectID(doc.motion?.project_id ?? "");
    setRevisionID(doc.motion?.revision_id ?? "");
    setDigest(doc.motion?.digest ?? "");
  }, []);

  const load = useCallback(async () => {
    if (!campaignId || !scope.tenant) return;
    const init = scopeInit(scope.tenant);
    const capRes = await fetch("/api/campaign-motion/capability", init);
    if (!capRes.ok) {
      setHidden(true);
      return;
    }
    setHidden(false);
    const res = await fetch(`/api/campaigns/${campaignId}/motion-handoff`, init);
    if (res.status === 404) {
      setMessage("当前线索不是活动。这一步只交出活动事实。");
      return;
    }
    if (!res.ok) {
      const errBody = (await res.json().catch(() => null)) as MotionResponse | null;
      setMessage(failureText(errBody, res.status));
      return;
    }
    const body = (await res.json()) as MotionResponse;
    applyExport(body.export);
  }, [applyExport, campaignId, scope.tenant]);

  useEffect(() => {
    void load();
  }, [load]);

  if (hidden) return null;

  async function save() {
    if (!scope.tenant) return;
    setBusy(true);
    setMessage("");
    const names = channels.split(/[\s,，、]+/).map((item) => item.trim()).filter(Boolean);
    const payload: Record<string, unknown> = {
      campaign_goal: goal.trim(),
      target_audience: audience.trim(),
      cta: cta.trim(),
      channels: names,
      budget_attribution_id: budgetID.trim(),
      motion: {
        project_id: projectID.trim(),
        revision_id: revisionID.trim(),
        campaign_id: campaignId,
        digest: digest.trim(),
      },
    };
    if (fact.trim()) payload.approved_facts = [{ key: "fact", value: fact.trim(), confirmed: true }];
    if (assetID.trim()) payload.assets = [{ id: assetID.trim(), authorized: true }];
    const headers = new Headers(scopeInit(scope.tenant).headers);
    headers.set("content-type", "application/json");
    const res = await fetch(`/api/campaigns/${campaignId}/motion-handoff`, {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
    });
    const body = (await res.json().catch(() => null)) as MotionResponse | null;
    setBusy(false);
    if (!res.ok) {
      setMessage(failureText(body, res.status));
      return;
    }
    applyExport(body?.export);
    setMessage(presentCampaignMotion(null).status);
  }

  return (
    <section className="card stack-form">
      <h2>活动动效交出</h2>
      <p data-testid="campaign-motion-notice">{view.notice}</p>
      {recorded ? <p data-testid="campaign-motion-status">{view.status}</p> : null}
      <p>{view.conversionNote}</p>
      <label>
        活动目标
        <input value={goal} onChange={(e) => setGoal(e.target.value)} />
      </label>
      <label>
        目标受众
        <input value={audience} onChange={(e) => setAudience(e.target.value)} />
      </label>
      <label>
        已确认的品牌或产品事实
        <input value={fact} onChange={(e) => setFact(e.target.value)} />
      </label>
      <label>
        已授权素材 id
        <input value={assetID} onChange={(e) => setAssetID(e.target.value)} />
      </label>
      <label>
        CTA
        <input value={cta} onChange={(e) => setCta(e.target.value)} />
      </label>
      <label>
        渠道名
        <input value={channels} onChange={(e) => setChannels(e.target.value)} placeholder="抖音、视频号" />
      </label>
      <label>
        预算归属 id
        <input value={budgetID} onChange={(e) => setBudgetID(e.target.value)} />
      </label>
      <label>
        Motion project_id
        <input value={projectID} onChange={(e) => setProjectID(e.target.value)} />
      </label>
      <label>
        Motion revision_id
        <input value={revisionID} onChange={(e) => setRevisionID(e.target.value)} />
      </label>
      <label>
        Motion digest
        <input value={digest} onChange={(e) => setDigest(e.target.value)} />
      </label>
      <p>
        渠道：{view.channels.join("、") || "还没有渠道名"}
      </p>
      <button type="button" disabled={busy || !goal.trim() || !budgetID.trim() || view.channels.length === 0} onClick={() => void save()}>
        记下交出
      </button>
      {message ? <p>{message}</p> : null}
    </section>
  );
}
