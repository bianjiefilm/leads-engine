"use client";
import React, { createElement } from "react";
import { useRendererScope } from "./internal/ScopeContext";
export interface PageHeaderProps { title: string; description?: string; breadcrumb?: React.ReactNode; actions?: React.ReactNode; headingLevel?: 1 | 2 }
const present = (n: React.ReactNode) => n !== undefined && n !== null && typeof n !== "boolean" && typeof n !== "number" && n !== "";
export function PageHeader({ title, description, breadcrumb, actions, headingLevel = 1 }: PageHeaderProps) {
  useRendererScope();
  if (typeof title !== "string" || !title.trim() || ![1, 2].includes(headingLevel)) throw new Error("renderer header props invalid");
  return (
    <header className="pn-r-page-header">
      {present(breadcrumb) && <nav className="pn-r-page-breadcrumb" aria-label="面包屑">{breadcrumb}</nav>}
      <div className="pn-r-page-header-row">
        <div className="pn-r-page-header-text">
          {createElement(`h${headingLevel}`, { className: "pn-r-page-title" }, title)}
          {typeof description === "string" && description && <p className="pn-r-page-description">{description}</p>}
        </div>
        {present(actions) && <div className="pn-r-page-actions">{actions}</div>}
      </div>
    </header>
  );
}
