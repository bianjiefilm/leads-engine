"use client";

import React, { useEffect, useRef, type ReactNode } from "react";
import { Drawer } from "@/vendor/painuo/react/v1/src/index";
import { drawerSide } from "@/lib/finish";

// HUI-2626 finish-r1（DECISIONS.md D2-3）：列表页详情抽屉。
// 桌面右侧 / 窄屏底部；打开时焦点进抽屉，关闭后焦点返回触发行（finalFocus）。
// vendored Drawer 标题行自带关闭按钮（busy 时禁用），children 不再重复放关闭；
// 无 provider 时由 vendored 内部契约抛错（与 Status 等原语一致）。

export function DetailDrawer({
  open,
  onClose,
  title,
  description,
  busy,
  width,
  children,
  footer,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  busy?: boolean;
  width: number;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const triggerRef = useRef<HTMLElement | null>(null);

  // 焦点返回：打开时记住当前活动元素（列表行/主按钮），关闭后 renderer finalFocus 接管。
  useEffect(() => {
    if (open && document.activeElement instanceof HTMLElement) {
      triggerRef.current = document.activeElement;
    }
  }, [open]);

  if (!open) return null;
  const finalFocus = { current: triggerRef.current };
  return (
    <Drawer
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
      title={title}
      description={description}
      busy={busy === true}
      side={drawerSide(width)}
      finalFocus={finalFocus}
    >
      <div data-detail-drawer="body">{children}</div>
      {footer ? <div data-detail-drawer="footer">{footer}</div> : null}
    </Drawer>
  );
}
