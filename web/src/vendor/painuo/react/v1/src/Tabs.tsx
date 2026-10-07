'use client';
import React, { createContext, forwardRef, useContext } from 'react';
import { Tabs as BaseTabs } from '@base-ui/react/tabs';
import { useRendererScope } from './internal/ScopeContext';
import { invalid, validateField, type FieldProps, useField } from './internal/Field';
const Orientation = createContext<'horizontal' | 'vertical' | null>(null);
type Common = Pick<FieldProps, 'label' | 'id' | 'disabledReason' | 'className' | 'size'>;
export type TabsListProps = Common & {
  children: React.ReactNode;
  activation?: 'manual' | 'automatic';
  orientation?: 'horizontal' | 'vertical';
};
const List = forwardRef<HTMLDivElement, TabsListProps>(function List(
  { children, activation = 'manual', orientation, ...props },
  ref,
) {
  useRendererScope();
  const rootOrientation = useContext(Orientation);
  validateField(props, props as unknown as Record<string, unknown>);
  if (
    !rootOrientation ||
    !['manual', 'automatic'].includes(activation) ||
    (orientation !== undefined && orientation !== rootOrientation)
  )
    invalid();
  return (
    <BaseTabs.List
      ref={ref}
      aria-label={props.label}
      activateOnFocus={activation === 'automatic'}
      className={'pn-r-tabs-list' + (props.className ? ' ' + props.className : '')}
    >
      {children}
    </BaseTabs.List>
  );
});
export type TabsTabProps = Common & { value: string; disabled?: boolean };
const Tab = forwardRef<HTMLElement, TabsTabProps>(function Tab({ value, ...props }, ref) {
  const meta = useField(props);
  validateField(props, props as unknown as Record<string, unknown>);
  if (typeof value !== 'string') invalid();
  return (
    <span className="pn-r-control">
      <BaseTabs.Tab
        ref={ref}
        id={meta.id}
        value={value}
        disabled={props.disabled}
        aria-describedby={meta.descriptions}
        className={'pn-r-tab pn-r-size-' + meta.size}
      >
        {props.label}
      </BaseTabs.Tab>
      {props.disabledReason && (
        <span id={meta.id + '-reason'} className="pn-r-reason">
          {props.disabledReason}
        </span>
      )}
    </span>
  );
});
export type TabsPanelProps = { value: string; children: React.ReactNode; className?: string };
const Panel = forwardRef<HTMLDivElement, TabsPanelProps>(function Panel(
  { value, children, className, ...extra },
  ref,
) {
  useRendererScope();
  if (typeof value !== 'string' || Object.keys(extra).length) invalid();
  return (
    <BaseTabs.Panel
      ref={ref}
      value={value}
      className={'pn-r-tabs-panel' + (className ? ' ' + className : '')}
    >
      {children}
    </BaseTabs.Panel>
  );
});
export type TabsRootProps = {
  value: string | null;
  onValueChange: (value: string | null, details: BaseTabs.Root.ChangeEventDetails) => void;
  children: React.ReactNode;
  orientation?: 'horizontal' | 'vertical';
  className?: string;
};
function valuesIn(children: React.ReactNode, seen = new Set<string>(), panels = new Set<string>()) {
  React.Children.forEach(children, (child) => {
    if (!React.isValidElement(child)) return;
    const props = child.props as {
      value?: unknown;
      children?: React.ReactNode;
      activation?: unknown;
      label?: unknown;
    };
    if (child.type === List || child.type === Tab) {
      validateField(props as unknown as FieldProps, props as unknown as Record<string, unknown>);
      if (
        child.type === List &&
        props.activation !== undefined &&
        !['manual', 'automatic'].includes(props.activation as string)
      )
        invalid();
    }
    if (child.type === Tab || child.type === Panel) {
      const target = child.type === Tab ? seen : panels;
      if (typeof props.value !== 'string' || target.has(props.value)) invalid();
      target.add(props.value);
    } else if (
      child.type === List ||
      child.type === React.Fragment ||
      typeof child.type === 'string'
    )
      valuesIn(props.children, seen, panels);
    else invalid();
  });
  return { seen, panels };
}
const Root = forwardRef<HTMLDivElement, TabsRootProps>(function Root(
  { value, onValueChange, children, orientation = 'horizontal', className, ...extra },
  ref,
) {
  useRendererScope();
  if (
    Object.keys(extra).length ||
    typeof onValueChange !== 'function' ||
    !['horizontal', 'vertical'].includes(orientation) ||
    (typeof value !== 'string' && value !== null)
  )
    invalid();
  const { seen, panels } = valuesIn(children);
  if ((value !== null && !seen.has(value)) || [...panels].some((x) => !seen.has(x))) invalid();
  return (
    <Orientation.Provider value={orientation}>
      <BaseTabs.Root
        ref={ref}
        value={value}
        onValueChange={(next, details) => {
          if (next !== null && typeof next !== 'string') invalid();
          onValueChange(next, details);
        }}
        orientation={orientation}
        className={'pn-r-tabs' + (className ? ' ' + className : '')}
      >
        {children}
      </BaseTabs.Root>
    </Orientation.Provider>
  );
});
export const Tabs = { Root, List, Tab, Panel };
