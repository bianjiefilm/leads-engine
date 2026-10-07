'use client';
import React, { forwardRef, useId, useRef } from 'react';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { ScopedPortal, useScopedPortalTargetConnected } from './internal/ScopedPortal';
import { useScopeValue } from './internal/ScopeContext';
import { assignRef } from './internal/Field';
export interface PopoverProps {
  open: boolean;
  onOpenChange: (open: boolean, details: BasePopover.Root.ChangeEventDetails) => void;
  triggerLabel: string;
  popupLabel: string;
  children: React.ReactNode;
  side?: 'top' | 'right' | 'bottom' | 'left';
  alignment?: 'start' | 'center' | 'end';
  finalFocus?: React.RefObject<HTMLElement | null>;
  disabled?: boolean;
  disabledReason?: string;
}
const keys = [
  'open',
  'onOpenChange',
  'triggerLabel',
  'popupLabel',
  'children',
  'side',
  'alignment',
  'finalFocus',
  'disabled',
  'disabledReason',
];
function invalid(): never {
  throw new Error('renderer popover props invalid');
}
function dataRef(value: unknown): { current: HTMLElement | null } | null {
  if (
    !value ||
    typeof value !== 'object' ||
    (Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null) ||
    Reflect.ownKeys(value).some((k) => k !== 'current')
  )
    return null;
  const p = Object.getOwnPropertyDescriptor(value, 'current');
  return p &&
    'value' in p &&
    (p.value === null || (typeof HTMLElement !== 'undefined' && p.value instanceof HTMLElement))
    ? (value as { current: HTMLElement | null })
    : null;
}
export const Popover = forwardRef<HTMLButtonElement, PopoverProps>(function Popover(props, ref) {
  const scope = useScopeValue(),
    targetConnected = useScopedPortalTargetConnected(),
    id = useId(),
    trigger = useRef<HTMLButtonElement | null>(null),
    popup = useRef<HTMLDivElement | null>(null);
  const {
    open,
    onOpenChange,
    triggerLabel,
    popupLabel,
    children,
    side = 'bottom',
    alignment = 'center',
    finalFocus,
    disabled = false,
    disabledReason,
  } = props;
  const connected = (node: HTMLElement | null, portalled = false) => {
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
  if (
    Reflect.ownKeys(props).some(
      (k) => k !== 'key' && k !== 'ref' && (typeof k !== 'string' || !keys.includes(k)),
    ) ||
    typeof open !== 'boolean' ||
    typeof onOpenChange !== 'function' ||
    typeof triggerLabel !== 'string' ||
    !triggerLabel.trim() ||
    typeof popupLabel !== 'string' ||
    !popupLabel.trim() ||
    !Object.hasOwn(props, 'children') ||
    !['top', 'right', 'bottom', 'left'].includes(side) ||
    !['start', 'center', 'end'].includes(alignment) ||
    typeof disabled !== 'boolean' ||
    (disabledReason !== undefined && (typeof disabledReason !== 'string' || !disabledReason.trim()))
  )
    invalid();
  if (finalFocus !== undefined) {
    const r = dataRef(finalFocus);
    if (!r || (r.current !== null && !connected(r.current))) invalid();
  }
  const finalTarget = () => {
    const r = dataRef(finalFocus);
    return r && connected(r.current) ? r.current : true;
  };
  return (
    <BasePopover.Root
      open={open}
      modal={false}
      triggerId={id}
      onOpenChange={(next, details) => {
        if (
          disabled ||
          !targetConnected() ||
          !connected(trigger.current) ||
          (open && !connected(popup.current, true))
        ) {
          details.cancel();
          return;
        }
        onOpenChange(next, details);
      }}
    >
      <span className="pn-r-control">
        <BasePopover.Trigger
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
        </BasePopover.Trigger>
        {disabledReason && (
          <span id={id + '-reason'} className="pn-r-reason">
            {disabledReason}
          </span>
        )}
      </span>
      <ScopedPortal kind="popover">
        <BasePopover.Positioner side={side} align={alignment} className="pn-r-overlay-positioner">
          <BasePopover.Popup
            ref={popup}
            finalFocus={finalFocus === undefined ? undefined : finalTarget}
            className="pn-r-popover"
          >
            <BasePopover.Title className="pn-r-popover-title">{popupLabel}</BasePopover.Title>
            {children}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </ScopedPortal>
    </BasePopover.Root>
  );
});
