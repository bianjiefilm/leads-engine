'use client';
import React, { forwardRef, useId, useRef } from 'react';
import { Menu as BaseMenu } from '@base-ui/react/menu';
import { ScopedPortal, useScopedPortalTargetConnected } from './internal/ScopedPortal';
import { useScopeValue } from './internal/ScopeContext';
import { assignRef } from './internal/Field';
export type DropdownNode =
  | {
      kind: 'action';
      id: string;
      label: string;
      onSelect: () => void;
      disabled?: boolean;
      reason?: string;
    }
  | { kind: 'separator'; id?: string }
  | {
      kind: 'group';
      id: string;
      label: string;
      items: readonly DropdownNode[];
      disabled?: boolean;
      reason?: string;
    }
  | {
      kind: 'submenu';
      id: string;
      label: string;
      items: readonly DropdownNode[];
      open: boolean;
      onOpenChange: (open: boolean, details: BaseMenu.SubmenuRoot.ChangeEventDetails) => void;
      disabled?: boolean;
      reason?: string;
    };
export interface DropdownProps {
  open: boolean;
  onOpenChange: (open: boolean, details: BaseMenu.Root.ChangeEventDetails) => void;
  triggerLabel: string;
  items: readonly DropdownNode[];
  disabled?: boolean;
  disabledReason?: string;
}
type ValidNode = { node: DropdownNode; index: number; children: ValidNode[] };
function invalid(): never {
  throw new Error('renderer dropdown props invalid');
}
const nonblank = (v: unknown) => typeof v === 'string' && Boolean(v.trim());
function validate(items: unknown): ValidNode[] {
  const ancestors = new Set<object>(),
    ids = new Set<string>();
  let count = 0;
  function walk(list: unknown, depth: number): ValidNode[] {
    if (!Array.isArray(list) || list.length > 200) invalid();
    const out: ValidNode[] = [];
    for (let i = 0; i < list.length; i++) {
      const entry = Object.getOwnPropertyDescriptor(list, String(i));
      if (!entry || !('value' in entry)) invalid();
      const n = entry.value;
      if (
        depth > 8 ||
        ++count > 200 ||
        !n ||
        typeof n !== 'object' ||
        Array.isArray(n) ||
        (Object.getPrototypeOf(n) !== Object.prototype && Object.getPrototypeOf(n) !== null) ||
        ancestors.has(n)
      )
        invalid();
      const descriptors = Object.getOwnPropertyDescriptors(n);
      if (!descriptors.kind || !('value' in descriptors.kind)) invalid();
      if (
        Reflect.ownKeys(descriptors).some(
          (k) => typeof k !== 'string' || !('value' in descriptors[k]!),
        )
      )
        invalid();
      const allowed =
        n.kind === 'action'
          ? ['kind', 'id', 'label', 'onSelect', 'disabled', 'reason']
          : n.kind === 'separator'
            ? ['kind', 'id']
            : n.kind === 'group'
              ? ['kind', 'id', 'label', 'items', 'disabled', 'reason']
              : n.kind === 'submenu'
                ? ['kind', 'id', 'label', 'items', 'open', 'onOpenChange', 'disabled', 'reason']
                : null;
      const required =
        n.kind === 'separator'
          ? ['kind']
          : n.kind === 'action'
            ? ['kind', 'id', 'label', 'onSelect']
            : n.kind === 'group'
              ? ['kind', 'id', 'label', 'items']
              : ['kind', 'id', 'label', 'items', 'open', 'onOpenChange'];
      if (required.some((k) => !Object.prototype.hasOwnProperty.call(descriptors, k))) invalid();
      if (!allowed || Reflect.ownKeys(n).some((k) => typeof k !== 'string' || !allowed.includes(k)))
        invalid();
      if (n.kind !== 'separator' || Object.prototype.hasOwnProperty.call(n, 'id')) {
        if (!nonblank(n.id) || ids.has(n.id)) invalid();
        ids.add(n.id);
      }
      if (
        n.kind !== 'separator' &&
        (!nonblank(n.label) ||
          (n.disabled !== undefined && typeof n.disabled !== 'boolean') ||
          (n.reason !== undefined && !nonblank(n.reason)))
      )
        invalid();
      if (n.kind === 'action' && typeof n.onSelect !== 'function') invalid();
      if (
        n.kind === 'submenu' &&
        (typeof n.open !== 'boolean' || typeof n.onOpenChange !== 'function')
      )
        invalid();
      const index = count;
      ancestors.add(n);
      const children = n.kind === 'group' || n.kind === 'submenu' ? walk(n.items, depth + 1) : [];
      ancestors.delete(n);
      out.push({ node: n, index, children });
    }
    return out;
  }
  return walk(items, 1);
}
function useOwnedNode() {
  const scope = useScopeValue();
  return (node: HTMLElement | null, portalled: boolean) => {
    const owner = scope.portalRoot,
      root = owner?.closest('.pn-r-scope');
    return Boolean(
      node?.isConnected &&
      root?.isConnected &&
      root.contains(node) &&
      node.closest('.pn-r-scope') === root &&
      (!portalled || (owner?.contains(node) && node.closest('.pn-r-portal') === owner)),
    );
  };
}
interface BranchProps {
  entry: ValidNode;
  prefix: string;
  reasons: string[];
  blocked: boolean;
  active: () => boolean;
}
function Branch({ entry, prefix, reasons, blocked, active }: BranchProps) {
  const targetConnected = useScopedPortalTargetConnected(),
    connected = useOwnedNode(),
    trigger = useRef<HTMLElement | null>(null),
    popup = useRef<HTMLDivElement | null>(null);
  const n = entry.node;
  const ownReason =
    n.kind !== 'separator' && n.reason ? prefix + '-reason-' + entry.index : undefined;
  const reasonIDs = ownReason ? [...reasons, ownReason] : reasons;
  const described = reasonIDs.join(' ') || undefined;
  const disabled = blocked || (n.kind !== 'separator' && Boolean(n.disabled));
  const reason =
    ownReason && n.kind !== 'separator' ? (
      <span id={ownReason} className="pn-r-reason">
        {n.reason}
      </span>
    ) : null;
  const children = (enabled: () => boolean) => (
    <>
      {entry.children.map((e) => (
        <Branch
          key={e.index}
          entry={e}
          prefix={prefix}
          reasons={reasonIDs}
          blocked={disabled}
          active={enabled}
        />
      ))}
    </>
  );
  if (n.kind === 'separator') return <BaseMenu.Separator className="pn-r-menu-separator" />;
  if (n.kind === 'group')
    return (
      <BaseMenu.Group className="pn-r-menu-group">
        <BaseMenu.GroupLabel className="pn-r-menu-group-label">{n.label}</BaseMenu.GroupLabel>
        {reason}
        {children(() => active() && !disabled)}
      </BaseMenu.Group>
    );
  if (n.kind === 'action')
    return (
      <>
        <BaseMenu.Item
          label={n.label}
          disabled={disabled}
          aria-describedby={described}
          className="pn-r-menu-item"
          onClick={(event) => {
            if (
              disabled ||
              !active() ||
              !targetConnected() ||
              !connected(event.currentTarget, true)
            ) {
              event.preventBaseUIHandler();
              return;
            }
            n.onSelect();
          }}
        >
          {n.label}
        </BaseMenu.Item>
        {reason}
      </>
    );
  return (
    <BaseMenu.SubmenuRoot
      open={n.open}
      disabled={disabled}
      onOpenChange={(next, details) => {
        if (
          disabled ||
          !active() ||
          !targetConnected() ||
          !connected(trigger.current, true) ||
          (n.open && !connected(popup.current, true))
        ) {
          details.cancel();
          return;
        }
        n.onOpenChange(next, details);
      }}
    >
      <BaseMenu.SubmenuTrigger
        ref={trigger}
        label={n.label}
        disabled={disabled}
        aria-describedby={described}
        className="pn-r-menu-item"
      >
        {n.label}
      </BaseMenu.SubmenuTrigger>
      {reason}
      <ScopedPortal kind="menu">
        <BaseMenu.Positioner side="right" align="start" className="pn-r-overlay-positioner">
          <BaseMenu.Popup ref={popup} className="pn-r-menu">
            {children(() => active() && !disabled && n.open && connected(popup.current, true))}
          </BaseMenu.Popup>
        </BaseMenu.Positioner>
      </ScopedPortal>
    </BaseMenu.SubmenuRoot>
  );
}
const keys = ['open', 'onOpenChange', 'triggerLabel', 'items', 'disabled', 'disabledReason'];
export const Dropdown = forwardRef<HTMLButtonElement, DropdownProps>(function Dropdown(props, ref) {
  const targetConnected = useScopedPortalTargetConnected(),
    connected = useOwnedNode(),
    id = useId(),
    trigger = useRef<HTMLButtonElement | null>(null),
    popup = useRef<HTMLDivElement | null>(null);
  const { open, onOpenChange, triggerLabel, items, disabled = false, disabledReason } = props;
  if (
    Reflect.ownKeys(props).some(
      (k) => k !== 'key' && k !== 'ref' && (typeof k !== 'string' || !keys.includes(k)),
    ) ||
    typeof open !== 'boolean' ||
    typeof onOpenChange !== 'function' ||
    !nonblank(triggerLabel) ||
    typeof disabled !== 'boolean' ||
    (disabledReason !== undefined && !nonblank(disabledReason))
  )
    invalid();
  const entries = validate(items);
  const active = () =>
    open &&
    !disabled &&
    targetConnected() &&
    connected(trigger.current, false) &&
    connected(popup.current, true);
  return (
    <BaseMenu.Root
      open={open}
      disabled={disabled}
      triggerId={id}
      onOpenChange={(next, details) => {
        if (
          disabled ||
          !targetConnected() ||
          !connected(trigger.current, false) ||
          (open && !connected(popup.current, true))
        ) {
          details.cancel();
          return;
        }
        onOpenChange(next, details);
      }}
    >
      <span className="pn-r-control">
        <BaseMenu.Trigger
          id={id}
          disabled={disabled}
          render={<button type="button" disabled={disabled} />}
          ref={(node: HTMLElement | null) => {
            trigger.current = node instanceof HTMLButtonElement ? node : null;
            assignRef(ref, trigger.current);
          }}
          aria-describedby={disabledReason ? id + '-reason' : undefined}
          className="pn-r-button pn-r-secondary"
        >
          {triggerLabel}
        </BaseMenu.Trigger>
        {disabledReason && (
          <span id={id + '-reason'} className="pn-r-reason">
            {disabledReason}
          </span>
        )}
      </span>
      <ScopedPortal kind="menu">
        <BaseMenu.Positioner side="bottom" align="start" className="pn-r-overlay-positioner">
          <BaseMenu.Popup ref={popup} className="pn-r-menu">
            {entries.map((e) => (
              <Branch
                key={e.index}
                entry={e}
                prefix={id}
                reasons={[]}
                blocked={disabled}
                active={active}
              />
            ))}
          </BaseMenu.Popup>
        </BaseMenu.Positioner>
      </ScopedPortal>
    </BaseMenu.Root>
  );
});
