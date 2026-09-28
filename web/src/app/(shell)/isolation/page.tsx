"use client";

import Link from "next/link";
import { useState } from "react";
import { brandIsDisplayOnly, exportConfirmHeader } from "@/lib/crmIsolation";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";

// 本租户导出与隔离说明。品牌只展示来源。下载在已有会话上再带确认头，
// 由服务端重新鉴权。真实 Touch 留资链不在这个页面里宣称完成。

export default function IsolationPage() {
  const scope = useCrmScope();
  const [brand, setBrand] = useState("白标品牌");
  const [jobId, setJobId] = useState("");
  const [message, setMessage] = useState("");
  const [payload, setPayload] = useState("");

  const createJob = async () => {
    setMessage("");
    setPayload("");
    if (!scope.tenantId) {
      setMessage("先选择本租户，再申请导出。");
      return;
    }
    const res = await fetch("/api/crm/exports", scopeInit(scope.tenantId, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ purpose: "offboarding", brand_id: brand.trim() }),
    }));
    const body = await res.json();
    if (!res.ok) {
      setMessage(body.message ?? body.error ?? "导出申请失败");
      setJobId("");
      return;
    }
    setJobId(typeof body.id === "string" ? body.id : "");
    setMessage("已建立本租户导出任务。下载会再次鉴权，且不含渠道采购价、其他租户或内部令牌。");
  };

  const download = async () => {
    if (!jobId || !scope.tenantId) return;
    const res = await fetch(`/api/crm/exports/${jobId}/download`, scopeInit(scope.tenantId, {
      headers: exportConfirmHeader(),
    }));
    const text = await res.text();
    setPayload(text);
    setMessage(res.ok ? "已下载本租户导出。" : "下载未通过再次鉴权。");
  };

  return (
    <main>
      <h1>本租户 CRM 隔离</h1>
      <p className="muted">
        联系人、线索、商机和跟进的读写都落在当前租户。同一手机号出现在别的租户或品牌时，不跨租户合并，也不绑定平台账号。
      </p>
      <p data-testid="tenant-scope">当前租户：{scope.tenantId || "未选择"}</p>
      <section className="card">
        <h2>品牌来源</h2>
        <input
          aria-label="品牌显示名"
          value={brand}
          onChange={(e) => setBrand(e.target.value)}
        />
        <p data-testid="brand-role">{brandIsDisplayOnly(brand)}</p>
        <p className="muted">改品牌名称不会改已有联系人的租户、同意来源和商机历史。</p>
      </section>
      <section className="card">
        <h2>本租户导出</h2>
        <p className="muted">租户负责人可导出联系人、线索、商机、跟进、分配、来源、同意和撤回状态。</p>
        <button type="button" onClick={createJob}>申请导出</button>{" "}
        <button type="button" onClick={download} disabled={!jobId}>确认并下载</button>
        {jobId ? <p data-testid="export-job">任务 {jobId}</p> : null}
        {message ? <p data-testid="export-message">{message}</p> : null}
        {payload ? <pre data-testid="export-payload">{payload}</pre> : null}
      </section>
      <p className="muted" data-testid="touch-unverified">
        Touch 到 Leads 的真实留资链仍未验证。这里没有用模拟接收器宣称白标经营链已完成。
      </p>
      <p className="muted">
        <Link href="/">返回工作台</Link> · <Link href="/contacts">客户档案</Link>
      </p>
    </main>
  );
}
