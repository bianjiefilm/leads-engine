"use client";

import { useEffect } from "react";

// HUI-2626 fix2（gate-r2 #8 offline 态）：注册离线兜底 service worker。
// 断网时的导航由 /sw.js 回应应用内离线页（public/offline.html，带重试入口），
// 不再渲染浏览器错误页。注册失败静默降级——在线行为完全不变。

export function OfflineReady() {
  useEffect(() => {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
    const register = () => {
      navigator.serviceWorker.register("/sw.js").catch(() => {});
    };
    if (document.readyState === "complete") {
      register();
      return;
    }
    window.addEventListener("load", register, { once: true });
    return () => window.removeEventListener("load", register);
  }, []);
  return null;
}
