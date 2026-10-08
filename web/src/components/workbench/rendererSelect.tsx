"use client";

import React, { useState } from "react";
import { Select, type Choice } from "@/vendor/painuo/react/v1/src/index";

// HUI-2626 finish-r1：renderer Select 的受控适配器。
// vendored Select 要求 open/onOpenChange 受控；业务页只需要 value/onChange，
// 这里内管 open 状态，保持行为契约（Esc/外点关闭由 vendored 负责）。

export function RendererSelect({
  label,
  value,
  onValueChange,
  choices,
  disabled,
  disabledReason,
  id,
  help,
  size,
}: {
  label: string;
  value: string;
  onValueChange: (value: string) => void;
  choices: readonly Choice[];
  disabled?: boolean;
  disabledReason?: string;
  id?: string;
  help?: string;
  size?: "sm" | "md" | "lg";
}) {
  const [open, setOpen] = useState(false);
  const known = choices.some((choice) => choice.value === value);
  const safeValue = known ? value : null;
  return (
    <Select
      label={label}
      id={id}
      help={help}
      size={size}
      disabled={disabled}
      disabledReason={disabledReason}
      items={choices}
      value={safeValue}
      open={open}
      onValueChange={(next) => {
        setOpen(false);
        if (typeof next === "string") onValueChange(next);
      }}
      onOpenChange={(next) => setOpen(next)}
    />
  );
}
