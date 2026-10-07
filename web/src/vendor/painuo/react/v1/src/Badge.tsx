"use client";
import React from "react";
import { useRendererScope } from "./internal/ScopeContext";
export const BADGE_TONES = ["neutral", "info", "success", "warning", "danger"] as const;
export type BadgeTone = (typeof BADGE_TONES)[number];
export interface BadgeProps { tone: BadgeTone; children: string }
export function Badge({ tone, children }: BadgeProps) {
  useRendererScope();
  if (typeof tone !== "string" || !BADGE_TONES.includes(tone) || typeof children !== "string" || !children.trim()) throw new Error("renderer badge props invalid");
  return <span className={`pn-r-badge pn-r-badge-${tone}`}>{children}</span>;
}
