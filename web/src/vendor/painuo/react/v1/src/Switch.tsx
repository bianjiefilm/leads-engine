'use client';
import React, { forwardRef } from 'react';
import { Switch as BaseSwitch } from '@base-ui/react/switch';
import { type FieldProps, FieldFrame, useField, validateField, invalid } from './internal/Field';
export type SwitchProps = FieldProps & {
  checked: boolean;
  onCheckedChange: (checked: boolean, details: BaseSwitch.Root.ChangeEventDetails) => void;
  name?: string;
  form?: string;
  value?: string;
  inputRef?: React.Ref<HTMLInputElement>;
};
export const Switch = forwardRef<HTMLElement, SwitchProps>(function Switch(
  { checked, onCheckedChange, name, form, value, inputRef, ...props },
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
  validateField(field, extra);
  if (typeof checked !== 'boolean' || typeof onCheckedChange !== 'function') invalid();
  return (
    <FieldFrame field={field} meta={meta}>
      <BaseSwitch.Root
        ref={ref}
        id={meta.id}
        checked={checked}
        name={name}
        form={form}
        value={value}
        inputRef={inputRef}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
        onCheckedChange={onCheckedChange}
        aria-labelledby={meta.labelId}
        aria-describedby={meta.descriptions}
        className="pn-r-boolean pn-r-switch"
      >
        <BaseSwitch.Thumb className="pn-r-switch-thumb" aria-hidden="true" />
      </BaseSwitch.Root>
    </FieldFrame>
  );
});
