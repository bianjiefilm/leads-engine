"use client";

import Link from "next/link";
import { useState } from "react";
import { SurfaceState } from "@/components/workbench/chrome";
import { Button, Input } from "@/vendor/painuo/react/v1/src/index";
import { WHITE_LABEL_CHAIN_UNVERIFIED, brandIsDisplayOnly, exportConfirmHeader, sourceStaysOnTenant } from "@/lib/crmIsolation";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, pagePrimary } from "@/lib/productShell";

// 本租户导出与隔离说明。品牌只展示来源。下载在已有会话上再带确认头，
// 由服务端重新鉴权。真实 Touch 留资链不在这个页面里宣称完成。

export default function IsolationPage() {
  const scope = useCrmScope();
  const [brand, setBrand] = useState("白标品牌");
  const [sourceTag, setSourceTag] = useState("门店活动");
  const [campaignId, setCampaignId] = useState("camp_local");
  const [jobId, setJobId] = useState("");
  const [message, setMessage] = useState("");

  const createJob = async () => {
    setMessage("");
    if (!scope.tenant) {
      setMessage(MISSING_SCOPE);
      return;
    }
    const res = await fetch("/api/crm/exports", scopeInit(scope.tenant, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ purpose: "offboarding", brand_id: brand.trim() }),
    }));
    const body = await res.json();
    if (!res.ok) {
      setMessage(failureText(body, res.status));
      setJobId("");
      return;
    }
    setJobId(typeof body.id === "string" ? body.id : "");
    setMessage("已建立本租户导出任务。下载会再次鉴权，且不含渠道采购价、其他租户或内部令牌。");
  };

  const download = async () => {
    if (!jobId || !scope.tenant) return;
    const res = await fetch(`/api/crm/exports/${jobId}/download`, scopeInit(scope.tenant, {
      headers: exportConfirmHeader(),
    }));
    if (!res.ok) {
      setMessage("下载未通过再次鉴权。");
      return;
    }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `crm-export-${jobId}.txt`;
    anchor.click();
    URL.revokeObjectURL(url);
    setMessage("已下载本租户导出。");
  };

  return (
    <main>
      <h1>本租户 CRM 隔离</h1>
      <p className="muted">
        联系人、线索、商机和跟进的读写都落在当前租户。同一手机号出现在别的租户或品牌时，不跨租户合并，也不绑定平台账号。
      </p>
      <p data-testid="tenant-scope">当前工作范围：{scope.tenant ? "已选择" : "未选择"}</p>
      {!scope.tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <section className="card">
        <h2>品牌来源</h2>
        <Input label="品牌显示名" value={brand} onChange={(e) => setBrand(e.target.value)} />
        <Input label="来源标签" value={sourceTag} onChange={(e) => setSourceTag(e.target.value)} />
        <Input label="活动" value={campaignId} onChange={(e) => setCampaignId(e.target.value)} />
        <p data-testid="brand-role">{brandIsDisplayOnly(brand)}</p>
        <p data-testid="source-stays">{sourceStaysOnTenant(sourceTag, brand, campaignId)}</p>
        <p className="muted">改品牌名称不会改已有联系人的租户、同意来源和商机历史。</p>
      </section>
      <section className="card">
        <h2>本租户导出</h2>
        <p className="muted">租户负责人可导出联系人、线索、商机、跟进、分配、来源、同意和撤回状态。</p>
        <Button variant="primary" size="sm" type="button" data-page-primary="true" onClick={createJob}>{pagePrimary("isolation")}</Button>{" "}
        <Button variant="secondary" size="sm" type="button" onClick={download} disabled={!jobId}>确认并下载</Button>
        {jobId ? <p data-testid="export-job">导出任务已建立</p> : null}
        {message ? <p data-testid="export-message">{message}</p> : null}
      </section>
      <p className="muted" data-testid="pause-quota">
        租户暂停和品牌暂停分开。恢复不会自动恢复已撤销的成员、营销同意或旧授权。额度不足只停止新增收费 AI，不隐藏已有联系人和跟进。
      </p>
      <p className="muted" data-testid="touch-unverified">
        {WHITE_LABEL_CHAIN_UNVERIFIED}
      </p>
      <p className="muted">
        <Link href="/">返回工作台</Link> · <Link href="/contacts">客户档案</Link>
      </p>
    </main>
  );
}
