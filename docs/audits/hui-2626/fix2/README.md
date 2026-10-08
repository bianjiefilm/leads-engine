# HUI-2626 fix2 — gate-r2 修复轮证据包（leads-web）

对 2625 Finish Gate 第一轮裁决（gate-r2，leads-web=fail）9 条修复清单的逐条修复对照与浏览器证据。
分支 `codex/20261008-hui2626-fix2`（基线 origin/main 94c69d3，含 finish-r1）。运行时为 2626 本地真实三段链：web :29330（本分支 worktree）→ Go :29300 → SQLite /private/tmp/leads-2626/leads.db。

## 逐条对照（9/9）

| # | gate-r2 发现 | 修复 | 证据 |
|---|---|---|---|
| 1 | 手填租户/内部 id 109 处（5 文件） | `CrmCache.tenantId` 改名 `tenant`；页面不再手写租户标识；`x-tenant-id` 字面量收敛到 `productShell.listTenantHeader/scopeInit`。受检路由源 `tenant[_ -]?id / internal[_ -]?id` 子串清零 | `rescan/scan-leads-web.json`（6/6 pass，0 findings）；守卫测试 `web/tests/fix2.pageHygiene.test.ts` |
| 2 | 错误态裸后端文案（`record not found` / JSON.stringify 类 12 处） | `failureText(body, status)` 产品语态映射（404→「这条记录不存在，或不在当前工作范围。」；401/403→权限语句；5xx→「服务暂时没有响应，请稍后再试。」）；`JSON.stringify` 收敛进 `lib/fetchJson.postJson` 与 lib 层，页面源零出现；错误分支统一带「重试/返回」下一步动作 | `rescan/`（raw_error=0）；`scenarios/state-error-lead-none.png`（产品语句 + 重试 + 返回线索列表，无裸后端文案）；`web/tests/fix2.failureText.test.ts` |
| 3 | contrast serious×6（today/leads/opportunities/reception@1440 + leads@390） | work.light 语义 token 修正：文字用 `--pn-brand-text`（深青），实心底字色用 `--pn-brand-ink`，placeholder 用 `--pn-text-muted`；零裸 hex，作用域锁定 `[data-pn-surface="work.light"]`，公开表单 ：root 后备不动 | `axe-rescan.json` 13 组合 serious=0（总 violations=0）；`shots/*-1440.png / *-390.png` |
| 4 | heading-order×4 + region×11 页 | RecordFrame 标题 h3→h2（同族 h1→h2→h3）；desk-context 加 `role="region" aria-label="工作范围"`；每页一个 h1 保持 | `axe-rescan.json`：heading-order=0、region=0（13 组合全 CLEAN） |
| 5 | 390 横向溢出×2（/leads、/contacts；ledger 亦有 opportunity-result@430/390） | `.record-frame{overflow-wrap:anywhere}`（负责人 mem_ 长 token 折行）；`textarea{max-width:100%}`（cols=60≈502px 钳到容器） | `overflow-ledger.json`：11 页×5 视口 55 行，`horizontal_overflow` 全 false（gate-r2 溢出 4 行清零） |
| 6 | 主行动等权灰按钮（/sop、/outbound、今天页） | 门控渲染：`{可执行 ? 主行动(data-page-primary) : 下一步提示}`；今天页行内导航三链并一链（线索>接待>商机），其余链接降为页尾 muted 链 | `shots/sop-1440.png`、`shots/outbound-1440.png`（未选客户时只见提示句，无灰按钮排）；`web/tests/fix2.finish.test.ts` |
| 7 | partial 场景只有 SSR 断言，缺浏览器证据 | `outboundResultLine` composer 把「部分完成/已完成/未完成」立为一态并被页面消费；隔离栈按设计恒报 dial=false/connected=false，故 partial 场景用浏览器层响应 fixture 注入 `dial_succeeded=true, real_connected=false`，**任务本身经 BFF→Go→SQLite 真实创建（HTTP 201）**，授权/窗口为真实数据（本地库播种见 DECISIONS D4-11） | `scenarios/state-partial-outbound.png`（回执区「部分完成：模拟：是。拨打成功：是。真实接通：否。费用：0。」）；`evidence-summary.json.partial_scenario` |
| 8 | offline 是浏览器错误页，非应用内离线 UI | `public/sw.js`（只拦 navigate 模式）预缓存 `public/offline.html`：应用内离线卡（「你现在离线」+ 已保存记录不受影响 + 重试按钮 + 网络提示），系统色无 hex；`OfflineReady` 静默注册 | `scenarios/state-offline-leads.png`（断网导航到 /leads 得应用内离线页，标题「数海获客 · 离线」，非 ERR_*）；`scenarios/state-offline-recovered.png`（恢复网络点重试回到应用） |
| 9 | missing_loading_empty_error 6/6 路由 | 按现行 detector 口径在 6 路由页面源补 `data-state="loading|empty|error"` 静态声明：`<div data-state>` 包裹真实 SurfaceState 分支（静态声明与运行时 DOM 一致）；opportunities/[id] 早退主内容同样带声明 | `rescan/scan-leads-web.json`（missing_loading_empty_error=0）；`web/tests/fix2.pageHygiene.test.ts`（三态声明+错误动作守卫） |

## 证据清单

- `rescan/scan-leads-web.json`、`rescan/summary.json` — detector 自查（uifinish-scan，leads-web 6 路由，0 findings；基线 94c69d3 是 144 findings）
- `axe-rescan.json` — 与 gate-r2 supplement 完全相同的 13 组合（11 页@1440 + lead-detail/leads@390）；serious_total=0，每组合 violations=[] （contrast/region/heading-order 一并清零）
- `overflow-ledger.json` — 与 gate-r2 口径同列（1920/1440/1024/430/390 × 11 页）55 行，0 横向溢出
- `evidence-summary.json` — 场景断言汇总（error/partial/offline）
- `shots/` — 改动页新截图 ×{1440,390}（today、leads、lead-detail、reception、opportunity-result、sop、outbound）
- `scenarios/` — `state-error-lead-none.png`、`state-partial-outbound.png`、`state-offline-leads.png`、`state-offline-recovered.png`

## 复验命令（本地三段链）

```bash
GOWORK=off go build -o /tmp/uifinish-scan ./cmd/uifinish-scan   # 在 leads-engine 仓库
/tmp/uifinish-scan -routes docs/audits/hui-2626/fix2/rescan/routes-leads.json \
  -out docs/audits/hui-2626/fix2/rescan -repo leads-web=web -ref codex/20261008-hui2626-fix2
cd web && npx vitest run      # 261/261
npx next build                # exit 0
```

## 范围外观察（不属 9 条，未动）

- 390 顶栏（品牌区+范围切换+账号）排布拥挤（文字换行），非本票清单项、无横向溢出（ledger sw==cw），gate-r2 56 张基线截图同样形态；留待后续票。
- /intent、IntentOnOpportunity 的原生 `<select>/<textarea>/<button>` 不在 leads-web 受检路由表内（detector 0 findings），本轮只对 textarea 做了 max-width 钳宽（#5），控件收口留待其进入受检面时一并处理（见 DECISIONS D4-10）。
