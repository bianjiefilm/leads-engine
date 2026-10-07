'use client';
import React, { forwardRef } from 'react';
import { Check } from 'lucide-react';
import { Checkbox as BaseCheckbox } from '@base-ui/react/checkbox';
import { type FieldProps, FieldFrame, useField, validateField, invalid } from './internal/Field';
export type CheckboxProps = FieldProps & {
  checked: boolean;
  indeterminate?: boolean;
  onCheckedChange: (checked: boolean, details: BaseCheckbox.Root.ChangeEventDetails) => void;
  name?: string;
  form?: string;
  value?: string;
  inputRef?: React.Ref<HTMLInputElement>;
};
export const Checkbox = forwardRef<HTMLElement, CheckboxProps>(function Checkbox(
  { checked, indeterminate = false, onCheckedChange, name, form, value, inputRef, ...props },
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
  if (
    typeof checked !== 'boolean' ||
    typeof onCheckedChange !== 'function' ||
    typeof indeterminate !== 'boolean'
  )
    invalid();
  return (
    <FieldFrame field={field} meta={meta}>
      <BaseCheckbox.Root
        ref={ref}
        id={meta.id}
        checked={checked}
        indeterminate={indeterminate}
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
        className="pn-r-boolean pn-r-checkbox"
      >
        <BaseCheckbox.Indicator className="pn-r-boolean-indicator" aria-hidden="true">
          <Check aria-hidden="true" focusable="false" />
        </BaseCheckbox.Indicator>
      </BaseCheckbox.Root>
    </FieldFrame>
  );
});
