"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { RecordList, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { DetailDrawer } from "@/components/workbench/detailDrawer";
import { SegmentedControl } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { leadRowFacts } from "@/lib/finish";
import { deskState, sourceText, type StatusFacts } from "@/lib/workbench";
import { MISSING_SCOPE, acceptLeadRows, failureText, listTenantHeader, pagePrimary, productError } from "@/lib/productShell";

// 原生线索页（HUI-1680）。只打本站 BFF。租户由工作区选择，服务端再校验成员身份。
// 未分配的线索明确写「待分配」，不把空负责人伪装成已分派。
// HUI-2626 finish-r1（票面 A/B 组）：状态筛选 + 抽屉详情统一模式。
// 服务端 /api/v1/leads 无查询参数，返回当前范围全量（owner 全量、销售只看名下），
// 状态筛选是对这份全量结果的客户端过滤，文案如实标注，不冒充服务端行为。

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

interface TimelineEvent {
  at: string;
  kind: string;
  summary: string;
}

interface DrawerTimeline {
  source?: { form?: string; activity?: string; channel?: string };
  statuses?: StatusFacts;
  next?: { label?: string; at?: string };
  events?: TimelineEvent[];
  owner_label?: string;
  assignment_reason?: string;
  message?: string;
  error?: string;
}

const STATUS_FILTERS = [
  { value: "all", label: "全部" },
  { value: "new", label: "新线索" },
  { value: "in_progress", label: "跟进中" },
  { value: "converted", label: "已转化" },
  { value: "closed", label: "已关闭" },
  { value: "filtered", label: "已过滤" },
];

const EVENT_KIND: Record<string, string> = {
  source: "来源",
  assignment: "分配",
  follow_up: "跟进",
  opportunity: "商机",
  consent: "授权",
  system: "系统",
};

