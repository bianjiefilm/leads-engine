'use client';
import React, { forwardRef } from 'react';
import { ToggleGroup } from '@base-ui/react/toggle-group';
import { Toggle } from '@base-ui/react/toggle';
import {
  type FieldProps,
  type Choice,
  FieldFrame,
  useField,
  validateField,
  validateChoices,
  invalid,
} from './internal/Field';
export type SegmentedControlProps = FieldProps & {
  items: readonly Choice[];
  value: string | null;
  onValueChange: (value: string | null, details: ToggleGroup.ChangeEventDetails) => void;
  selectionRequired?: boolean;
  orientation?: 'horizontal' | 'vertical';
};
export const SegmentedControl = forwardRef<HTMLDivElement, SegmentedControlProps>(
  function SegmentedControl(
    { items, value, onValueChange, selectionRequired = true, orientation = 'horizontal', ...props },
    ref,
  ) {
    const {
      label,
      id,
      help,
      error,
      disabledReason,
      size,
      disabled,
      readOnly,
      className,
      'aria-describedby': described,
      ...extra
    } = props;
    const field = {
      label,
      id,
      help,
      error,
      disabledReason,
      size,
      disabled,
      readOnly,
      className,
      'aria-describedby': described,
    };
    const meta = useField(field);
    validateField(field, extra);
    validateChoices(items, value);
    if (
      typeof onValueChange !== 'function' ||
      typeof selectionRequired !== 'boolean' ||
      !['horizontal', 'vertical'].includes(orientation)
    )
      invalid();
    return (
      <FieldFrame field={field} meta={meta}>
        <ToggleGroup
          ref={ref}
          id={meta.id}
          aria-labelledby={meta.labelId}
          aria-describedby={meta.descriptions}
          multiple={false}
          value={value === null ? [] : [value]}
          disabled={disabled}
          orientation={orientation}
          className="pn-r-segmented"
          onValueChange={(next, details) => {
            if (disabled || readOnly) {
              details.cancel();
              return;
            }
            if (next.length === 0 && selectionRequired) {
              details.cancel();
              return;
            }
            onValueChange(next[0] ?? null, details);
          }}
        >
          {items.map((x, i) => (
            <span className="pn-r-control" key={x.value}>
              <Toggle
                value={x.value}
                disabled={x.disabled}
                aria-describedby={x.disabledReason ? meta.id + '-reason-' + i : undefined}
                className="pn-r-segment"
              >
                {x.label}
              </Toggle>
              {x.disabledReason && (
                <span className="pn-r-reason" id={meta.id + '-reason-' + i}>
                  {x.disabledReason}
                </span>
              )}
            </span>
          ))}
        </ToggleGroup>
      </FieldFrame>
    );
  },
);
