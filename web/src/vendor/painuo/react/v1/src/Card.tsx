"use client";
import React, { createElement, useId } from "react";
import { useRendererScope } from "./internal/ScopeContext";
type Level = 2 | 3 | 4;
const AS = ["div", "section", "article"];
const LEVELS = [2, 3, 4];
export interface CardProps { as?: "div" | "section" | "article"; title?: string; children?: React.ReactNode }
function Header({ children }: { children?: React.ReactNode }) { useRendererScope(); return <div className="pn-r-card-header">{children}</div>; }
function Title({ level = 3, id, children }: { level?: Level; id?: string; children?: React.ReactNode }) {
  useRendererScope();
  if (!LEVELS.includes(level)) throw new Error("renderer card props invalid");
  return createElement(`h${level}`, { className: "pn-r-card-title", id }, children);
}
function Description({ children }: { children?: React.ReactNode }) { useRendererScope(); return <p className="pn-r-card-description">{children}</p>; }
function CardRoot(props: CardProps) {
  useRendererScope();
  const titleId = useId();
  const keys = Object.keys(props);
  if (keys.some((k) => !["as", "title", "children"].includes(k))) throw new Error("renderer card props invalid");
  const { as = "div", title, children } = props;
  if (!AS.includes(as) || (title !== undefined && (typeof title !== "string" || !title.trim()))) throw new Error("renderer card props invalid");
  const labelled = title !== undefined && as !== "div";
  return createElement(as, { className: "pn-r-card", "aria-labelledby": labelled ? titleId : undefined },
    title !== undefined && <Header><Title id={titleId}>{title}</Title></Header>, children);
}
export const Card = Object.assign(CardRoot, { Header, Title, Description });
