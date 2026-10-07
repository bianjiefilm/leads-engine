# HUI-2621+2622 leads-engine 消费试点 —— 非作者复核报告（2026-10-07）

- 复核者：非作者复核子代理（与执行子代理不同体；未参与任何实现提交）
- 对象：分支 `codex/20261006-hui-2621-2622-leads-pilot`，HEAD `ba4cd2f`（8 commit：fef9f01 RED → 0540a7a T面 → e035b47 G3停门 → 8991812 G4 → 2d70591 G5 → 169e699 G6 → b7fd59a G7 → ba4cd2f G8），base 钉 `687f9d0`，producer 钉 `public-ai@01e6765`
- 依据：设计 `dispatch-ready/2026-10-06-hui-2621-2622-pilot-design.md` §2/§4/§5/§9/§12/§13(r1)/§14(r2)；仓内审计目录 `docs/audits/2026-10-06-hui-2621-2622-leads-pilot/`
- 方法：两轴（Spec 试点设计 + Standards 仓惯例）逐条独立验证；所有关键数字均由复核者亲跑复现，不采信执行者自报
- 本文件为复核者唯一写入（untracked，留 Root 决定是否入档提交）；其余全部操作只读。共享 checkout（leads 368eaa0、public-ai b713ba7）与 producer worktree 全程零写入（复核尾部实测 0 dirty）

---

## 一、Spec 轴逐条

### S1. diff 范围 = §9 owned 集 —— PASS
- `git diff --name-only 687f9d0..HEAD` = **104 路径**（复核者以 §9 精确集独立判定）：0 越界。构成：generated 8 + vendor 41（40 文件 + v1.lock.json）+ 5 测试 + layout/attribution/package.json/package-lock.json/painuoStatus.ts + audit 目录 46 文件。
- 逐 commit（8 个）`git show --name-status` 复核：全部落在 owned 集内；evidence.json 跨门累加属 audit 目录内演化，合规。与 g7/scope-verdict.txt（100 路径口径 = 不含 ba4cd2f 自身 3 路径）一致。

### S2. layout.tsx 恰 +2 import —— PASS
- diff 仅 4 行：2 行注释 + `import "../styles/generated/painuo/v1/tokens.css";` + `import "../vendor/painuo/react/v1/src/styles.css";`。无其他改动；`<body>` 未挂任何 data 属性（L18 原样）。

### S3. alias 桥休眠 —— PASS（R2 守卫真实）
- 全 `web/src` grep：`aliases.css`/`aliases.scss` 零 import/零引用；命中仅为 layout.tsx 注释与 generated 目录自引用（lock/allowlist）。
- 全仓 `data-pn` 属性设置点：仅 vendor 组件内部；无任何页面/layout 主动激活 scope。
- 结构性零视觉变化成立：tokens.css 全部规则在 `[data-pn-surface=…][data-pn-theme=…]` 选择器下（8 个组合块），无属性匹配 ⇒ 无规则激活。
- R2 守卫测试（painuoTokens.importer.test.ts:32-40）真实有效：walk 全 src ts/tsx、正则匹配 `styles/generated/painuo/v1/aliases.(css|scss)` 子串（相对路径与 `@/` 别名形态均覆盖），RED 时点为「空真」、GREEN 后防漂移。复活守卫无豁免后门。

### S4. attribution 页接线 —— PASS
- 页级 `<RendererProvider profile="leads-web" surface="work.light" theme="light">` 包裹整个 return（profile-scopes.json leads-web 合法组合）。
- ROI 行**加法式追加** `<Status>`；`STATUS_LABELS` 文案逐字未动（`web/src/lib/roi.ts` 对 687f9d0 diff = 0 行）。
- `lib/painuoStatus.ts` 纯映射：complete→resultReady、incomplete→needsAttention、source_stats_only→waiting、未知→queued（DEGRADED_STATE，永不成功色）；无 IO、无 React。`Status` 渲染 `<span>`（`<p>` 内合法），闭枚举降级 unknown/neutral，颜色不单独立义（文字+图标承载）。

