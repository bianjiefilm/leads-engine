"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";

// HUI-1678 企业资料筛选。只打本站 BFF。
// 没有官方公开库。导入的是客户声明有权再利用的资料。
// 确认前只预览；确认后进入本租户候选池，不授予营销同意，也不发起触达。
// 销售工作台（HUI-1893）、活的碰一碰活动和广告渠道不在本页。

const TENANT_KEY = "leads_tenant_id";

interface Capability {
  official_directory?: string;
  reason?: string;
  accepted_source?: string;
  marketing_implied?: boolean;
  outbound_linked?: boolean;
  error?: string;
  message?: string;
}

interface Person {
  name?: string;
  phone?: string;
  email?: string;
}

interface EnterpriseRow {
  id: string;
  enterprise_id: string;
  enterprise_name: string;
  industry: string;
  region: string;
  scale: string;
  person?: Person;
  missing_fields?: string[];
  allowed_uses?: string[];
  marketing_consent?: boolean;
  freshness?: string;
  conflict?: boolean;
  status?: string;
  source_name?: string;
  collected_at?: string;
  field_sources?: Record<string, { value?: string; source?: string; collected_at?: string; missing?: boolean }>;
}

interface Preview {
  will_enter_pool?: boolean;
  marketing_consent?: boolean;
  outbound?: string;
  freshness?: string;
  missing_fields?: string[];
  allowed_uses?: string[];
  field_sources?: EnterpriseRow["field_sources"];
  record?: EnterpriseRow;
  message?: string;
  error?: string;
}

async function readJSON(res: Response): Promise<Record<string, unknown>> {
  return res.json();
}

