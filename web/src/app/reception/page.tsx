"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { RecordFrame, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, factTone, failureText, pagePrimary, productError } from "@/lib/productShell";
import { MODE_TEXT, PENDING_TEXT } from "@/lib/reception";
import SessionPanel from "./session-panel";

// 接待工作台（HUI-1688 → HUI-1893 可读）。只展示负责人、待处理原因、人工待办和下一次跟进。

interface DeskItem {
  session_id: string;
  mode: string;
  epoch: number;
  owner_member_id?: string;
  pending_reason?: string;
  human_todo: boolean;
  next_follow_up_at?: string;
  version: number;
}

const ROLE_TEXT: Record<string, string> = {
  owner: "店主",
  sales: "销售",
  agent: "客服",
};

export default function ReceptionDeskPage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const [role, setRole] = useState("");
  const [items, setItems] = useState<DeskItem[] | null>(null);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState("");

  const load = useCallback(async (tenantID: string) => {
    setError("");
    if (!tenantID.trim()) {
      setItems([]);
      return;
    }
    try {
      const headers = { "x-tenant-id": tenantID.trim() };
      const who = await fetch("/api/whoami", { headers });
      const whoBody = await who.json().catch(() => ({}));
      setRole(who.ok && typeof whoBody.role === "string" ? whoBody.role : "");
      const res = await fetch("/api/reception/desk", { headers });
      const body = await res.json();
      if (!res.ok) {
        setError(failureText(body, res.status));
        setItems([]);
        return;
      }
      setItems(body.items ?? []);
    } catch (e) {
      setError(productError((e as Error).message));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    setSelected("");
    setItems(null);
    if (scope.tenantId) void load(scope.tenantId);
    else setItems([]);
  }, [scope.epoch, scope.tenantId, load]);

  return (
    <main data-page="reception">
      <header className="page-head">
        <h1>接待工作台</h1>
        <Link className="btn" href="/">返回工作台</Link>
      </header>
      <p className="muted">
        <span data-tone="human">人工事实</span> 当前身份：{ROLE_TEXT[role] ?? "未确认"}。当前负责人、待处理原因和人工待办。
      </p>
      <p data-tone="ai">模型不可用时，人工仍可在会话里继续回复。这里不会自动外呼。</p>
      {!scope.tenantId ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {error ? <SurfaceState kind="error" title="接待没有载入" detail={error} /> : null}
      {scope.tenantId && items === null && !error ? <SurfaceState kind="loading" title="正在读取接待" detail="进行中的会话马上就位。" /> : null}
      {items && items.length === 0 && !error ? <SurfaceState kind="empty" title="没有进行中的接待" detail="新的咨询进来后会出现在这里。" /> : null}
      <div className="record-list">
        {(items ?? []).map((item, index) => {
          const open = () => setSelected(item.session_id);
          const primary = index === 0;
          return (
            <RecordFrame
              key={item.session_id}
              width={width}
              tone={item.human_todo ? "human" : factTone({ kind: item.mode === "ai" ? "ai_draft" : "human" })}
              title={selected === item.session_id ? "处理中" : (MODE_TEXT[item.mode] ?? "接待")}
              facts={[
                { label: "会话", value: item.session_id },
                { label: "模式", value: MODE_TEXT[item.mode] ?? item.mode },
                { label: "负责人", value: item.owner_member_id || "尚未接管" },
                { label: "待处理", value: item.pending_reason ? (PENDING_TEXT[item.pending_reason] ?? item.pending_reason) : "无" },
                { label: "人工待办", value: item.human_todo ? "是" : "否" },
                { label: "下一次跟进", value: item.next_follow_up_at || "未安排" },
              ]}
              primary={primary ? (
                <button className="primary" type="button" data-page-primary="true" onClick={open}>
                  {pagePrimary("reception")}
                </button>
              ) : undefined}
              secondary={primary ? undefined : (
                <button className="btn" type="button" onClick={open}>
                  {selected === item.session_id ? "处理中" : "处理"}
                </button>
              )}
            />
          );
        })}
      </div>
      {selected && scope.tenantId ? (
        <SessionPanel tenant={scope.tenantId} sessionId={selected} onChanged={() => { void load(scope.tenantId ?? ""); }} />
      ) : null}
    </main>
  );
}