### S5. vendor 40 文件身份 —— PASS（超额：全量复核）
- 复核者以 producer worktree（`01e6765`，实测 HEAD 相符、0 dirty）`renderers/react/v1/contract/source-manifest.json` 为基准，对消费树 **全部 40 文件**（超出抽 5 要求）独立 sha256：40/40 与 manifest 及消费侧字节三方一致，0 失配。
- 锁 pin 链自洽：manifest 文件 sha256 `15bcaaca…` == v1.lock.json.manifestSha256；`git rev-parse 01e6765^{tree}` == 锁.producerTree `f5370120…`；`host.lockSha256 d65296ce…` == 现盘 package-lock.json 实测 sha256。
- v1.lock.json 通过 producer 自带 `renderer-lock.schema.json` 结构校验（required 全在、无 additionalProperties 违例）。

### S6. generated tokens 落盘 —— PASS
- 7 个 owned 产物逐一对照 producer 源（顶层 generated 5 文件 + profiles/leads-web aliases 2 文件）：producer==consumer==lock.files 三方一致，0 失配。
- painuo-tokens.lock.json：schema `painuo-token-lock/v1`、sourceCommit `01e6765…`（pin=01e6765 ✔）、consumerId `leads-web`、work.light/light、`aliasesDeprecatedUntil.v1FirstReleasedAt=null`（draft 诚实）。

### S7. deps 与锁 —— PASS
- package.json：`@base-ui/react 1.8.0`、`lucide-react 1.44.0` 精确钉（无 ^）；react/react-dom `19.0.0→19.1.1`（Root r1 第 3 条明示批准）；`next ^15.5.25` 未动。
- package-lock.json lockfileVersion 3，五关键包版本/registry resolved 与 package.json 一致；`npm ci` exit0（ci 对 json/lock 失配会硬失败，构成一致性硬证明）；ci 后 lock sha 不变（d65296ce…）。

### S8. G3 停门与 §14 零增量语义 —— PASS（于其判据时点；见 F-P1-1 保留项）
- 复核者独立复算：`lint-delta.basepin-687f9d0.json` 与 `lint-delta.head-0540a7a.json` findings 集合（18==18，全在 `web/src/app/globals.css`）排序规范化后**完全相等**；rehearsal @368eaa0 = pass/0 条。§14 裁决所依据的事实全部复现。
- lint-lock verdict=pass（files=7）、baseline 71 文件/52 记录（=设计演练值；记录构成 literal 22 / token 28 / dynamic 2，分布 3 文件，attribution/page.tsx 0 条）均亲跑复现。
- 18 条继承债引入方（03bd4a8 / 6d68f19）的指名与去向阳面表述与 §14 一致。

### S9. G5 基建修正披露核真 —— PASS（披露属实）
- `git diff fef9f01..HEAD -- web/tests/painuoRenderer.status.test.tsx` = **恰 1 行**（`import React from "react"` + 行内注释），断言零改动——与披露逐字节一致。
- RED 日志（red/painuo-tests-red.stdout.log）：12 败/1 过，失败形态为 ENOENT/模块不存在/断言（设计 §5 认可形态；R4/R6 的 RED 形态设计表已预写「import 失败（模块不存在）」）。
- 复现证据 g5/vitest-status-prerefix.*：用 fef9f01 原文件对现实现重跑 5×「React is not defined」，与披露机制吻合（vitest esbuild + tsconfig `jsx: preserve` ⇒ classic runtime）。该修正是测试基建缺陷而非「顺手改测试」，且测试文件本身属 §9 owned 集；Root r1 第 3 条（针对既有测试因 bump 失败）确实不适用。披露诚实。

### S10. 证据台账 —— PASS（一处文牍矛盾降级为 P2，见 F-P2-1）
- SHA256SUMS.txt 46/46 条目 `shasum -c` 全 OK。
- evidence.json 命令台账、pins（leads 零漂移；producer 按 r1 规则回退钉 01e6765，renderer 子树 01e6765..origin/main 6 提交变更、generated 子树零变更）、resumeLeg2、g3Verdict 结构完整。工作树/红线声明与复核实测相符。

---

## 二、Standards 轴逐条