export default function EnterprisesPage() {
  const [tenant, setTenant] = useState("");
  const [capability, setCapability] = useState<Capability | null>(null);
  const [rows, setRows] = useState<EnterpriseRow[] | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [confirmText, setConfirmText] = useState("");
  const [error, setError] = useState("");
  const [sourceKey, setSourceKey] = useState("cust-export-1");
  const [sourceName, setSourceName] = useState("");
  const [collectedAt, setCollectedAt] = useState("");
  const [cycle, setCycle] = useState("30");
  const [license, setLicense] = useState("");
  const [correction, setCorrection] = useState("客户可在本页删除；同一来源再次导入这家企业不得恢复营销");
  const [enterpriseID, setEnterpriseID] = useState("");
  const [enterpriseName, setEnterpriseName] = useState("");
  const [industry, setIndustry] = useState("");
  const [region, setRegion] = useState("");
  const [scale, setScale] = useState("");
  const [personName, setPersonName] = useState("");
  const [personPhone, setPersonPhone] = useState("");
  const [filterIndustry, setFilterIndustry] = useState("");
  const [filterRegion, setFilterRegion] = useState("");
  const [filterScale, setFilterScale] = useState("");

  const headers = useCallback((): HeadersInit => ({ "x-tenant-id": tenant.trim(), "content-type": "application/json" }), [tenant]);

  const load = useCallback(async (tenantID: string, filter?: { industry: string; region: string; scale: string }) => {
    setError("");
    setConfirmText("");
    if (!tenantID.trim()) {
      setError("先填写当前租户");
      setCapability(null);
      setRows([]);
      return;
    }
    const h = { "x-tenant-id": tenantID.trim() };
    const capRes = await fetch("/api/enterprise-directory/capability", { headers: h });
    const cap = (await readJSON(capRes)) as Capability;
    if (!capRes.ok) {
      setCapability(null);
      setRows([]);
      setError(cap.message ?? cap.error ?? `HTTP ${capRes.status}`);
      return;
    }
    setCapability(cap);
    const params = new URLSearchParams();
    const f = filter ?? { industry: filterIndustry, region: filterRegion, scale: filterScale };
    if (f.industry.trim()) params.set("industry", f.industry.trim());
    if (f.region.trim()) params.set("region", f.region.trim());
    if (f.scale.trim()) params.set("scale", f.scale.trim());
    const q = params.toString();
    const listRes = await fetch("/api/enterprise-directory/records" + (q ? `?${q}` : ""), { headers: h });
    const list = await readJSON(listRes);
    if (!listRes.ok) {
      setError(String(list.message ?? list.error ?? `HTTP ${listRes.status}`));
      setRows([]);
      return;
    }
    setRows((list.items as EnterpriseRow[]) ?? []);
  }, [filterIndustry, filterRegion, filterScale]);

  useEffect(() => {
    const saved = window.localStorage.getItem(TENANT_KEY) ?? "";
    if (saved) {
      setTenant(saved);
    }
  }, []);

  const saveTenant = () => {
    const value = tenant.trim();
    window.localStorage.setItem(TENANT_KEY, value);
    void load(value);
  };

  const importBatch = async () => {
    setError("");
    setPreview(null);
    const res = await fetch("/api/enterprise-directory/imports", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify({
        source_key: sourceKey.trim(),
        source_name: sourceName.trim(),
        collected_at: collectedAt.trim(),
        update_cycle_days: Number(cycle),
        license: license.trim(),
        correction: correction.trim(),
        records: [
          {
            enterprise_id: enterpriseID.trim(),
            enterprise_name: enterpriseName.trim(),
            industry: industry.trim(),
            region: region.trim(),
            scale: scale.trim(),
            person_name: personName.trim(),
            person_phone: personPhone.trim(),
            person_email: "",
          },
        ],
      }),
    });
    const body = await readJSON(res);
    if (!res.ok) {
      setError(String(body.message ?? body.error ?? `HTTP ${res.status}`));
      return;
    }
    await load(tenant);
  };

  const openPreview = async (id: string) => {
    setError("");
    setConfirmText("");
    const res = await fetch(`/api/enterprise-directory/records/${id}/preview`, { headers: { "x-tenant-id": tenant.trim() } });
    const body = (await readJSON(res)) as Preview;
    if (!res.ok) {
      setPreview(null);
      setError(body.message ?? body.error ?? `HTTP ${res.status}`);
      return;
    }
    setPreview(body);
  };

  const act = async (id: string, action: "confirm" | "refuse" | "delete") => {
    setError("");
    const res = await fetch(`/api/enterprise-directory/records/${id}/${action}`, {
      method: "POST",
      headers: headers(),
    });
    const body = await readJSON(res);
    if (!res.ok) {
      setConfirmText(String(body.message ?? body.error ?? `HTTP ${res.status}`));
      return;
    }
    if (action === "confirm") {
      setConfirmText(
        `已进入本租户候选池。线索 ${String(body.lead_id ?? "")}。营销同意：否。外呼/短信/SOP：无。来源 ${String(body.source_app ?? "")}。`,
      );
    } else {
      setConfirmText(action === "refuse" ? "已拒绝。同一来源再次导入这家企业不能恢复营销。" : "已删除。同一来源再次导入这家企业不能恢复营销。");
    }
    setPreview(null);
    await load(tenant);
  };

  return (
    <main>
      <p>
        <Link href="/">返回工作台</Link>
      </p>
      <h1>企业资料筛选</h1>
      <div className="card" id="capability">
        <h2>数据源</h2>
        {capability ? (
          <p>
            官方企业公开库：{capability.official_directory}。{capability.reason} 可用来源：
            {capability.accepted_source}。营销不会因公开资料自动成立，也不会联动外呼、短信或 SOP。
          </p>
        ) : (
          <p className="muted">填写租户后加载。未开启时这里不会假装已经接上公开库。</p>
        )}
        <label>
          当前租户{" "}
          <input id="tenant-input" value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="租户 id" />
        </label>{" "}
        <button id="load-tenant" type="button" onClick={saveTenant}>
          加载
        </button>
      </div>

      <div className="card">
        <h2>导入客户有权再利用的企业资料</h2>
        <p className="muted">企业行业、地区、规模和自然人联系方式分开填写。电话不代表可以营销。</p>
        <p>
          <label>
            来源键 <input id="source-key" value={sourceKey} onChange={(e) => setSourceKey(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            来源名称 <input id="source-name" value={sourceName} onChange={(e) => setSourceName(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            采集时间 <input id="collected-at" value={collectedAt} onChange={(e) => setCollectedAt(e.target.value)} placeholder="2026-09-01T00:00:00Z" />
          </label>
        </p>
        <p>
          <label>
            更新周期（天） <input id="update-cycle" value={cycle} onChange={(e) => setCycle(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            再利用许可 <input id="license" value={license} onChange={(e) => setLicense(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            删除/更正 <input id="correction" value={correction} onChange={(e) => setCorrection(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            企业标识 <input id="enterprise-id" value={enterpriseID} onChange={(e) => setEnterpriseID(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            企业名称 <input id="enterprise-name" value={enterpriseName} onChange={(e) => setEnterpriseName(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            行业 <input id="industry" value={industry} onChange={(e) => setIndustry(e.target.value)} />
          </label>{" "}
          <label>
            地区 <input id="region" value={region} onChange={(e) => setRegion(e.target.value)} />
          </label>{" "}
          <label>
            规模 <input id="scale" value={scale} onChange={(e) => setScale(e.target.value)} />
          </label>
        </p>
        <p>
          <label>
            联系人姓名（可选，不是营销同意）{" "}
            <input id="person-name" value={personName} onChange={(e) => setPersonName(e.target.value)} />
          </label>{" "}
          <label>
            联系人电话（可选） <input id="person-phone" value={personPhone} onChange={(e) => setPersonPhone(e.target.value)} />
          </label>
        </p>
        <button id="import-submit" type="button" onClick={() => void importBatch()}>
          导入到本租户
        </button>
      </div>

      <div className="card">
        <h2>按企业筛选</h2>
        <label>
          行业 <input id="filter-industry" value={filterIndustry} onChange={(e) => setFilterIndustry(e.target.value)} />
        </label>{" "}
        <label>
          地区 <input id="filter-region" value={filterRegion} onChange={(e) => setFilterRegion(e.target.value)} />
        </label>{" "}
        <label>
          规模 <input id="filter-scale" value={filterScale} onChange={(e) => setFilterScale(e.target.value)} />
        </label>{" "}
        <button id="filter-submit" type="button" onClick={() => void load(tenant)}>
          筛选
        </button>
        {error ? <p className="muted">{error}</p> : null}
        <table>
          <thead>
            <tr>
              <th>企业</th>
              <th>行业/地区/规模</th>
              <th>新鲜度</th>
              <th>缺失</th>
              <th>状态</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {(rows ?? []).map((row) => (
              <tr key={row.id}>
                <td>
                  {row.enterprise_name}
                  <div className="muted">{row.enterprise_id}</div>
                </td>
                <td>
                  {row.industry || "行业缺失"} / {row.region || "地区缺失"} / {row.scale || "规模缺失"}
                  {row.conflict ? <div>资料冲突</div> : null}
                </td>
                <td>{row.freshness === "expired" ? "已过期" : "未过期"}</td>
                <td>{(row.missing_fields ?? []).join("、") || "无"}</td>
                <td>{row.conflict && row.status === "confirmed" ? "池内仍是确认时的旧内容" : row.status}</td>
                <td>
                  <button type="button" onClick={() => void openPreview(row.id)}>
                    预览
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {preview ? (
        <div className="card" id="preview">
          <h2>导入前预览</h2>
          <p>这一步不会进入候选池。允许用途：{(preview.allowed_uses ?? []).join("、") || "无"}。营销同意：否。外呼：{preview.outbound}。</p>
          <p>
            新鲜度：{preview.freshness === "expired" ? "已过期" : "未过期"}。缺失：
            {(preview.missing_fields ?? []).join("、") || "无"}。
          </p>
          <ul>
            {Object.entries(preview.field_sources ?? {}).map(([key, fact]) => (
              <li key={key}>
                {key}：{fact.missing ? "缺失" : fact.value}（来源 {fact.source}，采集 {fact.collected_at}）
              </li>
            ))}
          </ul>
          {preview.record?.conflict ? (
            <p>资料冲突：目录里是新事实，候选池里仍是确认时的旧内容，不能静默覆盖。</p>
          ) : null}
          <p className="muted">自然人联系方式只作旁注，不构成营销许可。拒绝或删除只挡住同一来源的再次导入。</p>
          {preview.record ? (
            <p>
              <button id="confirm-submit" type="button" onClick={() => void act(preview.record!.id, "confirm")}>
                确认进入本租户候选池
              </button>{" "}
              <button id="refuse-submit" type="button" onClick={() => void act(preview.record!.id, "refuse")}>
                拒绝
              </button>{" "}
              <button id="delete-submit" type="button" onClick={() => void act(preview.record!.id, "delete")}>
                删除
              </button>
            </p>
          ) : null}
        </div>
      ) : null}
      {confirmText ? <p id="confirm-result">{confirmText}</p> : null}
    </main>
  );
}
