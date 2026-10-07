'use client';
import React, { forwardRef, useCallback } from 'react';
import { Input as BaseInput } from '@base-ui/react/input';
import {
  type FieldProps,
  FieldFrame,
  useField,
  validateField,
  invalid,
  assignRef,
} from './internal/Field';
type Native = Omit<
  React.InputHTMLAttributes<HTMLInputElement>,
  keyof FieldProps | 'type' | 'style' | 'dangerouslySetInnerHTML' | 'size' | 'children'
>;
export type InputProps = Native &
  FieldProps & { type?: 'text' | 'search' | 'email' | 'password' | 'tel' | 'url' | 'number' };
export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
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
    type = 'text',
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
    !['text', 'search', 'email', 'password', 'tel', 'url', 'number'].includes(type) ||
    (native.value !== undefined && native.defaultValue !== undefined)
  )
    invalid();
  const realRef = useCallback(
    (el: HTMLElement | null) => {
      if (el && el.tagName !== 'INPUT') invalid();
      assignRef(ref, el as HTMLInputElement | null);
    },
    [ref],
  );
  return (
    <FieldFrame field={field} meta={meta}>
      <BaseInput
        {...native}
        ref={realRef}
        id={meta.id}
        type={type}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
        aria-describedby={meta.descriptions}
        aria-invalid={error ? true : undefined}
        className="pn-r-input"
      />
    </FieldFrame>
  );
});
