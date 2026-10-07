import type { Metadata } from "next";
import "./globals.css";
// HUI-2621 T 面（leads-web profile）：semantic tokens 生成物 + vendored renderer 样式。
// importer 锚点合同：只此文件引 tokens.css / renderer styles.css；aliases.css 落盘不 import（休眠，2626 决策）。
import "../styles/generated/painuo/v1/tokens.css";
import "../vendor/painuo/react/v1/src/styles.css";

export const metadata: Metadata = {
  title: "数海获客 · leads-engine",
  description: "独立商家 CRM(L0 底座):联系人 / 线索 / 商机",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
