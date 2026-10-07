"use client";
import React from "react";
import { Button } from "./Button";
import { SafeError } from "./internal/SafeError";
import { useRendererScope } from "./internal/ScopeContext";
export interface InlineErrorProps { message: string; fieldId?: string; retryLabel?: string; onRetry?: () => void | Promise<void> }
/** `message` is a caller-written safe string, never an Error. With `fieldId` the alert gets the id `<fieldId>-error` for aria-describedby. */
export function InlineError({ message, fieldId, retryLabel = "重试", onRetry }: InlineErrorProps) {
  useRendererScope();
  if (typeof message !== "string" || !message.trim() || (fieldId !== undefined && (typeof fieldId !== "string" || !fieldId)) || typeof retryLabel !== "string" || !retryLabel.trim() || (onRetry !== undefined && typeof onRetry !== "function")) throw new Error("renderer inline error props invalid");
  return (
    <div className="pn-r-inline-error">
      <SafeError id={fieldId ? `${fieldId}-error` : undefined} message={message} />
      {onRetry && <Button variant="secondary" size="sm" onAction={onRetry}>{retryLabel}</Button>}
    </div>
  );
}
