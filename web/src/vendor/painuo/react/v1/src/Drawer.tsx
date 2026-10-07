'use client';
import React, { forwardRef, useRef } from 'react';
import { Drawer as BaseDrawer } from '@base-ui/react/drawer';
import { X } from 'lucide-react';
import { ScopedPortal, useScopedPortalTargetConnected } from './internal/ScopedPortal';
import { useScopeValue } from './internal/ScopeContext';
import { ModalPresence } from './internal/ModalPresence';
import { assignRef } from './internal/Field';
export interface DrawerProps {
  open: boolean;
  onOpenChange: (open: boolean, details: BaseDrawer.Root.ChangeEventDetails) => void;
  title: string;
  description?: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
  busy?: boolean;
  side?: 'right' | 'left' | 'top' | 'bottom';
  dismissOnOutsidePress?: boolean;
  initialFocus?: React.RefObject<HTMLElement | null>;
  finalFocus?: React.RefObject<HTMLElement | null>;
}
const keys = [
  'open',
  'onOpenChange',
  'title',
  'description',
  'children',
  'footer',
  'busy',
  'side',
  'dismissOnOutsidePress',
  'initialFocus',
  'finalFocus',
];
const directions = { right: 'right', left: 'left', top: 'up', bottom: 'down' } as const;
function invalid(): never {
  throw new Error('renderer drawer props invalid');
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
export const Drawer = forwardRef<HTMLDivElement, DrawerProps>(function Drawer(props, ref) {
  const scope = useScopeValue(),
    targetConnected = useScopedPortalTargetConnected(),
    popup = useRef<HTMLDivElement | null>(null);
  const {
    open,
    onOpenChange,
    title,
    description,
    children,
    footer,
    busy = false,
    side = 'right',
    dismissOnOutsidePress = false,
    initialFocus,
    finalFocus,
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
    typeof title !== 'string' ||
    !title.trim() ||
    (description !== undefined && (typeof description !== 'string' || !description.trim())) ||
    !Object.prototype.hasOwnProperty.call(props, 'children') ||
    typeof busy !== 'boolean' ||
    typeof dismissOnOutsidePress !== 'boolean' ||
    !['right', 'left', 'top', 'bottom'].includes(side)
  )
    invalid();
  for (const focus of [initialFocus, finalFocus])
    if (focus !== undefined) {
      const r = dataRef(focus);
      if (!r || (r.current !== null && !connected(r.current))) invalid();
    }
  const focusTarget = (focus: unknown) => () => {
    const r = dataRef(focus);
    return r && connected(r.current) ? r.current : true;
  };
  return (
    <BaseDrawer.Root
      open={open}
      modal={true}
      swipeDirection={directions[side]}
      disablePointerDismissal={!dismissOnOutsidePress}
      onOpenChange={(next, details) => {
        if (busy || !targetConnected() || !open || !connected(popup.current, true)) {
          details.cancel();
          return;
        }
        onOpenChange(next, details);
      }}
    >
      <ModalPresence open={open} />
      <ScopedPortal kind="drawer">
        <BaseDrawer.Backdrop className="pn-r-drawer-backdrop pn-r-backdrop" />
        <BaseDrawer.Viewport className={'pn-r-drawer-viewport pn-r-drawer-viewport-' + side}>
          <BaseDrawer.Popup
            ref={(node) => {
              popup.current = node;
              assignRef(ref, node);
            }}
            initialFocus={initialFocus === undefined ? undefined : focusTarget(initialFocus)}
            finalFocus={finalFocus === undefined ? undefined : focusTarget(finalFocus)}
            aria-modal={true}
            aria-busy={busy || undefined}
            className={'pn-r-drawer pn-r-drawer-' + side}
          >
            <div className="pn-r-dialog-heading">
              <BaseDrawer.Title className="pn-r-drawer-title pn-r-dialog-title">
                {title}
              </BaseDrawer.Title>
              <BaseDrawer.Close
                render={<button type="button" />}
                className="pn-r-drawer-close pn-r-button pn-r-ghost"
                disabled={busy}
                aria-label="关闭"
              >
                <X aria-hidden="true" focusable="false" />
              </BaseDrawer.Close>
            </div>
            {description && (
              <BaseDrawer.Description className="pn-r-drawer-description pn-r-description">
                {description}
              </BaseDrawer.Description>
            )}
            <BaseDrawer.Content className="pn-r-drawer-content">{children}</BaseDrawer.Content>
            {footer !== undefined && footer !== null && (
              <div className="pn-r-drawer-footer">{footer}</div>
            )}
          </BaseDrawer.Popup>
        </BaseDrawer.Viewport>
      </ScopedPortal>
    </BaseDrawer.Root>
  );
});
