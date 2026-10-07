# HUI-2621+2622 leads-engine 消费试点 —— G4–G8 差异报告（2026-10-07，续棒）

恢复点 e035b47（第一棒 G0–G3 + Root §14 裁决 r2：G3 零增量语义视为过）。本报告覆盖续棒 G4–G8；G9 停手。分支 `codex/20261006-hui-2621-2622-leads-pilot`，base 钉 `687f9d0`，producer 钉 `public-ai@01e6765`（Root r1 回退钉，续棒前 fetch 复核：leads origin/main 零前进；producer origin/main 前进但 renderer src/contract 面在 01e6765..origin/main 有 6 个提交 ⇒ 维持回退钉，与 G2/G3 时点事实一致）。

## 1. 逐门结果

| 门 | 判据 | 结果 | 关键证据 |
|---|---|---|---|
| G4 | 身份 40/40；`npm ci` 成功 | **PASS**（commit 8991812） | vendor.py --check pre=missing → stage-out（仓外 /private/tmp，工具拒绝 git 仓属性实证）→ 拷入 → post --check ok:true；独立 sha256 表 40/40（g4/vendor-identity-40.txt）；identity vitest 5/5；npm install+ci exit0，锁 sha d65296ce… |
| G5 | importer+provider+Status+映射；vitest 全量（含既有） | **PASS**（commit 2d70591） | 28 文件 / 187 测试全过 exit0（g5/vitest-full.stdout.txt） |
| G6 | `next build` exit0 无 hydration/类型错误 | **PASS**（commit 169e699） | exit0，33s，16/16 页预渲染，/attribution 35.9kB（g6/） |
| G7 | diff 只含 §9 owned；逐 commit 显式 add | **PASS**（commit b7fd59a） | 100 变更路径 0 越界（g7/scope-verdict.txt）；g7/per-commit-audit.txt |
| G8 | 差异报告+证据 JSON+SHA256 在位自洽 | **PASS**（本提交） | 本文件 + evidence.json + SHA256SUMS.txt（全目录重算） |

## 2. RED→GREEN 七项对照（RED=fef9f01，12 败/1 过）

| # | RED 失败形态（实测 @fef9f01） | GREEN 形态（本棒实测） |
|---|---|---|
| R1 锁存在+pin+字节 | lock ENOENT ×5 | 5/5 过：schema painuo-token-lock/v1、sourceCommit=01e6765、7 owned 文件 sha256 逐一相符、8 文件在盘、draft 诚实 |
| R2 importer 两行+alias 休眠 | tokens.css/styles.css 两 import 缺失 ×2 败；alias 休眠守卫空真 1 过（预期） | 3/3 过：layout.tsx 两 import 在位；全 src 树无 aliases.css/scss 引用（休眠守卫） |
| R3 身份 40/40 | vendored 目录缺失 | 5/5 过（G4 内先行验证）：锁 pin commit/tree/manifestSha、40/40 destination==source 字节一致、contractHashes 4 项、host 矩阵 19.1.1/1.8.0/1.44.0、token 块逐字段相符 |
| R4 SSR 行为 | import 失败（模块不存在，0 test 收集） | 3/3 过：renderToString 含 pn-r-scope + data-pn-profile/surface/theme + pn-r-status + data-pn-status="failed" + badge-danger + 中文 label；非法 state 降级 unknown/neutral 不抛错；无 provider 抛 `renderer provider required` |
| R5 负控 | 同上 | 2/2 过：portal.brand/light 组合抛 `renderer scope invalid`；work.light/dark 登记组合可渲染（本切片不启用 dark） |
| R6 roiStatus 映射 | 模块不存在 | 4/4 过：complete→resultReady、incomplete→needsAttention、source_stats_only→waiting、未知/空→queued（DEGRADED_STATE，永不成功色） |
| R7 篡改探针 | —（随 R1 ENOENT） | 过：tokens.css 改 1 字节 → sha256 失配 |

**基建修正（如实披露）**：RED 提交的 `painuoRenderer.status.test.tsx` 在 RED 时点因 vendor 模块不存在而 0 收集，掩盖了其 JSX 需 classic runtime（vitest esbuild + tsconfig `jsx: preserve` ⇒ `React.createElement`）的基建缺陷。G5 首跑暴露「React is not defined」5 败（复现证据 g5/vitest-status-prerefix.\*，用 fef9f01 原文件对现实现重跑）；修正 = 加 1 行 `import React from "react"`，**断言与 fef9f01 逐字节一致**（测试文件属 §9 owned 集；非「顺手改测试」，Root r1 第 3 条针对的是既有测试因 bump 失败，不适用本情形）。

## 3. raw-hex 基线 × 本切片面

基线（`painuo-style-baseline.v1.json`，锚 b58ea173，**维持不动**）：**71 文件 / 52 记录**。attribution/page.tsx 本身 **0 条** raw-hex 记录（其颜色全部来自 globals.css 类）。本切片唯一新增「颜色面」= vendored `Status` 徽章：其 styles.css 185 处 `var(--pn-*)` 引用、零裸 hex，badge 色（`--pn-status-{danger,success,warning,info}-*`/`--pn-bg-sunken`）全部由 tokens.css 在 provider 子树（`data-pn-surface="work.light" data-pn-theme="light"`）内解析——**不向页外渗漏一行颜色**。既有 `STATUS_LABELS` 文案逐字未动；徽章为加法式追加。

