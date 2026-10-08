"use client";

import React, { createContext, useContext, useSyncExternalStore, type ReactNode } from "react";
import Link from "next/link";
import { Button as PainuoButton } from "@/vendor/painuo/react/v1/src/index";
import {
  SHELL_NAV,
  chooseScope,
  factTone,
  shellStructure,
  toneLabel,
  type FactTone,
  type WorkScope,
} from "@/lib/productShell";

const ShellWidthContext = createContext(1440);

function subscribe(onStoreChange: () => void) {
  window.addEventListener("resize", onStoreChange);
  return () => window.removeEventListener("resize", onStoreChange);
}

export function ShellWidthProvider({ children }: { children: ReactNode }) {
  const width = useSyncExternalStore(subscribe, () => window.innerWidth, () => 1440);
  return <ShellWidthContext.Provider value={width}>{children}</ShellWidthContext.Provider>;
}

export function useShellWidth(): number {
  return useContext(ShellWidthContext);
}

export function ToneBadge({ tone }: { tone: FactTone }) {
  return (
    <span className="tone-badge" data-tone={tone}>
      {toneLabel(tone)}
    </span>
  );
}

export function SurfaceState({
  kind,
  title,
  detail,
  action,
}: {
  kind: "loading" | "empty" | "error" | "recovery";
  title?: string;
  detail?: string;
  action?: ReactNode;
}) {
  const copy = {
    loading: { title: title ?? "正在读取", detail: detail ?? "页面还在整理，请稍候。" },
    empty: { title: title ?? "这里还没有内容", detail: detail ?? "换个条件，或等新的客户工作进来。" },
    error: { title: title ?? "这一步没有完成", detail: detail ?? "请重试。如果仍不行，换一个工作范围再看。" },
    recovery: { title: title ?? "需要先选择工作范围", detail: detail ?? "先在顶部选择工作范围。" },
  }[kind];
  return (
    <section className="surface-state" data-surface-state={kind} role={kind === "error" ? "alert" : "status"} aria-busy={kind === "loading" ? true : undefined}>
      <span className={`state-mark state-mark-${kind}`} data-state-mark={kind} aria-hidden="true" />
      {kind === "loading" ? (
        <div data-skeleton="block" aria-hidden="true">
          <span data-skeleton="title" />
          <span data-skeleton="line" />
          <span data-skeleton="line" />
        </div>
      ) : null}
      <h2>{copy.title}</h2>
      <p>{copy.detail}</p>
      {action ? <div data-state-action="true">{action}</div> : null}
    </section>
  );
}

export function RecordFrame({
  width,
  tone,
  title,
  facts,
  primary,
  secondary,
  children,
}: {
  width: number;
  tone: FactTone;
  title: string;
  facts: { label: string; value: string }[];
  primary?: ReactNode;
  secondary?: ReactNode;
  children?: ReactNode;
}) {
  const structure = shellStructure(width);
  return (
    <article className="record-frame" data-record={structure.records} data-density={structure.density} data-columns={structure.columns} data-tone={tone}>
      <header className="record-head">
        <ToneBadge tone={tone} />
        <h3>{title}</h3>
        {primary ? <div data-page-primary="true">{primary}</div> : null}
        {secondary ? <div data-page-primary="false">{secondary}</div> : null}
      </header>
      {structure.factLayout === "chips" ? (
        <ul data-facts="chips">
          {facts.map((fact) => (
            <li key={fact.label}>
              <span>{fact.label}</span>
              <strong>{fact.value}</strong>
            </li>
          ))}
        </ul>
      ) : (
        <dl data-facts="columns">
          {facts.map((fact) => (
            <div key={fact.label}>
              <dt>{fact.label}</dt>
              <dd>{fact.value}</dd>
            </div>
          ))}
        </dl>
      )}
      {children}
    </article>
  );
}

export function RecordList({
  width,
  rows,
}: {
  width: number;
  rows: {
    id: string;
    title: string;
    tone?: FactTone;
    facts: { label: string; value: string }[];
    href?: string;
    action?: string;
    primary?: boolean;
    /** HUI-2626：行内打开详情抽屉（不路由跳转，保留列表筛选/滚动）。 */
    onOpen?: () => void;
  }[];
}) {
  const structure = shellStructure(width);
  return (
    <div className="record-list" data-record={structure.records} data-density={structure.density}>
      {rows.map((row) => {
        // onOpen 优先：行动作开抽屉；href 交给抽屉内的「打开完整页」链接，避免一排同权链接。
        const action = row.onOpen ? (
          <PainuoRowButton label={row.action ?? "查看详情"} primary={row.primary === true} onOpen={row.onOpen} />
        ) : row.href ? (
          <Link className={row.primary ? "btn primary" : "btn"} href={row.href}>
            {row.action ?? "打开"}
          </Link>
        ) : null;
        return (
          <RecordFrame
            key={row.id}
            width={width}
            tone={row.tone ?? factTone({ kind: "human" })}
            title={row.title}
            facts={row.facts}
            primary={row.primary ? action : undefined}
            secondary={row.primary ? undefined : action}
          />
        );
      })}
    </div>
  );
}

// 行级抽屉触发按钮。renderer Button 需要 provider scope（壳级已提供）。
function PainuoRowButton({ label, primary, onOpen }: { label: string; primary: boolean; onOpen: () => void }) {
  return (
    <PainuoButton
      variant={primary ? "primary" : "secondary"}
      size="sm"
      type="button"
      onClick={() => onOpen()}
    >
      {label}
    </PainuoButton>
  );
}

export function WorkbenchChrome({
  width,
  scopes,
  activeId,
  onSwitch,
  children,
}: {
  width: number;
  scopes: WorkScope[];
  activeId: string | null;
  onSwitch: (tenantId: string) => void;
  children: ReactNode;
}) {
  const structure = shellStructure(width);
  const active = chooseScope(scopes, activeId ?? "");
  return (
    <div className="desk-frame" data-shell-density={structure.density} data-shell-nav={structure.navigation}>
      <div className="desk-context" id="work-context" data-work-context="tenant">
        <span>工作范围</span>
        <strong>{active?.display_name ?? "未选择"}</strong>
        <select
          aria-label="切换工作范围"
          value={active?.tenant_id ?? ""}
          onChange={(event) => {
            const chosen = chooseScope(scopes, event.target.value);
            if (chosen) onSwitch(chosen.tenant_id);
          }}
        >
          {active ? null : <option value="">未选择</option>}
          {scopes.map((scope) => (
            <option key={scope.tenant_id} value={scope.tenant_id}>
              {scope.display_name}
            </option>
          ))}
        </select>
      </div>
      <nav className="desk-nav" aria-label="销售工作台" data-shell-nav={structure.navigation}>
        {SHELL_NAV.map((item) => (
          <Link key={item.id} href={item.href}>
            {item.label}
          </Link>
        ))}
      </nav>
      <div data-shell-main="true">{children}</div>
    </div>
  );
}
