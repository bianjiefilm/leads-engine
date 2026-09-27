"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { MODE_TEXT, PENDING_TEXT } from "@/lib/reception";

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

const TENANT_KEY = "leads_tenant_id";

export default function ReceptionDeskPage() {
  const [tenant, setTenant] = useState("");
  const [items, setItems] = useState<DeskItem[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    setTenant(window.localStorage.getItem(TENANT_KEY) ?? "");
  }, []);

  const load = useCallback(async (tenantID: string) => {
    setError("");
    if (!tenantID.trim()) {
      setItems([]);
      setError("先填写当前租户");
      return;
    }
    window.localStorage.setItem(TENANT_KEY, tenantID.trim());
    try {
      const res = await fetch("/api/reception/desk", { headers: { "x-tenant-id": tenantID.trim() } });
      const body = await res.json();
      if (!res.ok) {
        setError(body.message ?? body.error ?? `HTTP ${res.status}`);
        setItems([]);
        return;
      }
      setItems(body.items ?? []);
    } catch (e) {
      setError((e as Error).message);
      setItems([]);
    }
  }, []);

  return (
    <main>
      <p className="muted"><Link href="/">返回工作台</Link></p>
      <h1>接待工作台</h1>
      <p className="muted">当前负责人、待处理原因和人工待办。模型不可用时，人工仍可在会话里继续回复。</p>
      <form className="composer" onSubmit={(event) => { event.preventDefault(); void load(tenant); }}>
        <input aria-label="租户" value={tenant} onChange={(event) => setTenant(event.target.value)} placeholder="租户 id" />
        <button className="primary" type="submit">查看</button>
      </form>
      {error ? <p className="error">{error}</p> : null}
      {items == null ? <p className="muted">填写租户后查看进行中的接待。</p> : null}
      {items && items.length === 0 && !error ? <p className="muted">没有进行中的接待。</p> : null}
      {items && items.length > 0 ? (
        <table>
          <thead>
            <tr>
              <th>会话</th>
              <th>模式</th>
              <th>负责人</th>
              <th>待处理</th>
              <th>人工待办</th>
              <th>下一次跟进</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <tr key={item.session_id}>
                <td><code>{item.session_id}</code></td>
                <td>{MODE_TEXT[item.mode] ?? item.mode}</td>
                <td>{item.owner_member_id ? <code>{item.owner_member_id}</code> : "尚未接管"}</td>
                <td>{item.pending_reason ? (PENDING_TEXT[item.pending_reason] ?? item.pending_reason) : "无"}</td>
                <td>{item.human_todo ? "是" : "否"}</td>
                <td>{item.next_follow_up_at || "未安排"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
    </main>
  );
}
