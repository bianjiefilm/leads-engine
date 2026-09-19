import type { Metadata } from "next";
import "./globals.css";

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
