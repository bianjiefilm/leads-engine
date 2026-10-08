"use client";

import React from "react";

// HUI-2626 fix2：日期/时间选择是可访问标准件。vendored renderer Input 的 type
// 枚举不含日期类型（DECISIONS D2-12 豁免类），这里以原生日期控件 + token 化
// 样式统一收口——页面源不再手写裸 <input>，样式与 .stack-form 控件同族。

export function DateTimeField({
  label,
  value,
  onChange,
  type = "datetime-local",
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: "datetime-local" | "date";
}) {
  return (
    <label className="dt-field">
      <span>{label}</span>
      <input
        type={type}
        className="dt-input"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}
