import type { Metadata } from "next";
// HUI-2621 T 面（leads-web profile）：semantic tokens 生成物 + vendored renderer 样式。
// importer 锚点合同：只此文件引 tokens.css / renderer styles.css / aliases.css。
// HUI-2626（DECISIONS.md D2-1）：激活 aliases.css——work.light 作用域下把
// --accent/--bg/--fg/--line/--muted 映射到 --pn-* token；业务层禁新增裸 hex。
import "../styles/generated/painuo/v1/tokens.css";
import "../styles/generated/painuo/v1/aliases.css";
import "../vendor/painuo/react/v1/src/styles.css";
// HUI-2626：globals 置于 vendored 样式之后——应用层等特异度覆盖（触控兜底、
// 抽屉视口让位顶栏）必须压过 vendored 默认值；tokens/aliases 锚点顺序不变。
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
