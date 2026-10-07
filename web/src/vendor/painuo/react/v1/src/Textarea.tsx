'use client';
import React, { forwardRef } from 'react';
import { type FieldProps, FieldFrame, useField, validateField, invalid } from './internal/Field';
type Native = Omit<
  React.TextareaHTMLAttributes<HTMLTextAreaElement>,
  keyof FieldProps | 'style' | 'dangerouslySetInnerHTML' | 'children'
>;
export type TextareaProps = Native & FieldProps;
export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  {
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
    ...native
  },
  ref,
) {
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
  validateField(field, native);
  if (
    (native.value !== undefined && native.defaultValue !== undefined) ||
    (native.rows !== undefined && (!Number.isInteger(native.rows) || native.rows < 1))
  )
    invalid();
  return (
    <FieldFrame field={field} meta={meta}>
      <textarea
        {...native}
        ref={ref}
        id={meta.id}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
        aria-describedby={meta.descriptions}
        aria-invalid={error ? true : undefined}
        className="pn-r-input pn-r-textarea"
      />
    </FieldFrame>
  );
});
