'use client';
import React, { forwardRef, useCallback, useId, useRef, useState } from 'react';
import { Tooltip as BaseTooltip } from '@base-ui/react/tooltip';
import { ScopedPortal, useScopedPortalTargetConnected } from './internal/ScopedPortal';
import { useScopeValue } from './internal/ScopeContext';
import { assignRef } from './internal/Field';
export interface TooltipProps {
  open: boolean;
  onOpenChange: (open: boolean, details: BaseTooltip.Root.ChangeEventDetails) => void;
  triggerLabel: string;
  text: string;
  delay?: number;
  disabled?: boolean;
  disabledReason?: string;
}
const keys = [
  'open',
  'onOpenChange',
  'triggerLabel',
  'text',
  'delay',
  'disabled',
  'disabledReason',
];
function invalid(): never {
  throw new Error('renderer tooltip props invalid');
}
export const Tooltip = forwardRef<HTMLButtonElement, TooltipProps>(function Tooltip(props, ref) {
  const scope = useScopeValue(),
    targetConnected = useScopedPortalTargetConnected(),
    id = useId();
  const trigger = useRef<HTMLButtonElement | null>(null),
    popup = useRef<HTMLDivElement | null>(null);
  const [hasPopup, setHasPopup] = useState(false);
  const popupRef = useCallback((node: HTMLDivElement | null) => {
    popup.current = node;
    setHasPopup(Boolean(node));
  }, []);
  const { open, onOpenChange, triggerLabel, text, delay, disabled = false, disabledReason } = props;
  if (
    Object.keys(props).some((k) => !keys.includes(k)) ||
    typeof open !== 'boolean' ||
    typeof onOpenChange !== 'function' ||
    typeof triggerLabel !== 'string' ||
    !triggerLabel.trim() ||
    typeof text !== 'string' ||
    !text.trim() ||
    text.length > 512 ||
    typeof disabled !== 'boolean' ||
    (disabledReason !== undefined &&
      (typeof disabledReason !== 'string' || !disabledReason.trim())) ||
    (delay !== undefined && (!Number.isInteger(delay) || delay < 0 || delay > 60000))
  )
    invalid();
  const connected = (node: HTMLElement | null, portalled: boolean) => {
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
  return (
    <BaseTooltip.Provider delay={delay}>
      <BaseTooltip.Root
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
          <BaseTooltip.Trigger
            id={id}
            delay={delay}
            disabled={disabled}
            render={<button type="button" disabled={disabled} />}
            ref={(node: HTMLElement | null) => {
              trigger.current = node instanceof HTMLButtonElement ? node : null;
              assignRef(ref, trigger.current);
            }}
            aria-describedby={
              [
                disabledReason ? id + '-reason' : '',
                open && !disabled && hasPopup && targetConnected() && connected(popup.current, true)
                  ? id + '-tooltip'
                  : '',
              ]
                .filter(Boolean)
                .join(' ') || undefined
            }
            className="pn-r-button pn-r-secondary"
          >
            {triggerLabel}
          </BaseTooltip.Trigger>
          {disabledReason && (
            <span id={id + '-reason'} className="pn-r-reason">
              {disabledReason}
            </span>
          )}
        </span>
        <ScopedPortal kind="tooltip">
          <BaseTooltip.Positioner side="top" align="center" className="pn-r-overlay-positioner">
            <BaseTooltip.Popup
              role="tooltip"
              id={id + '-tooltip'}
              ref={popupRef}
              className="pn-r-tooltip"
            >
              {text}
            </BaseTooltip.Popup>
          </BaseTooltip.Positioner>
        </ScopedPortal>
      </BaseTooltip.Root>
    </BaseTooltip.Provider>
  );
});
