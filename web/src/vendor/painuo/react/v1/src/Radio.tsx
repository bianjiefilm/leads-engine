'use client';
import React, { createContext, forwardRef, useContext } from 'react';
import { Circle } from 'lucide-react';
import { Radio as BaseRadio } from '@base-ui/react/radio';
import { RadioGroup as BaseRadioGroup } from '@base-ui/react/radio-group';
import { type FieldProps, FieldFrame, useField, validateField, invalid } from './internal/Field';
const Values = createContext<ReadonlySet<string> | null>(null);
export type RadioProps = FieldProps & { value: string; inputRef?: React.Ref<HTMLInputElement> };
const RadioItem = forwardRef<HTMLElement, RadioProps>(function Radio(
  { value, inputRef, ...props },
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
  const values = useContext(Values);
  if (typeof value !== 'string' || !values?.has(value)) invalid();
  return (
    <FieldFrame field={field} meta={meta}>
      <BaseRadio.Root
        ref={ref}
        id={meta.id}
        value={value}
        inputRef={inputRef}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
        aria-labelledby={meta.labelId}
        aria-describedby={meta.descriptions}
        className="pn-r-boolean pn-r-radio"
      >
        <BaseRadio.Indicator aria-hidden="true" className="pn-r-boolean-indicator">
          <Circle aria-hidden="true" focusable="false" fill="currentColor" />
        </BaseRadio.Indicator>
      </BaseRadio.Root>
    </FieldFrame>
  );
});
export type RadioGroupProps = FieldProps & {
  value: string | null;
  onValueChange: (value: string | null, details: BaseRadioGroup.ChangeEventDetails) => void;
  name?: string;
  form?: string;
  children: React.ReactNode;
};
function radioValues(children: React.ReactNode, seen = new Set<string>()) {
  React.Children.forEach(children, (child) => {
    if (!React.isValidElement(child)) return;
    const props = child.props as { value?: unknown; children?: React.ReactNode };
    if (child.type === RadioItem) {
      if (typeof props.value !== 'string' || seen.has(props.value)) invalid();
      seen.add(props.value);
    } else if (child.type === React.Fragment || typeof child.type === 'string') {
      radioValues(props.children, seen);
    } else invalid();
  });
  return seen;
}
const Group = forwardRef<HTMLDivElement, RadioGroupProps>(function Group(
  { value, onValueChange, name, form, children, ...props },
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
  const values = radioValues(children);
  if (
    typeof onValueChange !== 'function' ||
    (typeof value !== 'string' && value !== null) ||
    (value !== null && !values.has(value))
  )
    invalid();
  return (
    <FieldFrame field={field} meta={meta}>
      <BaseRadioGroup<string | null>
        ref={ref}
        id={meta.id}
        value={value}
        onValueChange={onValueChange}
        name={name}
        form={form}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
        aria-labelledby={meta.labelId}
        aria-describedby={meta.descriptions}
        className="pn-r-radio-group"
      >
        <Values.Provider value={values}>{children}</Values.Provider>
      </BaseRadioGroup>
    </FieldFrame>
  );
});
export const Radio = Object.assign(RadioItem, { Group });
