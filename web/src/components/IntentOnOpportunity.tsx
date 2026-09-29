"use client";

import { useCallback, useEffect, useState } from "react";
import { scopeInit } from "@/lib/eco-nav/use-crm-scope";
import {
  modelStatusText,
  originLabel,
  outreachButtons,
  outreachGranted,
  presentAssessment,
  salesCorrectionActions,
  type GradeInput,
} from "@/lib/intentGrade";

interface LeadRow {
  id: string;
  contact_id?: string;
}

interface GradeSnapshot {
  id?: string;
  grade?: string;
  reason?: string;
  citations?: { evidence_id: string; excerpt: string }[];
  missing_fields?: string[];
  fresh_until?: string;
  rule_version?: string;
  model_version?: string;
  calibrated?: boolean;
  disclaimer?: string;
  suggestion?: { kind?: string; label?: string };
  human_locked?: boolean;
  stale?: boolean;
  stale_reason?: string;
  grade_origin?: string;
  permissions?: { call?: boolean; direct_message?: boolean; create_order?: boolean };
}

function asInput(snap: GradeSnapshot): GradeInput {
  return {
    grade: snap.grade ?? "",
    reason: snap.reason ?? "",
    citations: snap.citations ?? [],
    missing_fields: snap.missing_fields ?? [],
    fresh_until: snap.fresh_until ?? "",
    rule_version: snap.rule_version ?? "",
    model_version: snap.model_version ?? "",
    calibrated: snap.calibrated === true,
    disclaimer: snap.disclaimer ?? "",
    suggestion: { kind: snap.suggestion?.kind ?? "", label: snap.suggestion?.label ?? "" },
    human_locked: snap.human_locked === true,
    stale: snap.stale === true,
    stale_reason: snap.stale_reason,
  };
}

export function IntentOnOpportunity({
  contactId,
  tenantId,
}: {
  contactId?: string;
  tenantId?: string | null;
}) {
  const [leads, setLeads] = useState<LeadRow[]>([]);
  const [note, setNote] = useState("");
  const [rows, setRows] = useState<Record<string, GradeSnapshot | null>>({});
  const [text, setText] = useState<Record<string, string>>({});
  const [grade, setGrade] = useState("low");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (!contactId) {
      setNote("这个商机没有关联联系人，不编造级别。");
      setLeads([]);
      return;
    }
    if (!tenantId) {
      setNote("先选择租户，再查看同一联系人的线索。");
      setLeads([]);
      return;
    }
    const res = await fetch("/api/leads", scopeInit(tenantId));
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      setNote(body.message ?? "线索列表没有读到");
      setLeads([]);
      return;
    }
    const matched = ((body.items ?? []) as LeadRow[]).filter((item) => item.contact_id === contactId);
    setLeads(matched);
    if (matched.length === 0) {
      setNote("这个联系人还没有线索，不编造级别。");
      setRows({});
      return;
    }
    setNote("");
    const next: Record<string, GradeSnapshot | null> = {};
    for (const lead of matched) {
      const current = await fetch(
        `/api/intent-grades/current?subject_kind=lead&subject_id=${encodeURIComponent(lead.id)}`,
        scopeInit(tenantId),
      );
      next[lead.id] = current.ok ? ((await current.json()) as GradeSnapshot) : null;
    }
    setRows(next);
  }, [contactId, tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  const score = async (leadId: string) => {
    if (!tenantId) return;
    setBusy(true);
    try {
      const evidenceText = (text[leadId] ?? "").trim();
      const res = await fetch(
        "/api/intent-grades",
        scopeInit(tenantId, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            subject_kind: "lead",
            subject_id: leadId,
            evidence: evidenceText ? [{ id: "ev-opportunity", tenant_id: tenantId, text: evidenceText }] : [],
          }),
        }),
      );
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        setNote(body.message ?? body.error ?? "评估没有完成");
        return;
      }
      setNote("");
      await load();
    } finally {
      setBusy(false);
    }
  };

  const correct = async (leadId: string, disposition: "adopted" | "rejected") => {
    if (!tenantId) return;
    const snap = rows[leadId];
    if (!snap?.id || !reason.trim()) {
      setNote("修正要写下原因");
      return;
    }
    setBusy(true);
    try {
      const res = await fetch(
        `/api/intent-grades/${snap.id}/corrections`,
        scopeInit(tenantId, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            grade,
            disposition,
            misjudgment: disposition === "rejected",
            reason: reason.trim(),
            facts: [{ field: "intent", value: grade }],
          }),
        }),
      );
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        setNote(body.message ?? body.error ?? "修正没有保存");
        return;
      }
      setNote("");
      await load();
    } finally {
      setBusy(false);
    }
  };

  return (
    <section>
      <h2>意向分级</h2>
      <p>{modelStatusText()}</p>
      <p className="muted">级别只有高、中、低、信息不足。下一步只是建议。</p>
      {note ? <p className="muted">{note}</p> : null}
      {leads.map((lead) => {
        const snap = rows[lead.id];
        const view = snap?.grade ? presentAssessment(asInput(snap)) : null;
        const buttons = outreachButtons(snap?.permissions);
        return (
          <div key={lead.id} className="card">
            <p>线索 {lead.id}</p>
            {view ? (
              <>
                <p>
                  {view.label} · 来源 {originLabel(snap?.grade_origin ?? "rules")}
                  {view.humanLocked ? " · 人工已确认" : ""}
                </p>
                <p>{view.reason}</p>
                <p>{view.suggestion}</p>
                <p className="muted">
                  版本 {view.version} · 有效至 {view.freshness || "未给出"}
                </p>
                {view.staleText ? <p>{view.staleText}</p> : null}
                <p>证据：{view.citations.length ? view.citations.join("；") : "没有可引用的证据"}</p>
                <p>缺失：{view.missing.length ? view.missing.join("、") : "没有列出缺失字段"}</p>
                <p className="muted">{view.disclaimer}</p>
              </>
            ) : (
              <p className="muted">还没有分级。</p>
            )}
            {outreachGranted(snap?.permissions) ? <p>返回了触达许可，此页只保留下一步建议。</p> : null}
            {buttons.length > 0 ? <p>{buttons.join("、")}</p> : null}
            <p>
              <label>
                本租户可见的原文
                <br />
                <textarea
                  value={text[lead.id] ?? ""}
                  onChange={(e) => setText((prev) => ({ ...prev, [lead.id]: e.target.value }))}
                  rows={3}
                  cols={60}
                />
              </label>
            </p>
            <button type="button" disabled={busy} onClick={() => void score(lead.id)}>
              用规则重新评估
            </button>
            {snap?.id ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                }}
              >
                <label>
                  分级{" "}
                  <select value={grade} onChange={(e) => setGrade(e.target.value)}>
                    <option value="high">高</option>
                    <option value="medium">中</option>
                    <option value="low">低</option>
                    <option value="insufficient">信息不足</option>
                  </select>
                </label>
                <p>
                  <label>
                    原因
                    <br />
                    <textarea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} cols={60} />
                  </label>
                </p>
                {salesCorrectionActions().map((action) => (
                  <button
                    key={action}
                    type="button"
                    disabled={busy}
                    onClick={() => void correct(lead.id, action === "驳回" ? "rejected" : "adopted")}
                  >
                    {action}
                  </button>
                ))}
              </form>
            ) : null}
          </div>
        );
      })}
    </section>
  );
}
