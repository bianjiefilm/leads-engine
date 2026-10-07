"use client";
import React from "react";
import { CircleCheck, CircleX, Clock, Hourglass, LoaderCircle, RefreshCw, TriangleAlert, WifiOff, type LucideIcon } from "lucide-react";
import { useRendererScope } from "./internal/ScopeContext";
import type { BadgeTone } from "./Badge";
export type StatusState = "queued" | "running" | "waiting" | "resultReady" | "needsAttention" | "reconciling" | "failed" | "offline";
const MAP: Record<StatusState, { tone: BadgeTone; icon: LucideIcon }> = {
  queued: { tone: "neutral", icon: Clock },
  running: { tone: "info", icon: LoaderCircle },
  waiting: { tone: "neutral", icon: Hourglass },
  resultReady: { tone: "success", icon: CircleCheck },
  needsAttention: { tone: "warning", icon: TriangleAlert },
  reconciling: { tone: "info", icon: RefreshCw },
  failed: { tone: "danger", icon: CircleX },
  offline: { tone: "neutral", icon: WifiOff },
};
export interface StatusProps { state: StatusState; label: string; announce?: boolean }
/** Closed state enum. Anything outside it (including prototype keys) degrades to a neutral badge, never to a success colour. */
export function Status({ state, label, announce = false }: StatusProps) {
  useRendererScope();
  if (typeof label !== "string" || !label.trim() || typeof announce !== "boolean") throw new Error("renderer status props invalid");
  const known = typeof state === "string" && Object.hasOwn(MAP, state);
  const entry = known ? MAP[state] : null;
  const Icon = entry?.icon;
  return (
    <span className={`pn-r-status pn-r-badge pn-r-badge-${entry ? entry.tone : "neutral"}`} data-pn-status={known ? state : "unknown"} role={announce ? "status" : undefined}>
      {Icon && <Icon className={state === "running" ? "pn-r-spinner" : undefined} aria-hidden="true" focusable="false" />}
      {label}
    </span>
  );
}
