"use client";
import React from "react";
import { useRendererScope } from "./internal/ScopeContext";
const SHAPES = ["line", "block", "circle"];
const WIDTHS = ["sm", "md", "lg", "full"];
export interface SkeletonProps { shape?: "line" | "block" | "circle"; count?: number; width?: "sm" | "md" | "lg" | "full" }
/** Decorative placeholder: hidden from assistive technology and without text. Pair it with a real status message. */
export function Skeleton({ shape = "line", count = 1, width = "full" }: SkeletonProps) {
  useRendererScope();
  if (!SHAPES.includes(shape) || !WIDTHS.includes(width) || !Number.isInteger(count) || count < 1 || count > 20) throw new Error("renderer skeleton props invalid");
  return (
    <div className="pn-r-skeleton" aria-hidden="true">
      {Array.from({ length: count }, (_, i) => <span key={i} className={`pn-r-skeleton-item pn-r-skeleton-${shape} pn-r-skeleton-w-${width}`} />)}
    </div>
  );
}
