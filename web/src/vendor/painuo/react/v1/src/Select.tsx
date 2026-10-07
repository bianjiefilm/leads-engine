'use client';
import React, { forwardRef } from 'react';
import { Select as BaseSelect } from '@base-ui/react/select';
import { ChevronDown, Check } from 'lucide-react';
import {
  type FieldProps,
  type Choice,
  FieldFrame,
  useField,
  validateField,
  validateChoices,
  invalid,
} from './internal/Field';
import { ScopedPortal, useScopedPortalTargetConnected } from './internal/ScopedPortal';
export type SelectProps = FieldProps & {
  items: readonly Choice[];
  value: string | null;
  open: boolean;
  onValueChange: (value: string | null, details: BaseSelect.Root.ChangeEventDetails) => void;
  onOpenChange: (open: boolean, details: BaseSelect.Root.ChangeEventDetails) => void;
  name?: string;
  form?: string;
  autoComplete?: string;
  inputRef?: React.Ref<HTMLInputElement>;
  placeholder?: string;
};
export const Select = forwardRef<HTMLButtonElement, SelectProps>(function Select(
  {
    items,
    value,
    open,
    onValueChange,
    onOpenChange,
    name,
    form,
    autoComplete,
    inputRef,
    placeholder = '请选择',
    ...props
  },
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
    required,
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
    required,
    className,
    'aria-describedby': described,
  };
  const meta = useField(field);
  const targetConnected = useScopedPortalTargetConnected();
  validateField(field, extra);
  if (Object.hasOwn(extra, 'defaultValue')) invalid();
  validateChoices(items, value);
  if (
    typeof open !== 'boolean' ||
    typeof onValueChange !== 'function' ||
    typeof onOpenChange !== 'function' ||
    typeof placeholder !== 'string'
  )
    invalid();
  const active = () => !disabled && !readOnly && targetConnected();
  const reasonIndexes = new Map(items.map((x, i) => [x.value, i]));
  const row = (x: Choice) => (
    <BaseSelect.Item
      key={x.value}
      value={x.value}
      disabled={x.disabled}
      aria-describedby={
        x.disabledReason ? meta.id + '-item-reason-' + reasonIndexes.get(x.value) : undefined
      }
      className="pn-r-select-item"
    >
      <BaseSelect.ItemText>{x.label}</BaseSelect.ItemText>
      <BaseSelect.ItemIndicator>
        <Check aria-hidden="true" />
      </BaseSelect.ItemIndicator>
      {x.disabledReason && (
        <span className="pn-r-reason" id={meta.id + '-item-reason-' + reasonIndexes.get(x.value)}>
          {x.disabledReason}
        </span>
      )}
    </BaseSelect.Item>
  );
  const groups = new Set(items.flatMap((x) => (x.group === undefined ? [] : [x.group])));
  return (
    <FieldFrame field={field} meta={meta}>
      <BaseSelect.Root<string>
        id={meta.id}
        value={value}
        open={open}
        items={items}
        onValueChange={(next, details) => {
          if (active()) onValueChange(next, details);
          else details.cancel();
        }}
        onOpenChange={(next, details) => {
          if (active()) onOpenChange(next, details);
          else details.cancel();
        }}
        name={name}
        form={form}
        autoComplete={autoComplete}
        inputRef={inputRef}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
      >
        <BaseSelect.Trigger
          ref={ref}
          aria-labelledby={meta.labelId}
          aria-describedby={meta.descriptions}
          className="pn-r-input pn-r-select-trigger"
        >
          <BaseSelect.Value>
            {value === null ? placeholder : items.find((x) => x.value === value)!.label}
          </BaseSelect.Value>
          <BaseSelect.Icon>
            <ChevronDown aria-hidden="true" />
          </BaseSelect.Icon>
        </BaseSelect.Trigger>
        <ScopedPortal kind="select">
          <BaseSelect.Positioner alignItemWithTrigger={false} className="pn-r-select-positioner">
            <BaseSelect.Popup className="pn-r-select-popup">
              {items.filter((x) => x.group === undefined).map(row)}
              {[...groups].map((group) => (
                <BaseSelect.Group key={group}>
                  <BaseSelect.GroupLabel className="pn-r-select-group-label">
                    {group}
                  </BaseSelect.GroupLabel>
                  {items.filter((x) => x.group === group).map(row)}
                </BaseSelect.Group>
              ))}
            </BaseSelect.Popup>
          </BaseSelect.Positioner>
        </ScopedPortal>
      </BaseSelect.Root>
    </FieldFrame>
  );
});