- **测试风格**：vitest node env、无 jsdom（vitest.config.ts 未动，`tests/**/*.test.tsx` include 为既有）；describe/it/expect、`node:` 前缀内建模块、`resolve(__dirname, …)` 与仓内既有测试（pageCensus/workbench/shell 等）同风格。R7 篡改探针在 tmpdir 进行并 finally 清理，不触碰真实产物。PASS。
- **命名**：`painuo*.test.ts`、`painuoStatus.ts` 驼峰与既有 `pageCensus/intentGrade` 一致。PASS。
- **注释密度**：新代码注释带 HUI 工单号、说明「为什么」（锚点合同、休眠决策、降级语义），与仓内 `// HUI-1696 …` 惯例一致，无冗余注释。PASS。
- **凭据/密钥**：新增非 vendor 文件零命中；vendor 内唯一 "password" 命中为 Input.tsx 的 input type 枚举（producer 源，非凭据）。PASS。
- **debug 残留**：新增非 vendor 文件零 `console.*`/`debugger`/`.only`/TODO/FIXME。PASS。
- **package.json/lock 一致**：npm ci 硬校验 exit0（见 S7）。PASS。
- **红线**：复核全程零写入共享 checkout 与 producer worktree（尾部实测 0 dirty）；无 push/PR/Linear 动作；无 provider 付费调用。PASS。

---

## 三、Findings

### F-P1-1（P1）：lint delta 门在最终 HEAD 实为 71 findings（含 G4 引入的 53 条新记录），§14 零增量证明仅覆盖 T 面 head，G8 报告「本切片新增债 = 0」在报告时点的分支顶不成立 —— 需 Root 知情裁决，非代码可修

- **复核者实测**（producer lint.py，--base b58ea173 强制锚点，--baseline 用仓内 painuo-style-baseline.v1.json）：
  - `--head 0540a7a`（G3 判据时点）：exit1，18 findings 全在 globals.css —— 复现执行者 g3 证据。
  - `--head ba4cd2f`（**最终 HEAD**）：exit1，**71 findings** = 18（globals.css，集合与 basepin 逐字节相等，§14 已裁决继承）+ **53 条新记录全在 `web/src/vendor/painuo/react/v1/src/styles.css`**。
  - 二分定位：53 条自 **8991812（G4 vendor 落盘）** 出现，2d70591 起持续存在。
- **53 条构成**：49 × kind=token（即 `var(--pn-*)` 引用记录——恰是合同期望的迁移形态，但按门语义属「基线外新记录」）+ 3 × dynamic/unrecognized_color_value + 1 × dynamic/unproved_color_expression（均为 producer 侧源码自带）。
- **根因结构**：leads-web 策略 `selectionPrefix=null` ⇒ lint.py `_source_files` 扫描 generatedRoot 之外的**全仓**可扫文件 ⇒ vendored renderer 源落入扫描范围；producer 仓内该文件不在任何 consumer 面内、从不被扫（合同对「vendored producer 源在消费侧 delta 门中的地位」存在空白，本试点是首个 vendor 方，无人预见）。
- **为何不是执行者违规**：§14 执行指令明文「恢复棒从 G4 继续…G3 视为已过；G4 起每门照旧」，G4–G8 门定义（§7）不含 delta 重跑；§14 判据书写时「head」即 0540a7a。执行者照令行事。且 diff-report §3 已如实披露「本切片唯一新增颜色面 = vendored styles.css，185 处 var(--pn-*)、零裸 hex」——缺的是**最终 HEAD 的门判定数字**，非颜色面本身。
- **为何非代码可修**：vendored 目录逐字节锁死（动 1 字节即身份测试红）；49/53 为 token 引用形态，恰是采用目标本身；4 条 dynamic 在 producer 源内。与 globals.css 18 条同属「owned 路径内不可修复」结构。
- **影响面**：不影响 vendor 身份、双锁、187 测试、next build（全部独立复核绿）；影响的是「债务只减不增」主张在最终态的完整性与 §14 判据的严格读法。
- **建议处置**：Root 出 r3 式注记（或并入 2621 收口 Linear 评论）：将 53 条与 18 条一并去向阳面指名（53 条责任归属 = 合同 vendor 扫描空白 + producer 源自带，非 leads 页面债）；或明示「vendored producer 源在消费侧 delta 门中的地位」的合同澄清。**在 Root 知情前不建议静默合并。**

### F-P2-1（P2）：evidence.json 台账文牍矛盾 —— G4-vendor-stageout-resolved 槽位 note 与自录输出相悖
- 该槽位记录 `exit: 1`、stdoutSha256 `fe519c49…`（对应实盘文件 `{"ok": false, "reason": "path"}`），但 note 写「exit0 ok:true」。重构事实：直接对 producer 根的两次 stage-out（/tmp 与 /private/tmp）**均**败于 path 规则；成功来自 manifest-pruned git-archive export 路线（G4-vendor-check-export + G4-vendor-stageout-ok 两槽位，输出 `{"ok":true}` sha `55f66c2c…`）。diff-report §1 G4 格「stage-out（仓外 /private/tmp…）→ 拷入」省略了 export 步。原始证据文件完好、可重构真序列；仅台账叙述失真。建议 Root 侧修正或加勘误注。

