export type ToastType = "info" | "success" | "warning" | "error";
export interface ToastMessage { id: string; type: ToastType; text: string; createdAt: number; expiresAt: number | null; actionLabel?: string }
export type AdmitResult = { ok: true; deduped: boolean } | { ok: false; reason: "capacity" | "expired" | "invalid" };
export interface ToastQueue {
  admit(m: ToastMessage, nowMs: number): AdmitResult;
  close(id: string): void;
  /** At most 3 messages in admission order; `[]` while a modal is active. Expired messages are purged and never return. */
  visible(nowMs: number, modalActive: boolean): readonly ToastMessage[];
  size(nowMs: number): number;
  /** Earliest finite `expiresAt` still in the queue, or null. */
  nextExpiry(nowMs: number): number | null;
}
export const TOAST_CAPACITY = 20;
export const TOAST_DISPLAY_LIMIT = 3;
const TYPES = ["info", "success", "warning", "error"];
const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);
function valid(m: unknown): m is ToastMessage {
  if (!m || typeof m !== "object" || Array.isArray(m)) return false;
  const v = m as Record<string, unknown>;
  return typeof v.id === "string" && v.id.length > 0 && typeof v.type === "string" && TYPES.includes(v.type)
    && typeof v.text === "string" && v.text.length > 0 && finite(v.createdAt)
    && (finite(v.expiresAt) || (v.expiresAt === null && v.type === "error")) && (v.actionLabel === undefined || typeof v.actionLabel === "string");
}
/** Pure queue rules with no React and no module-level state; each call returns an independent instance. */
export function createToastQueue(): ToastQueue {
  let items: ToastMessage[] = [];
  const purge = (now: number) => { items = items.filter((i) => i.expiresAt === null || i.expiresAt > now); };
  return {
    admit(m, now) {
      if (!valid(m) || !finite(now)) return { ok: false, reason: "invalid" };
      if (m.expiresAt !== null && m.expiresAt <= now) return { ok: false, reason: "expired" };
      purge(now);
      if (items.some((i) => i.id === m.id)) return { ok: true, deduped: true };
      if (items.length >= TOAST_CAPACITY) return { ok: false, reason: "capacity" };
      items.push({ id: m.id, type: m.type, text: m.text, createdAt: m.createdAt, expiresAt: m.expiresAt, ...(m.actionLabel === undefined ? {} : { actionLabel: m.actionLabel }) });
      return { ok: true, deduped: false };
    },
    close(id) { items = items.filter((i) => i.id !== id); },
    visible(now, modalActive) { purge(now); return modalActive ? [] : items.slice(0, TOAST_DISPLAY_LIMIT); },
    size(now) { purge(now); return items.length; },
    nextExpiry(now) { purge(now); let n: number | null = null; for (const i of items) if (i.expiresAt !== null && (n === null || i.expiresAt < n)) n = i.expiresAt; return n; },
  };
}
