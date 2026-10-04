"use client";

import Link from "next/link";
import { useState } from "react";
import { SurfaceState } from "@/components/workbench/chrome";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, pagePrimary, productError } from "@/lib/productShell";
import { centsText, presentRoi, STATUS_LABELS, type RoiReport } from "@/lib/roi";

// HUI-1696 来源链与费用复算页。只打本站 BFF。页面不保存第二份线索或费用。

export default function AttributionPage() {
  const scope = useCrmScope();
  const tenantId = scope.tenantId ?? "";
  const [raw, setRaw] = useState("");
  const [report, setReport] = useState<RoiReport | null>(null);
  const [err, setErr] = useState("");
  const view = presentRoi(report);

  async function recalculate() {
    if (!tenantId) {
      setErr(MISSING_SCOPE);
      setReport(null);
      return;
    }
    let body: unknown;
    try {
      body = raw.trim() ? JSON.parse(raw) : {};
    } catch {
      setErr("引用没有读成可复算的记录。");
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
      setErr(failureText(data, res.status));
      return;
    }
    setReport(data);
  }

  return (
    <main data-page="attribution">
      <header className="page-head">
        <h1>来源与费用复算</h1>
        <Link className="btn" href="/">返回工作台</Link>
      </header>
      {!tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <div className="card">
        <p>{view.disclaimer}</p>
        <p className="muted">{view.funnelReuse}</p>
        <p className="muted">当前状态：{STATUS_LABELS[view.roiStatus] ?? view.roiStatus}。这不是营销提升证明。</p>
      </div>
      <div className="card">
        <p>
          <label>
            来源、事件与费用引用
            <textarea
              value={raw}
              onChange={(e) => setRaw(e.target.value)}
              rows={8}
              placeholder="写下来源、事件、费用和收入引用"
            />
          </label>
        </p>
        <button className="primary" type="button" data-page-primary="true" disabled={!tenantId} onClick={() => void recalculate()}>
          {pagePrimary("attribution")}
        </button>
        {err ? <SurfaceState kind="error" title="没有复算出来" detail={productError(err)} /> : null}
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
