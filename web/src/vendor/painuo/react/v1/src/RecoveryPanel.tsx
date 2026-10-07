"use client";
import React from "react";
import { Button } from "./Button";
import { useRendererScope } from "./internal/ScopeContext";
export interface RecoveryPanelProps { reason: string; recoverLabel: string; onRecover: () => void | Promise<void>; disabled?: boolean; disabledReason?: string }
/** One explicit user-triggered recovery at a time (the Button owns the synchronous gate, a rejection becomes a fixed safe message, no automatic retry). */
export function RecoveryPanel({ reason, recoverLabel, onRecover, disabled = false, disabledReason }: RecoveryPanelProps) {
  useRendererScope();
  if (typeof reason !== "string" || !reason.trim() || typeof recoverLabel !== "string" || !recoverLabel.trim() || typeof onRecover !== "function" || typeof disabled !== "boolean") throw new Error("renderer recovery panel props invalid");
  return (
    <section className="pn-r-recovery">
      <p className="pn-r-recovery-reason">{reason}</p>
      <Button variant="primary" onAction={onRecover} disabled={disabled} disabledReason={disabledReason}>{recoverLabel}</Button>
    </section>
  );
}
