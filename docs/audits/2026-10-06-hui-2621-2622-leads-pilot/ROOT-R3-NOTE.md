# Root r3 注记 — vendor 画布债终态账目（2026-10-07 夜）

依设计文档 §15（Root 裁决 r3）与非作者复核 REVIEW-nonauthor.md P1-1：

- **终态账目修正**：lint delta @ba4cd2f = 71 findings = **18 继承**（§14 已裁，→2626/2598 清偿）+ **53 producer-vendored**（全在 `web/src/vendor/painuo/react/v1/src/styles.css`，消费锁锁死、与 producer `01e6765` 逐字节同一，卫生责任在 public-ai 源头 lint 门）+ **0 消费者自有新增** ✓。
- **判定域收缩**：§14 零增量语义的计数域=消费者自有面；vendored 路径（消费锁内）不计入消费者 delta 账本。
- **工具缺口**：lint 策略 `selectionPrefix=null` 全仓扫描、无 vendored-path 排除——记 producer 合同 backlog（非本切片缺陷）；2621 收口 Linear sunshine 一并披露。
- **P2 处置**：P2-1（evidence.json G4-vendor-stageout-resolved 槽位 note 写「exit0 ok:true」而实录为 `{ok:false,reason:path}` exit1，实情=两次直 stage-out 败 path 规则后走 git-archive export 路线成功，原始可重构）——以本注记+§15 双留痕纠正；P2-2/P2-3（painuoStatus typeof 死代码/importer 测试无意义拼接）随 2626 journey 迁移顺手清偿。
- 复核 APPROVE（0 P0；独立复跑 vitest 28 files/187 tests 全绿、next build exit0、vendor 40/40 身份、lint 三门+delta 集合相等复现）在 REVIEW-nonauthor.md。