### legacy 五变量 vs work.light/light `--pn-*` 对应值（alias 激活后的翻转量，现状未翻转）

| legacy（globals.css `:root`，现状生效） | work.light/light 对应 `--pn-*` | 翻转后值 | 视觉后果 |
|---|---|---|---|
| `--bg: #f7f8fa` | `--pn-bg-page` | `#f7f8f5` | 近白微暖移，低感知 |
| `--fg: #1c2430` | `--pn-text-primary` | `#192421` | 近黑微绿移，低感知 |
| `--muted: #667085` | `--pn-text-muted` | `#52615b` | 中灰→墨绿灰，中感知 |
| `--accent: #2563eb`（蓝） | `--pn-brand-accent` | `#6cebdc`（薄荷绿） | **全站主色翻转，高感知** |
| `--line: #e4e7ec` | `--pn-border-default` | `#dce2da` | 浅灰→绿灰，低感知 |

`var(--accent)` 现役使用点（alias 激活的 contrast 风险面）：globals.css 8 处（L66-67 链接 border+文字、L142/L172/L295 背景、L296/L410-411 border+文字）+ eco-top-nav.module.css 2 处（L41 内嵌下划线、L76 border）。**文字与填充混用同一变量**是合同明文警告点。

## 4. alias 两条激活路径（留给 2626 决策，本切片零激活）

| 路径 | 机制 | 代价 | contrast 前置迁移（激活前必须完成） |
|---|---|---|---|
| ① body 级全站翻转 | root layout（或 shell layout）给 `<body>` 挂 `data-pn-surface="work.light" data-pn-theme="light"` | 一次性、全站生效；tokens.css 选择器即 `[data-pn-surface]…[data-pn-theme]`，无需改 alias 文件 | 全站蓝→薄荷一次性翻转；`--accent` 混用点全部先迁角色：链接文字 → `--pn-brand-text`（#08675c，深青，白底可读）、按钮/深底文字 → `--pn-brand-ink`（#10332e）、边框 → `--pn-border-default`；10 处 `var(--accent)` 逐一改写后 alias 才可激活 |
| ② RendererProvider 子树逐页激活 | provider 自带 data 属性，alias 在其子树内自动生效（本切片 attribution 页已具备该子树，但 alias.css 未 import ⇒ 未生效） | 逐页迁移、可灰度；但同页会出现「provider 子树内薄荷、子树外蓝」的双主色过渡态 | 只需迁移被激活页面的 `--accent` 用法；attribution 页自身 0 处 `var(--accent)`（颜色走 globals 类），故页内无前置迁移，但导航/shell 共享件若进入子树仍受全局翻转影响需先审计 |

合同既定事实：alias 文件 `aliasesDeprecatedUntil.v1FirstReleasedAt = null`（draft 不计时）；两文件已落盘、未 import（R2 守卫钉住）。

## 5. 继承债（§14 去向阳面，本切片不修）

18 条 lint delta findings 全部位于 `web/src/app/globals.css`，集合在 base 钉 687f9d0 与本分支 head 逐字节相等（g3/lint-delta.{basepin,head}.\*.json）⇒ 本切片新增债 = 0。**2626（6d68f19）与 2598-today（03bd4a8）在 368eaa0..687f9d0 窗口向 globals.css 引入 305 行含裸 hex，为该 18 条的引入方**；2621 线收口时应在 Linear 指名其清偿义务。本切片 owned 路径内不可修复（globals.css 不碰 + lint-allowlist.json 为 sync 锁定产物）。

## 6. PARTIAL 边界（诚实状态）

- tokens：artifact+lock+importer+lint 门已完成；**alias 休眠、全站视觉零变化**（结构性证明：无 body data 属性 ⇒ tokens 选择器不匹配 ⇒ 零规则激活）；v1.0.0 未发布；contrast runtimeReviewRequired 36 项原样保留。
- renderer：仅 `Status` 一原语在 attribution 一条真实路由生效（Next 15.5.25 生产构建 + node SSR 测试）；2622 四消费者门仍 PARTIAL；**无真实浏览器键盘/a11y/截图证据**（合同保留给 Root 单独授权的 runtime gate）；暗色未启用（work.light/dark 仅验证可渲染）。
- 单 Status 原语接线可被 2626 journey 迁移取代/重写，属预期。

## 7. 提交清单（base 687f9d0 → head）

| commit | 内容 |
|---|---|
| fef9f01 | RED 7 项（第一棒） |
| 0540a7a | T 面 sync --apply 8 文件 + 锁 pin 01e6765（第一棒） |
| e035b47 | G3 停门证据（第一棒） |
| 8991812 | G4 vendor 40 文件 + v1.lock.json + deps 精确钉 + npm ci |
| 2d70591 | G5 importer + provider + Status + painuoStatus 映射，vitest 187 全绿 |
| 169e699 | G6 next build exit0 证据 |
| b7fd59a | G7 范围审计证据 |
| （本提交） | G8 差异报告 + evidence.json 台账 + SHA256SUMS |
