"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { RecordList, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText, pagePrimary, productError } from "@/lib/productShell";

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
  const width = useShellWidth();
  const [rows, setRows] = useState<Array<LeadRow & { name: string; source: string }> | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async (tenantID: string) => {
    setError("");
    setRows(null);
    if (!tenantID.trim()) {
      setRows([]);
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
        setError(failureText(leadsBody, leadRes.status));
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
      setError(productError((e as Error).message));
      setRows([]);
    }
  }, []);

  useEffect(() => {
    setRows(null);
    if (scope.tenantId) void load(scope.tenantId);
    else setRows([]);
  }, [scope.epoch, scope.tenantId, load]);

  return (
    <main data-page="leads">
      <header className="page-head">
        <h1>线索</h1>
        <Link className="btn primary" data-page-primary="true" href={rows && rows[0] ? `/leads/${rows[0].id}` : "/"}>
          {pagePrimary("leads")}
        </Link>
      </header>
      <p className="muted">来源、负责人和待分配原因以记录为准。这里不创建线索，也不发起触达。</p>
      {!scope.tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {error ? <SurfaceState kind="error" title="线索没有载入" detail={error} /> : null}
      {rows === null ? <SurfaceState kind="loading" title="正在读取线索" detail="姓名、来源和负责人马上就位。" /> : null}
      {rows !== null && rows.length === 0 && !error ? (
        <SurfaceState kind="empty" title="还没有线索" detail="这个工作范围里还没有可跟进的线索。" />
      ) : null}
      {rows && rows.length > 0 ? (
        <RecordList
          width={width}
          rows={rows.map((row) => ({
            id: row.id,
            title: row.name,
            href: `/leads/${row.id}`,
            action: "打开",
            primary: false,
            facts: [
              { label: "来源", value: row.source },
              { label: "状态", value: STATUS_TEXT[row.status] ?? row.status },
              { label: "负责人", value: row.assigned_member_id ? row.assigned_member_id : "待分配（接收时没有指定负责人）" },
            ],
          }))}
        />
      ) : null}
    </main>
  );
}
