"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { modelStatusText, originLabel, outreachControls, presentAssessment, type GradeInput, type GradeView } from "@/lib/intentGrade";

// HUI-1684 意向分级页。只打本站 BFF。规则版本由服务端返回。
// 页面只展示下一步建议，不外呼、不发短信、不拉群、不建单。

interface SnapshotBody {
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
  error?: string;
  message?: string;
}

interface ScoreResponse extends SnapshotBody {
  snapshot?: SnapshotBody;
}

interface ReportOutcome {
  id: string;
  kind: string;
  label: string;
  predicted: string;
  match: boolean;
  reason: string;
  suggestion: string;
  origin?: string;
}

const SCREEN_EXAMPLE: GradeInput = {
  grade: "high",
  reason: "出现采购、合同或打款等购买承诺。",
  citations: [{ evidence_id: "sample-strong", excerpt: "我们这周要采购 50 套，请发合同和报价。" }],
  missing_fields: ["buyer"],
  fresh_until: "2026-09-29T08:00:00Z",
  rule_version: "rules-hui-1684-v1",
  model_version: "none",
  calibrated: false,
  disclaimer: "规则评分，不是真人成交预测，也不是校准后的成交概率。",
  suggestion: {
    kind: "suggest_follow_up",
    label: "建议销售人工确认下一步。是否可联系由授权和渠道规则决定。",
  },
  human_locked: false,
  stale: false,
};

function ExampleCard() {
  const view = presentAssessment(SCREEN_EXAMPLE);
  return (
    <div className="card">
      <h2>界面样例 · {view.label}</h2>
      <p className="muted">这是标注样本的展示方式，不是某位客户的成交预测。</p>
      <p>{view.reason}</p>
      <p>{view.suggestion}</p>
      <p className="muted">
        版本 {view.version} · 有效至 {view.freshness}
      </p>
      <p>证据：{view.citations.join("；")}</p>
      <p>缺失：{view.missing.join("、")}</p>
      <p className="muted">{view.disclaimer}</p>
      <p className="muted">成交概率：不提供</p>
      {view.actions.map((action) => (
        <span key={action} className="tab">
          {action}
        </span>
      ))}
    </div>
  );
}

const KIND_LABEL: Record<string, string> = {
  strong_intent: "强意向",
  ordinary_qa: "普通问答",
  after_sales: "售后",
  missing_data: "缺数据",
  refuse_marketing: "拒绝联系",
};

function asInput(snap: SnapshotBody): GradeInput {
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
    suggestion: {
      kind: snap.suggestion?.kind ?? "",
      label: snap.suggestion?.label ?? "",
    },
    human_locked: snap.human_locked === true,
    stale: snap.stale === true,
    stale_reason: snap.stale_reason,
  };
}

