"use client";

import Link from "next/link";
import { useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { centsText, presentRoi, STATUS_LABELS, type RoiReport } from "@/lib/roi";

// HUI-1696 来源链与费用复算页。只打本站 BFF。页面不保存第二份线索或费用。

export default function AttributionPage() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState(scope.tenantId ?? "");
  const [raw, setRaw] = useState("");
  const [report, setReport] = useState<RoiReport | null>(null);
  const [err, setErr] = useState("");
  const view = presentRoi(report);

  async function recalculate() {
    const tenantId = tenant.trim();
    if (!tenantId) {
      setErr("先填写当前租户");
      setReport(null);
      return;
    }
    if (tenantId !== scope.tenantId) {
      commitCrmTenant(tenantId);
    }
    let body: unknown;
    try {
      body = raw.trim() ? JSON.parse(raw) : {};
    } catch {
      setErr("引用需要是 JSON");
      setReport(null);
      return;
    }
    setErr("");
    const res = await fetch("/api/roi/recalculate", {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": tenantId },
      body: JSON.stringify(body),
    });
    const data = (await res.json().catch(() => ({}))) as RoiReport & { message?: string; error?: string };
    if (!res.ok) {
      setReport(null);
      setErr(data.message ?? data.error ?? `HTTP ${res.status}`);
      return;
    }
    setReport(data);
  }

  return (
    <main>
      <h1>来源与费用复算</h1>
      <p className="muted">
        <Link href="/">返回工作台</Link>
      </p>
      <div className="card">
        <p>{view.disclaimer}</p>
        <p className="muted">{view.funnelReuse}</p>
        <p className="muted">当前状态：{STATUS_LABELS[view.roiStatus] ?? view.roiStatus}。这不是营销提升证明。</p>
      </div>
      <div className="card">
        <label>
          当前租户{" "}
          <input value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="租户 id" />
        </label>
        <p>
          <label>
            引用 JSON
            <textarea
              value={raw}
              onChange={(e) => setRaw(e.target.value)}
              rows={8}
              style={{ display: "block", width: "100%", marginTop: "0.4rem" }}
              placeholder="粘贴来源链、事件、费用与收入引用"
            />
          </label>
        </p>
        <button type="button" onClick={() => void recalculate()}>
          复算
        </button>
        {err ? <p className="muted">{err}</p> : null}
      </div>
      <div className="card">
        <h2>收入分开看</h2>
        <ul>
          {view.revenueLines.map((line) => (
            <li key={line.key}>
              {line.label}
              {line.authority ? `（${line.authority}）` : ""}：{centsText(line.cents)}
              {view.showCompleteRoi ? `，ROI ${line.roiText}` : "，ROI 不展示"}
            </li>
          ))}
        </ul>
      </div>
      {view.metricLines.length > 0 ? (
        <div className="card">
          <h2>原口径指标</h2>
          <ul>
            {view.metricLines.map((line) => (
              <li key={line.key}>
                {line.channel ? `${line.channel} ` : ""}
                {line.kind}：{line.text}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {view.stageLines.length > 0 ? (
        <div className="card">
          <h2>过程分开计</h2>
          <ul>
            {view.stageLines.map((line) => (
              <li key={line.key}>
                {line.key}：{line.text}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {report?.source_chain && report.source_chain.length > 0 ? (
        <div className="card">
          <h2>受限来源链</h2>
          <ul>
            {report.source_chain.map((node) => (
              <li key={node.id}>
                {node.kind} {node.ref || node.id}
                {node.gap ? " · 未关联" : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </main>
  );
}
