"use client";
import React, { createElement } from "react";
import { useRendererScope } from "./internal/ScopeContext";
export interface SectionHeaderProps { title: string; description?: string; actions?: React.ReactNode; headingLevel?: 2 | 3 | 4 }
const present = (n: React.ReactNode) => n !== undefined && n !== null && typeof n !== "boolean" && typeof n !== "number" && n !== "";
export function SectionHeader({ title, description, actions, headingLevel = 2 }: SectionHeaderProps) {
  useRendererScope();
  if (typeof title !== "string" || !title.trim() || ![2, 3, 4].includes(headingLevel)) throw new Error("renderer header props invalid");
  return (
    <div className="pn-r-section-header">
      <div className="pn-r-section-header-text">
        {createElement(`h${headingLevel}`, { className: "pn-r-section-title" }, title)}
        {typeof description === "string" && description && <p className="pn-r-section-description">{description}</p>}
      </div>
      {present(actions) && <div className="pn-r-section-actions">{actions}</div>}
    </div>
  );
}
