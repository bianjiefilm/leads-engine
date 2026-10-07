'use client';
import React, { useId } from 'react';
import { Field as BaseField } from '@base-ui/react/field';
import { useRendererScope } from './ScopeContext';
export interface FieldProps {
  label: string;
  id?: string;
  help?: string;
  error?: string;
  disabledReason?: string;
  size?: 'sm' | 'md' | 'lg';
  disabled?: boolean;
  readOnly?: boolean;
  required?: boolean;
  className?: string;
  'aria-describedby'?: string;
}
export function invalid(): never {
  throw new Error('renderer field props invalid');
}
export function validateField(props: FieldProps, extra: Record<string, unknown> = {}) {
  if (
    typeof props.label !== 'string' ||
    !props.label.trim() ||
    !['sm', 'md', 'lg'].includes(props.size ?? 'md')
  )
    invalid();
  for (const key of [
    'help',
    'error',
    'disabledReason',
    'id',
    'className',
    'aria-describedby',
  ] as const)
    if (props[key] !== undefined && typeof props[key] !== 'string') invalid();
  for (const key of ['disabled', 'readOnly', 'required'] as const)
    if (props[key] !== undefined && typeof props[key] !== 'boolean') invalid();
  for (const key of [
    'render',
    'style',
    'dangerouslySetInnerHTML',
    'container',
    'actionsRef',
    'handle',
    'defaultOpen',
    'defaultChecked',
    'nativeButton',
    'multiple',
    'onValueChange',
    'onCheckedChange',
  ])
    if (Object.hasOwn(extra, key)) invalid();
}
export function assignRef<T>(ref: React.ForwardedRef<T>, value: T | null) {
  if (typeof ref === 'function') ref(value);
  else if (ref) ref.current = value;
}
export function useField(props: FieldProps) {
  useRendererScope();
  const generated = useId();
  validateField(props);
  const id = props.id || generated;
  const labelId = id + '-label';
  const descriptions =
    [
      props['aria-describedby'],
      props.help ? id + '-help' : null,
      props.error ? id + '-error' : null,
      props.disabledReason ? id + '-reason' : null,
    ]
      .filter(Boolean)
      .join(' ') || undefined;
  return { id, labelId, descriptions, size: props.size ?? 'md' };
}
export function FieldFrame({
  field,
  meta,
  children,
}: {
  field: FieldProps;
  meta: ReturnType<typeof useField>;
  children: React.ReactNode;
}) {
  return (
    <BaseField.Root
      disabled={field.disabled}
      invalid={Boolean(field.error)}
      className={`pn-r-field pn-r-size-${meta.size}${field.className ? ' ' + field.className : ''}`}
    >
      <BaseField.Label id={meta.labelId} htmlFor={meta.id} className="pn-r-field-label">
        {field.label}
      </BaseField.Label>
      {children}
      {field.help && (
        <span id={meta.id + '-help'} className="pn-r-reason">
          {field.help}
        </span>
      )}
      {field.error && (
        <span id={meta.id + '-error'} className="pn-r-error">
          {field.error}
        </span>
      )}
      {field.disabledReason && (
        <span id={meta.id + '-reason'} className="pn-r-reason">
          {field.disabledReason}
        </span>
      )}
    </BaseField.Root>
  );
}
export interface Choice {
  value: string;
  label: string;
  disabled?: boolean;
  disabledReason?: string;
  group?: string;
}
export function validateChoices(items: readonly Choice[], value: string | null) {
  if (!Array.isArray(items) || (typeof value !== 'string' && value !== null)) invalid();
  const seen = new Set<string>();
  for (const x of items) {
    if (
      !x ||
      typeof x.value !== 'string' ||
      typeof x.label !== 'string' ||
      !x.label.trim() ||
      seen.has(x.value) ||
      (x.disabled !== undefined && typeof x.disabled !== 'boolean') ||
      (x.disabledReason !== undefined && typeof x.disabledReason !== 'string') ||
      (x.group !== undefined && typeof x.group !== 'string')
    )
      invalid();
    seen.add(x.value);
  }
  if (value !== null && !seen.has(value)) invalid();
}
