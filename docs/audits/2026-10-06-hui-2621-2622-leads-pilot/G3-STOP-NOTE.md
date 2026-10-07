# HUI-2621+2622 leads-engine 消费试点 —— G3 停门记录（2026-10-07）

## 停门点
G3（T 面：sync --check → --apply → lock；lint lock/baseline/delta）的 delta 判定 **FAIL**（verdict=fail，exit 1）。
按执行纪律「任一失败即停报 Root，不跳门」，G4–G9 未执行。

## 门内各步实测
| 步 | 结果 | 证据 |
|---|---|---|
| sync --check（apply 前） | PASS（首采用预期态：`{"status":"drift","unrelatedWip":[]}`，无无关 WIP） | g3/sync-check.first.json |
| sync --apply | PASS（exit0 `{"status":"applied"}`；8 文件落盘；producer worktree 保持零脏） | g3/sync-apply.first.json |
| lint lock | PASS（exit0 verdict=pass，files=7） | g3/lint-lock.verdict.json |
| lint baseline | PASS（exit0；71 文件 / 52 记录，与设计 §1 演练一致） | painuo-style-baseline.v1.json |
| lint delta | **FAIL**（exit1；18 findings，全部在 `web/src/app/globals.css`） | g3/lint-delta.head-0540a7a.json |
| 幂等 | PASS（二次 --apply/--check 均 `unchanged`，lock 哈希不变） | g3/sync-*.second-idempotent.json |

## delta FAIL 根因（三点实证，详见 evidence.json）
1. **债务是继承的，不是本切片引入的**：在 Root 批准的 base 钉 `687f9d0` 本身（本分支任何提交之前）跑 delta，同样是这 18 条（findings 集合逐字节一致，g3/lint-delta.basepin-687f9d0.json）。本分支 head 与 base 钉的 findings 集合零差异 ⇒ 本切片新增债务 = 0。
2. **设计 §1 演练结论在其记录时点为真**：在演练观察 HEAD `368eaa0` 跑 delta = pass、0 条（g3/lint-delta.rehearsal-368eaa0.json），与设计记录完全一致。
3. **债务引入窗口 = 368eaa0..687f9d0**：`03bd4a8`（hui-2598 今天工作台）与 `6d68f19`（hui-2626 外壳）向 globals.css 增加含裸 hex 的 305 行；两提交均为 base 钉祖先、均非演练 HEAD 祖先。设计 §8 矩阵将 2598-today 记为「未合分支」，与实核不符。

## 为何 owned 路径内不可修复
- `globals.css` 为设计 §2.1 明确不碰文件；
- `lint-allowlist.json` 为 sync 锁定产物（逐字节锁 + artifact-manifest 校验），不可编辑；
- `lint.py` 强制 `--base == sourceAnchor.sha == b58ea173`（leads-web consumer 条目），「以 687f9d0 为基的 delta」在工具上不可表达（设计 §4 措辞与工具约束的偏差，一并报 Root）。

## 需 Root 裁决的选项（供参考，非本代理决定）
a) 接受「零增量」语义：将 base 钉处的 18 条记为继承基线偏移，本切片以「findings(base 钉) == findings(head)」为 delta 通过判据继续 G4；
b) 令 2626/2598 责任面先清偿 globals.css 债务后本切片重跑 G3；
c) 由 producer 侧（public-ai）增补 migrationExceptions 后重发 allowlist（改变 T 面锁，需重走 sync）。

## 分支状态（留 Root，未 push、未建 PR、未动 Linear）
- `codex/20261006-hui-2621-2622-leads-pilot` @ fef9f01(RED) → 0540a7a(T 面 8 文件) → 本证据提交。
