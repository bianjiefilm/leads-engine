"use client";

import Link from "next/link";
import { useState } from "react";
import { RendererProvider, Status } from "@/vendor/painuo/react/v1/src/index";
import { SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { Button } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, listTenantHeader, pagePrimary, productError, rendererDensity } from "@/lib/productShell";
import { roiStatusToState } from "@/lib/painuoStatus";
import { centsText, presentRoi, STATUS_LABELS, type RoiReport } from "@/lib/roi";

// HUI-1696 来源链与费用复算页。只打本站 BFF。页面不保存第二份线索或费用。

export default function AttributionPage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const tenant = scope.tenant ?? "";
  const [raw, setRaw] = useState("");
  const [report, setReport] = useState<RoiReport | null>(null);
  const [err, setErr] = useState("");
  const view = presentRoi(report);

  async function recalculate() {
    if (!tenant) {
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
      headers: { "content-type": "application/json", ...listTenantHeader(tenant) },
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
    // HUI-2622：页级 renderer scope（profile-scopes.json leads-web 合法组合）；provider 自带 pn-r-scope data 属性，样式子树自洽零外溢
    // HUI-2626：密度与壳级同规——≤430 touch（48px 控件），桌面 default。
    <RendererProvider profile="leads-web" surface="work.light" theme="light" density={rendererDensity(width)}>
    <main data-page="attribution">
      <header className="page-head">
        <h1>来源与费用复算</h1>
        <Link className="btn" href="/">返回工作台</Link>
      </header>
      {!tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <div className="card">
        <p>{view.disclaimer}</p>
        <p className="muted">{view.funnelReuse}</p>
        <p className="muted">
          当前状态：{STATUS_LABELS[view.roiStatus] ?? view.roiStatus}。这不是营销提升证明。
          {/* HUI-2622：painuo renderer Status 徽章，加法式追加；STATUS_LABELS 文字原样保留，颜色不单独立义 */}
          <Status
            state={roiStatusToState(view.roiStatus)}
            label={(STATUS_LABELS[view.roiStatus] ?? view.roiStatus).trim() || "未知"}
          />
        </p>
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
        <Button variant="primary" size="sm" type="button" data-page-primary="true" disabled={!tenant} onClick={() => void recalculate()}>
          {pagePrimary("attribution")}
        </Button>
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
    </RendererProvider>
  );
}