export default function IntentGradePage() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState("");
  const [subjectKind, setSubjectKind] = useState("lead");
  const [subjectId, setSubjectId] = useState("");
  const [text, setText] = useState("");
  const [snapshotId, setSnapshotId] = useState("");
  const [view, setView] = useState<GradeView | null>(null);
  const [error, setError] = useState("");
  const [grade, setGrade] = useState("low");
  const [disposition, setDisposition] = useState("rejected");
  const [correctionReason, setCorrectionReason] = useState("");
  const [misjudgment, setMisjudgment] = useState(true);
  const [report, setReport] = useState<ReportOutcome[] | null>(null);
  const [reportNote, setReportNote] = useState("");

  useEffect(() => {
    setTenant(scope.tenantId ?? "");
  }, [scope.epoch, scope.tenantId]);

  const headers = useCallback((): HeadersInit => ({ "content-type": "application/json", "x-tenant-id": tenant.trim() }), [tenant]);

  const loadReport = useCallback(async () => {
    setReportNote("");
    if (!tenant.trim()) {
      setReport(null);
      setReportNote("先填写当前租户，再看标注样本。这些样本不是真人成交记录。");
      return;
    }
    const res = await fetch("/api/intent-grades/sample-report", { headers: { "x-tenant-id": tenant.trim() } });
    const body = await res.json();
    if (!res.ok) {
      setReport(null);
      setReportNote(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    setReport((body.outcomes ?? []) as ReportOutcome[]);
    setReportNote(typeof body.disclaimer === "string" ? body.disclaimer : "");
  }, [tenant]);

  const score = async () => {
    setError("");
    setView(null);
    if (!tenant.trim() || !subjectId.trim()) {
      setError("先填写当前租户和线索或会话编号");
      return;
    }
    const res = await fetch("/api/intent-grades", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify({
        subject_kind: subjectKind,
        subject_id: subjectId.trim(),
        evidence: text.trim() ? [{ id: "ev-page", tenant_id: tenant.trim(), text: text.trim() }] : [],
      }),
    });
    const body = (await res.json()) as ScoreResponse;
    if (!res.ok) {
      setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    const snap = body.snapshot ?? body;
    setSnapshotId(snap.id ?? "");
    setView(presentAssessment(asInput(snap)));
  };

  const correct = async () => {
    setError("");
    if (!snapshotId || !correctionReason.trim()) {
      setError("修正要写下采纳或驳回的原因");
      return;
    }
    const res = await fetch(`/api/intent-grades/${snapshotId}/corrections`, {
      method: "POST",
      headers: headers(),
      body: JSON.stringify({
        grade,
        disposition,
        misjudgment,
        reason: correctionReason.trim(),
        facts: [{ field: "intent", value: grade }],
      }),
    });
    const body = (await res.json()) as ScoreResponse;
    if (!res.ok) {
      setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    const snap = body.snapshot ?? body;
    setSnapshotId(snap.id ?? snapshotId);
    setView(presentAssessment(asInput(snap)));
  };

  const shown = view;
  const outreach = shown ? outreachControls(shown) : [];

  return (
    <main>
      <p>
        <Link href="/">返回工作台</Link>
      </p>
      <h1>意向分级</h1>
      <div className="card">
        <p>规则评分，不是真人成交预测，也不是校准后的成交概率。下一步只给建议，不自动外呼、短信、拉群或创建订单。</p>
        <p>{modelStatusText()}</p>
        <label>
          当前租户{" "}
          <input value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="租户 id" />
        </label>{" "}
        <button
          type="button"
          onClick={() => {
            const value = tenant.trim();
            if (!value) return;
            if (value === scope.tenantId) return;
            commitCrmTenant(value);
          }}
        >
          使用这个租户
        </button>
        <p>
          <label>
            对象{" "}
            <select value={subjectKind} onChange={(e) => setSubjectKind(e.target.value)}>
              <option value="lead">线索</option>
              <option value="session">会话</option>
            </select>
          </label>{" "}
          <label>
            编号 <input value={subjectId} onChange={(e) => setSubjectId(e.target.value)} placeholder="线索或会话 id" />
          </label>
        </p>
        <p>
          <label>
            本租户可见的原文
            <br />
            <textarea value={text} onChange={(e) => setText(e.target.value)} rows={4} cols={60} />
          </label>
        </p>
        <button type="button" onClick={score}>
          用规则重新评估
        </button>
        <p className="muted">重算沿用同一条用量记录，不重复扣费。人工确认过的事实不会被这次评估盖掉。</p>
      </div>
      <ExampleCard />
      {error ? <p className="muted">{error}</p> : null}
      {shown ? (
        <div className="card">
          <h2>
            {shown.label}
            {shown.humanLocked ? " · 人工已确认" : ""}
          </h2>
          <p>{shown.reason}</p>
          <p>{shown.suggestion}</p>
          <p className="muted">
            版本 {shown.version} · 有效至 {shown.freshness || "未给出"}
          </p>
          {shown.staleText ? <p>{shown.staleText}</p> : null}
          <p>证据：{shown.citations.length ? shown.citations.join("；") : "没有可引用的证据"}</p>
          <p>缺失：{shown.missing.length ? shown.missing.join("、") : "没有列出缺失字段"}</p>
          <p className="muted">{shown.disclaimer}</p>
          <p className="muted">成交概率：不提供</p>
          {shown.actions.map((action) => (
            <button key={action} type="button" onClick={() => document.getElementById("intent-correction")?.scrollIntoView()}>
              {action}
            </button>
          ))}
          {outreach.length > 0 ? <p>触达：{outreach.join("、")}</p> : null}
          <form
            id="intent-correction"
            onSubmit={(e) => {
              e.preventDefault();
              void correct();
            }}
          >
            <h3>人工修正</h3>
            <label>
              分级{" "}
              <select value={grade} onChange={(e) => setGrade(e.target.value)}>
                <option value="high">高</option>
                <option value="medium">中</option>
                <option value="low">低</option>
                <option value="insufficient">信息不足</option>
              </select>
            </label>{" "}
            <label>
              结论{" "}
              <select value={disposition} onChange={(e) => setDisposition(e.target.value)}>
                <option value="rejected">驳回</option>
                <option value="adopted">采纳</option>
              </select>
            </label>{" "}
            <label>
              <input type="checkbox" checked={misjudgment} onChange={(e) => setMisjudgment(e.target.checked)} /> 标记误判
            </label>
            <p>
              <label>
                原因
                <br />
                <textarea value={correctionReason} onChange={(e) => setCorrectionReason(e.target.value)} rows={3} cols={60} />
              </label>
            </p>
            <button type="submit">保存修正</button>
          </form>
        </div>
      ) : null}
      <div className="card">
        <h2>标注样本</h2>
        <button type="button" onClick={() => void loadReport()}>
          查看样本报告
        </button>
        {reportNote ? <p className="muted">{reportNote}</p> : null}
        {report ? (
          <table>
            <thead>
              <tr>
                <th>样本</th>
                <th>标注</th>
                <th>规则结果</th>
                <th>理由</th>
                <th>建议</th>
                <th>来源</th>
                <th>是否一致</th>
              </tr>
            </thead>
            <tbody>
              {report.map((row) => (
                <tr key={row.id}>
                  <td>{KIND_LABEL[row.kind] ?? row.kind}</td>
                  <td>{row.label}</td>
                  <td>{row.predicted}</td>
                  <td>{row.reason}</td>
                  <td>{row.suggestion}</td>
                  <td>{originLabel(row.origin || "fixture")}</td>
                  <td>{row.match ? "一致" : "误判"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : null}
      </div>
    </main>
  );
}
