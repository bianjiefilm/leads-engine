"use client";
import React, { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from "react";
import { Toast as BaseToast } from "@base-ui/react/toast";
import { X } from "lucide-react";
import { createToastQueue, TOAST_DISPLAY_LIMIT, type AdmitResult, type ToastMessage, type ToastQueue } from "./internal/ToastQueue";
import { IconButton } from "./IconButton";
import { useRendererScope } from "./internal/ScopeContext";
import { ScopedPortal } from "./internal/ScopedPortal";
export type { AdmitResult, ToastMessage, ToastType } from "./internal/ToastQueue";
interface Api { show(m: ToastMessage): AdmitResult; close(id: string): void }
interface Ctx { api: Api; queue: ToastQueue; bump(): void; bridgeClosing: React.MutableRefObject<boolean>; claimViewport(): { count: number; release(): void } }
const ToastContext = createContext<Ctx | null>(null);
const MAX_TIMER_MS = 0x7fffffff;
export interface ToastProviderProps { children: React.ReactNode; now?: () => number }
/** Mirrors the queue's visible window into Base UI's store. The queue stays the single source of truth for expiry. */
function Bridge({ visible, ctx }: { visible: readonly ToastMessage[]; ctx: Ctx }) {
  const { add, close } = BaseToast.useToastManager();
  const active = useRef(new Set<string>());
  useEffect(() => {
    const want = new Set(visible.map((v) => v.id));
    for (const v of visible) {
      if (active.current.has(v.id)) continue;
      active.current.add(v.id);
      add({ id: v.id, type: v.type, description: v.text, timeout: 0, priority: v.type === "error" ? "high" : "low",
        onClose: () => { active.current.delete(v.id); if (ctx.bridgeClosing.current) return; ctx.queue.close(v.id); ctx.bump(); } });
    }
    for (const id of [...active.current]) {
      if (want.has(id)) continue;
      // Deferral/expiry only hides the visual; it must not delete a still-valid queue entry.
      ctx.bridgeClosing.current = true;
      try { close(id); } finally { ctx.bridgeClosing.current = false; }
      active.current.delete(id);
    }
  }, [visible, add, close, ctx]);
  return null;
}
export function ToastProvider({ children, now = Date.now }: ToastProviderProps) {
  const { modalActive } = useRendererScope();
  const queueRef = useRef<ToastQueue | null>(null);
  if (!queueRef.current) queueRef.current = createToastQueue();
  const queue = queueRef.current;
  const nowRef = useRef(now); nowRef.current = now;
  const [version, bump] = useReducer((x: number) => x + 1, 0);
  const bridgeClosing = useRef(false);
  const viewports = useRef(0);
  const t = nowRef.current();
  const snapshot = queue.visible(t, modalActive);
  const key = snapshot.map((m) => m.id).join("\u0000");
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const visible = useMemo(() => snapshot, [key]);
  const next = queue.nextExpiry(t);
  useEffect(() => {
    if (next === null) return;
    // Timers beyond 2^31-1 ms overflow to 1ms; clamp, and the effect re-arms with the true remainder when it fires.
    const id = setTimeout(bump, Math.min(Math.max(1, next - nowRef.current()), MAX_TIMER_MS));
    return () => clearTimeout(id);
  }, [next, version]);
  const claimViewport = useCallback(() => { viewports.current += 1; return { count: viewports.current, release() { viewports.current -= 1; } }; }, []);
  const api = useMemo<Api>(() => ({
    show(m) { const r = queue.admit(m, nowRef.current()); if (r.ok && !r.deduped) bump(); return r; },
    close(id) { queue.close(id); bump(); },
  }), [queue]);
  const ctx = useMemo<Ctx>(() => ({ api, queue, bump, bridgeClosing, claimViewport }), [api, queue, claimViewport]);
  return <ToastContext.Provider value={ctx}><BaseToast.Provider timeout={0} limit={TOAST_DISPLAY_LIMIT}><Bridge visible={visible} ctx={ctx} />{children}</BaseToast.Provider></ToastContext.Provider>;
}
export function useToast(): Api {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("renderer toast provider required");
  return ctx.api;
}
function List() {
  const { toasts, close } = BaseToast.useToastManager();
  return <>{toasts.map((toast) => (
    <BaseToast.Root key={toast.id} toast={toast} className="pn-r-toast" data-pn-toast-type={toast.type} swipeDirection={[]}>
      <BaseToast.Content className="pn-r-toast-content">
        <BaseToast.Description className="pn-r-toast-text" render={<div />} />
        <IconButton className="pn-r-toast-close" variant="ghost" size="sm" label="关闭通知" icon={X} onClick={() => close(toast.id)} />
      </BaseToast.Content>
    </BaseToast.Root>
  ))}</>;
}
function ToastViewport() {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("renderer toast provider required");
  const [duplicate, setDuplicate] = useState(false);
  // A second viewport would create a second live region; the second claim is detected right after mount.
  useLayoutEffect(() => { const claim = ctx.claimViewport(); if (claim.count > 1) setDuplicate(true); return claim.release; }, [ctx]);
  if (duplicate) throw new Error("renderer toast viewport duplicate");
  return (
    <ScopedPortal kind="toast">
      <BaseToast.Viewport className="pn-r-toast-viewport" role="status" aria-live="polite"><List /></BaseToast.Viewport>
    </ScopedPortal>
  );
}
export const Toast = { Provider: ToastProvider, useToast, Viewport: ToastViewport };
