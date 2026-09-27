"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { commitCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";

// 原生线索页（HUI-1680）。只打本站 BFF。租户由工作区选择，服务端再校验成员身份。
// 未分配的线索明确写「待分配」，不把空负责人伪装成已分派。

interface LeadRow {
  id: string;
  contact_id: string;
  status: string;
  assigned_member_id?: string;
  created_by?: string;
}

interface ContactRow {
  id: string;
  name: string;
  source_type: string;
  phone?: string;
}

const SOURCE_TEXT: Record<string, string> = {
  manual: "手工录入",
  form: "自有表单",
  touch_campaign: "碰一碰",
};

const STATUS_TEXT: Record<string, string> = {
  new: "新线索",
  in_progress: "跟进中",
  converted: "已转化",
  closed: "已关闭",
  filtered: "已过滤",
};

export default function LeadsPage() {
  const scope = useCrmScope();
  const [tenant, setTenant] = useState("");
  const [rows, setRows] = useState<Array<LeadRow & { name: string; source: string }> | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async (tenantID: string) => {
    setError("");
    setRows(null);
    if (!tenantID.trim()) {
      setRows([]);
      setError("先填写当前租户");
      return;
    }
    const headers = { "x-tenant-id": tenantID.trim() };
    try {
      const [leadRes, contactRes] = await Promise.all([
        fetch("/api/leads", { headers }),
        fetch("/api/contacts", { headers }),
      ]);
      const leadsBody = await leadRes.json();
      const contactsBody = await contactRes.json();
      if (!leadRes.ok) {
        setError(leadsBody.message ?? leadsBody.error ?? `HTTP ${leadRes.status}`);
        setRows([]);
        return;
      }
      const contacts = new Map<string, ContactRow>();
      if (contactRes.ok) {
        for (const item of (contactsBody.items ?? []) as ContactRow[]) {
          contacts.set(item.id, item);
        }
      }
      const items = ((leadsBody.items ?? []) as LeadRow[]).map((lead) => {
        const contact = contacts.get(lead.contact_id);
        return {
          ...lead,
          name: contact?.name || "未命名",
          source: SOURCE_TEXT[contact?.source_type ?? ""] ?? contact?.source_type ?? "未知来源",
        };
      });
      setRows(items);
    } catch (e) {
      setError((e as Error).message);
      setRows([]);
    }
  }, []);

  useEffect(() => {
    setRows(null);
    setTenant(scope.tenantId ?? "");
    if (scope.tenantId) load(scope.tenantId);
    else {
      setRows([]);
      setError("先填写当前租户");
    }
  }, [scope.epoch, scope.tenantId, load]);

  const saveTenant = () => {
    const value = tenant.trim();
    if (!value) {
      setError("先填写当前租户");
      setRows([]);
      return;
    }
    if (value === scope.tenantId) load(value);
    else commitCrmTenant(value);
  };

  return (
    <main>
      <p>
        <Link href="/">返回工作台</Link>
      </p>
      <h1>线索</h1>
      <div className="card">
        <label>
          当前租户{" "}
          <input value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="租户 id" />
        </label>{" "}
        <button type="button" onClick={saveTenant}>
          加载
        </button>
        <p className="muted">来源、负责人和待分配原因以服务端记录为准。这里不创建线索，也不发起触达。</p>
      </div>
      {error ? <p className="muted">{error}</p> : null}
      <div className="card">
        {rows === null ? (
          <p className="muted">加载中…</p>
        ) : rows.length === 0 ? (
          <p className="muted">这个租户还没有线索。</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>姓名</th>
                <th>来源</th>
                <th>状态</th>
                <th>负责人</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.id}>
                  <td>{row.name}</td>
                  <td>{row.source}</td>
                  <td>{STATUS_TEXT[row.status] ?? row.status}</td>
                  <td>{row.assigned_member_id ? row.assigned_member_id : "待分配（接收时没有指定负责人）"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </main>
  );
}
