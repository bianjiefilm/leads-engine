"use client";
import React, { createElement } from "react";
import type { LucideIcon } from "lucide-react";
import { useRendererScope } from "./internal/ScopeContext";
export interface EmptyStateProps { title: string; description?: string; headingLevel?: 2 | 3 | 4; action?: React.ReactNode; icon?: LucideIcon }
export function EmptyState({ title, description, headingLevel = 3, action, icon: Icon }: EmptyStateProps) {
  useRendererScope();
  if (typeof title !== "string" || !title.trim() || ![2, 3, 4].includes(headingLevel)) throw new Error("renderer empty state props invalid");
  // Only real text/nodes render; numbers and booleans are ignored so a falsy 0 never leaks into the DOM.
  const text = typeof description === "string" && description.length > 0 ? description : null;
  const node = action !== undefined && action !== null && typeof action !== "boolean" && typeof action !== "number" && action !== "" ? action : null;
  return (
    <section className="pn-r-empty">
      {Icon && <Icon className="pn-r-empty-icon" aria-hidden="true" focusable="false" />}
      {createElement(`h${headingLevel}`, { className: "pn-r-empty-title" }, title)}
      {text && <p className="pn-r-empty-description">{text}</p>}
      {node && <div className="pn-r-empty-action">{node}</div>}
    </section>
  );
}