### F-P2-2（P2）：`lib/painuoStatus.ts` 的 `typeof roiStatus === "string"` 在 `(roiStatus: string)` 签名下为死代码
- 无害的运行时加固；若真要防 BFF 脏数据，应把参数放宽为 `unknown`（语义更诚实）。二选一即可，不阻塞。

### F-P2-3（P2）：`painuoTokens.importer.test.ts:13` 的 `${"vendor"}${"/"}painuo` 模板串拼接无作用
- 直接写 `"vendor/painuo"` 即可；现写法降低可读性（疑似防自匹配的过度设计，而该 walk 只扫 src 不扫 tests，无自匹配可能）。纯风格 nit。

（无 P0。）

---

## 四、独立复跑台账（复核者亲跑，2026-10-07）

环境：Node v22.20.0 / npm 10.9.3 / Python 3.9.6 / git 2.54.0（与执行者台账一致）。

| # | 命令（web/ 或 producer worktree） | 结果 |
|---|---|---|
| 1 | `npm ci --no-audit --no-fund` | exit0，82 packages / 22s；lock sha 前后不变 `d65296ce…` |
| 2 | `npm run test`（vitest run 全量；ci 前后各跑一次） | **exit0，28 files / 187 tests 全过**（与执行者自报一致） |
| 3 | `npm run build`（next build 生产构建） | **exit0**；20 路由，`/attribution` 35.9 kB / FirstLoad 146 kB；stderr 仅既有 multi-lockfile outputFileTracingRoot 警告（环境性：上级目录存在无关 lockfile，非本切片引入） |
| 4 | `lint.py lock --repo-root <leads> --lock-path …/painuo-tokens.lock.json` | exit0 verdict=pass files=7 head=ba4cd2f lockHash `68d37830…` |
| 5 | `lint.py baseline --profile leads-web --base b58ea173…` | exit0；**71 文件 / 52 记录**（=设计演练值） |
| 6 | `lint.py delta --head 0540a7a` | exit1；18 findings 全 globals.css（复现 g3；集合==basepin） |
| 7 | `lint.py delta --head ba4cd2f` | exit1；**71 findings**（18 继承 + **53 新**，见 F-P1-1） |
| 8 | `lint.py delta --head 8991812 / 2d70591` | 均含 53 条 vendor 新记录（F-P1-1 二分） |
| 9 | vendor 40 文件独立 sha256 对 producer `01e6765` source-manifest | **40/40 一致**（超出抽 5 要求）；manifest/tree/lock sha 三链自洽 |
| 10 | generated 7 产物 vs producer 源字节比对 | 7/7 一致；lock.files 摘要全符 |
| 11 | `SHA256SUMS.txt -c` | 46/46 OK |
| 12 | `git diff --name-only 687f9d0..HEAD` vs §9 | 104 路径，0 越界 |
| 13 | 红线终检 | leads worktree clean（仅 gitignored .next）；producer worktree 0 dirty；共享 checkout 零触碰 |

---

## 五、结论（Verdict）

**APPROVE** —— 附一项 P1 交 Root 知情处置。

- 执行者可控面全部成立：RED→GREEN 纪律、vendor 字节身份（40/40）、双锁（tokens lock pin=01e6765 + renderer lock schema 校验过）、alias 休眠（守卫真实、全仓零引用）、layout 恰 +2 import、attribution 加法式接线、deps 精确钉、104 路径零越界、28/187 测试与 next build 均由复核者独立复现，G5 基建修正披露核真属实。
- P1（F-P1-1）为披露完整性 + 门语义空白问题，非执行者违规、非代码可修、不影响任何已验证的硬门；但「本切片新增债 = 0」的表述在最终 HEAD 不成立，须 Root 出注记（r3 或 2621 收口 sunshine 扩充）后方可合并。
- P2 三项不阻塞；F-P2-1 建议随 Root 勘误一并处理。

复核到此停手，等 Root 裁决。本报告文件未提交（untracked），是否入档由 Root 定。
