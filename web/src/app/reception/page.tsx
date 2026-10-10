"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { RecordFrame, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { Button } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, acceptDeskPayload, factTone, failureText, listTenantHeader, pagePrimary, productError, receptionSessionFrame } from "@/lib/productShell";
import { MODE_TEXT, PENDING_TEXT } from "@/lib/reception";
import { searchToken, todayReturnHref } from "@/lib/workbench";
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
  const [selectedTenant, setSelectedTenant] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const [returnFocus, setReturnFocus] = useState("");
  const [wantedSession, setWantedSession] = useState("");
  const [seenTenant, setSeenTenant] = useState(scope.tenant);
  const gen = useRef({ seq: 0, tenant: "" });

  if (seenTenant !== scope.tenant) {
    setSeenTenant(scope.tenant);
    setSelected("");
    setSelectedTenant(null);
    setItems(null);
    setError("");
    setRole("");
  }

  useEffect(() => {
    const query = new URLSearchParams(window.location.search);
    setReturnFocus(searchToken(query.get("focus")));
    setWantedSession(searchToken(query.get("session")));
  }, []);

  useEffect(() => {
    if (!wantedSession || !items || !scope.tenant) return;
    if (!items.some((item) => item.session_id === wantedSession)) return;
    setSelected(wantedSession);
    setSelectedTenant(scope.tenant);
  }, [wantedSession, items, scope.tenant]);

  useEffect(() => {
    const tenant = scope.tenant ?? "";
    const seq = gen.current.seq + 1;
    gen.current = { seq, tenant: tenant };
    if (!tenant) {
      setItems([]);
      return;
    }
    let cancelled = false;
    void (async () => {
      const headers = listTenantHeader(tenant);
      if (!headers) {
        if (!cancelled) setItems([]);
        return;
      }
      try {
        const who = await fetch("/api/whoami", { headers });
        const whoBody = await who.json().catch(() => ({}));
        const res = await fetch("/api/reception/desk", { headers });
        const body = await res.json();
        const roleText = who.ok && typeof whoBody.role === "string" ? whoBody.role : "";
        if (!res.ok) {
          const accepted = acceptDeskPayload(gen.current, seq, tenant, [] as DeskItem[]);
          if (cancelled || accepted === null) return;
          setRole(roleText);
          setError(failureText(body, res.status));
          setItems([]);
          return;
        }
        const accepted = acceptDeskPayload(gen.current, seq, tenant, (body.items ?? []) as DeskItem[]);
        if (cancelled || accepted === null) return;
        setRole(roleText);
        setError("");
        setItems(accepted);
      } catch (e) {
        const accepted = acceptDeskPayload(gen.current, seq, tenant, [] as DeskItem[]);
        if (cancelled || accepted === null) return;
        setError(productError((e as Error).message));
        setItems([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenant, reload]);

  const panel = receptionSessionFrame(selected, selectedTenant, scope.tenant);

  return (
    <main data-page="reception">
      <header className="page-head">
        <h1>接待工作台</h1>
        <Link className="btn" href={returnFocus ? todayReturnHref(returnFocus) : "/"}>{returnFocus ? "返回继续" : "返回工作台"}</Link>
      </header>
      <p className="muted">
        <span data-tone="human">人工事实</span> 当前身份：{ROLE_TEXT[role] ?? "未确认"}。当前负责人、待处理原因和人工待办。
      </p>
      <p data-tone="ai">模型不可用时，人工仍可在会话里继续回复。这里不会自动外呼。</p>
      {!scope.tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {error ? (
        <div data-state="error">
          <SurfaceState
            kind="error"
            title="接待没有载入"
            detail={error}
            action={
              <Button variant="secondary" type="button" size="sm" onClick={() => setReload((n) => n + 1)}>
                重试
              </Button>
            }
          />
        </div>
      ) : null}
      {scope.tenant && items === null && !error ? (
        <div data-state="loading">
          <SurfaceState kind="loading" title="正在读取接待" detail="进行中的会话马上就位。" />
        </div>
      ) : null}
      {items && items.length === 0 && !error ? (
        <div data-state="empty">
          <SurfaceState kind="empty" title="没有进行中的接待" detail="新的咨询进来后会出现在这里。" />
        </div>
      ) : null}
      <div className="record-list">
        {(items ?? []).map((item, index) => {
          const open = () => {
            setSelected(item.session_id);
            setSelectedTenant(scope.tenant);
          };
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
                <Button variant="primary" type="button" size="sm" data-page-primary="true" onClick={open}>
                  {pagePrimary("reception")}
                </Button>
              ) : undefined}
              secondary={primary ? undefined : (
                <Button variant="secondary" type="button" size="sm" onClick={open}>
                  {selected === item.session_id ? "处理中" : "处理"}
                </Button>
              )}
            />
          );
        })}
      </div>
      {panel ? (
        <SessionPanel tenant={panel.tenant} sessionId={panel.sessionId} onChanged={() => setReload((n) => n + 1)} />
      ) : null}
    </main>
  );
}