export default function LeadsPage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const [rows, setRows] = useState<Array<LeadRow & { name: string; source: string }> | null>(null);
  const [error, setError] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const gen = useRef({ seq: 0, tenant: "" });

  // 详情抽屉（真实 timeline + 票号：切范围/迟到响应一律丢弃）。
  const [drawerId, setDrawerId] = useState<string | null>(null);
  const [drawerTitle, setDrawerTitle] = useState("");
  const [drawerBody, setDrawerBody] = useState<DrawerTimeline | null>(null);
  const [drawerBusy, setDrawerBusy] = useState(false);
  const [drawerErr, setDrawerErr] = useState("");
  const drawerGen = useRef({ seq: 0, tenant: "" });

  useEffect(() => {
    const tenant = scope.tenant ?? "";
    const ticket = { seq: gen.current.seq + 1, tenant: tenant };
    gen.current = ticket;
    setError("");
    setStatusFilter("all");
    setDrawerId(null);
    if (!tenant) {
      setRows([]);
      return;
    }
    setRows(null);
    let cancelled = false;
    void (async () => {
      const headers = listTenantHeader(tenant);
      if (!headers) {
        if (!cancelled && acceptLeadRows(gen.current, ticket, []) !== null) setRows([]);
        return;
      }
      try {
        const [leadRes, contactRes] = await Promise.all([
          fetch("/api/leads", { headers }),
          fetch("/api/contacts", { headers }),
        ]);
        const leadsBody = await leadRes.json();
        const contactsBody = await contactRes.json();
        if (!leadRes.ok) {
          if (cancelled || acceptLeadRows(gen.current, ticket, []) === null) return;
          setError(failureText(leadsBody, leadRes.status));
          setRows([]);
          return;
        }
        const contacts = new Map<string, ContactRow>();
        if (contactRes.ok) {
          for (const item of (contactsBody.items ?? []) as ContactRow[]) contacts.set(item.id, item);
        }
        const items = ((leadsBody.items ?? []) as LeadRow[]).map((lead) => {
          const contact = contacts.get(lead.contact_id);
          return {
            ...lead,
            name: contact?.name || "未命名",
            source: contact?.source_type ?? "",
          };
        });
        const accepted = acceptLeadRows(gen.current, ticket, items);
        if (cancelled || accepted === null) return;
        setRows(accepted);
      } catch (e) {
        if (cancelled || acceptLeadRows(gen.current, ticket, []) === null) return;
        setError(productError((e as Error).message));
        setRows([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenant]);

  const openDrawer = (id: string) => {
    const tenant = scope.tenant ?? "";
    const ticket = { seq: drawerGen.current.seq + 1, tenant };
    drawerGen.current = ticket;
    setDrawerId(id);
    setDrawerTitle(rows?.find((r) => r.id === id)?.name || "线索详情");
    setDrawerBody(null);
    setDrawerErr("");
    setDrawerBusy(true);
    void (async () => {
      try {
        const res = await fetch(`/api/leads/${id}/timeline`, { headers: listTenantHeader(tenant) ?? {} });
        const body = (await res.json()) as DrawerTimeline;
        if (drawerGen.current !== ticket || scope.tenant !== ticket.tenant) return;
        if (!res.ok) {
          setDrawerErr(failureText(body, res.status));
          return;
        }
        setDrawerBody(body);
      } catch (e) {
        if (drawerGen.current === ticket) setDrawerErr(productError((e as Error).message));
      } finally {
        if (drawerGen.current === ticket) setDrawerBusy(false);
      }
    })();
  };

  const closeDrawer = () => {
    drawerGen.current = { seq: drawerGen.current.seq + 1, tenant: scope.tenant ?? "" };
    setDrawerId(null);
    setDrawerBody(null);
    setDrawerErr("");
  };

  const visibleRows = (rows ?? []).filter((row) => statusFilter === "all" || row.status === statusFilter);
  const firstFollowable = (rows ?? []).find((row) => row.status === "new" || row.status === "in_progress");

  return (
    <main data-page="leads">
      <header className="page-head">
        <h1>线索</h1>
        <Link className="btn primary" data-page-primary="true" href={firstFollowable ? `/leads/${firstFollowable.id}` : "/"}>
          {pagePrimary("leads")}
        </Link>
      </header>
      <p className="muted">来源、负责人和待分配原因以记录为准。这里不创建线索，也不发起触达。</p>
      {!scope.tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {error ? <SurfaceState kind="error" title="线索没有载入" detail={error} /> : null}
      {rows === null && !error ? <SurfaceState kind="loading" title="正在读取线索" detail="姓名、来源和负责人马上就位。" /> : null}
      {rows !== null && rows.length === 0 && !error ? (
        <SurfaceState kind="empty" title="还没有线索" detail="这个工作范围里还没有可跟进的线索。" />
      ) : null}
      {rows !== null && rows.length > 0 ? (
        <>
          <div className="card" data-list-filter="leads">
            <SegmentedControl
              label="状态筛选（当前范围全部线索）"
              items={STATUS_FILTERS}
              value={statusFilter}
              onValueChange={(next) => setStatusFilter(next ?? "all")}
            />
            <p className="muted">
              筛选在本范围返回的全部线索上进行，共 {rows.length} 条，当前显示 {visibleRows.length} 条。
            </p>
          </div>
          {visibleRows.length === 0 ? (
            <SurfaceState kind="empty" title="这个状态没有线索" detail="换一个状态筛选再看。" />
          ) : (
            <RecordList
              width={width}
              rows={visibleRows.map((row) => ({
                id: row.id,
                title: row.name,
                action: "查看详情",
                onOpen: () => openDrawer(row.id),
                facts: leadRowFacts(row),
              }))}
            />
          )}
        </>
      ) : null}

      <DetailDrawer
        open={Boolean(drawerId)}
        onClose={closeDrawer}
        title={drawerTitle}
        busy={drawerBusy}
        width={width}
        footer={drawerId ? <Link className="btn" href={`/leads/${drawerId}`}>打开完整线索</Link> : null}
      >
        {drawerErr ? <SurfaceState kind="error" title="线索没有打开" detail={drawerErr} /> : null}
        {!drawerErr && !drawerBody && drawerBusy ? (
          <SurfaceState kind="loading" title="正在打开线索" detail="来源、负责人和下一步马上就位。" />
        ) : null}
        {drawerBody ? (
          <>
            <ul data-facts="chips">
              <li>
                <span>来源</span>
                <strong>{sourceText(drawerBody.source)}</strong>
              </li>
              <li>
                <span>状态</span>
                <strong>{deskState(drawerBody.statuses, undefined) || "未记录"}</strong>
              </li>
              <li>
                <span>负责人</span>
                <strong>{drawerBody.owner_label || "待分配"}</strong>
              </li>
              <li>
                <span>下一步</span>
                <strong>{drawerBody.next?.label || "还没有安排"}</strong>
              </li>
            </ul>
            <h3>最近动态</h3>
            {(drawerBody.events ?? []).length === 0 ? (
              <p className="muted">还没有记录。跟进步骤在完整线索页进行。</p>
            ) : (
              <ol className="desk-list">
                {(drawerBody.events ?? []).slice(0, 5).map((event, index) => (
                  <li key={`${event.kind}-${event.at}-${index}`}>
                    <p>{EVENT_KIND[event.kind] ?? event.kind} · {event.summary}</p>
                    <p className="muted">{event.at}</p>
                  </li>
                ))}
              </ol>
            )}
            <p className="muted">
              当前状态:{deskState(drawerBody.statuses, undefined)}。跟进与安排下一步在完整线索页操作。
            </p>
          </>
        ) : null}
      </DetailDrawer>
    </main>
  );
}
