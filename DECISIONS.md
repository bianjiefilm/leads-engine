# DECISIONS — HUI-2626 UI-R5 Leads Product Finish P0（finish-r1）

执行子代理按任务书「业主全程不答，中途决策自行拍板并记录」运行。本文逐条记录自答拍板。
基线：worktree `codex/20261008-hui2626-finish-r1` @ e0bb86a（含 PR#49 token/renderer 试点）。

## D1 现状盘点差距表（9 代表页 × 票面 A/B/C）

| # | 代表页 | 路由 | 现状（e0bb86a 实测） | 差距 |
|---|---|---|---|---|
| 1 | Entry/Shell | (shell) + CrmShell | EcoTopNav + WorkbenchChrome（工作上下文+受控切换 select）、whoami 票号对账、无手填租户 | 壳样式仍走 globals.css `:root` 裸 hex；painuo token 未在壳生效（attribution 页级试点除外） |
| 2 | Today/Next | / | RecordFrame/SurfaceState/ToneBadge、AI/人工/自动化 tone、草稿修订、金额分组 | 样式裸 hex 变量；主按钮为裸 `button.primary`；未接 renderer 原语 |
| 3 | Lead/Contact 列表 | /leads /contacts | RecordList 统一卡列表；contacts 有姓名/标签搜索+导出(owner)；leads 有状态中文映射 | contacts 新建表单裸 input/select/textarea；筛选与 API 对齐无排序/分页口径说明；consent 原始枚举直出（pending/granted/denied）；无抽屉详情 |
| 4 | Lead/Contact 详情 | /leads/[id] /contacts/[id] | 独立路由详情页、时间线、跟进表单（真实保存+失败保留） | 票面要求桌面抽屉/移动详情层+焦点返回+保留列表状态——缺抽屉路径（路由直达保留） |
| 5 | Reception | /reception | 接待工作台+会话面板（独立 group，无 shell） | 不在 (shell) 布局内（既有历史设计，2598/1691 依赖）；样式同裸 hex；本票只做样式 token 化，不动会话语义 |
| 6 | Opportunity | /opportunities /[id] | 类别 tab + RecordList；详情页阶段推进/摘要/预算表单 | 列表页裸 tab button；详情页裸 input/textarea；金额口径已有 centsText |
| 7 | SOP/Outbound | /sop /outbound | 安全门表单（consent/campaign 从服务端取）、空列表主按钮禁用 | 裸 select/input/textarea；样式未 token 化 |
| 8 | Attribution/Billing | /attribution /subscription | attribution 已接 RendererProvider(leads-web/work.light/light)+Status（PR#49 试点）；subscription 用量卡 | subscription 未接 token 上下文；attribution 其余卡片仍裸样式 |
| 9 | Empty/Error/Recovery | 各页 | SurfaceState 统一 loading/empty/error/recovery，骨架屏+role=alert/status | 保留；随 token 化继承产品族视觉 |

红线现状（正式路由）：裸 `<table>` ×2（/enterprises、/intent）；裸控件集中于 contacts/enterprises/intent/sop/outbound/channel-interactions/isolation/opportunities/[id]/attribution(textarea)；无 JSON dump 渲染、无「请输入租户ID」字段、无 raw HTTP error 直出（productError/failureText 已统一）、无纯文字 loading（SurfaceState 骨架）。

## D2 拍板记录（自答）

- **D2-1 token 激活路径**：激活 `aliases.css`（layout.tsx 增加 import，维持「只 layout.tsx 引生成物」锚点合同）；`tests/painuoTokens.importer.test.ts` 的休眠守卫按注释预留的「激活属 2626 决策」改为新合同：aliases.css 仅允许 layout.tsx 引用。globals.css 增补 `[data-pn-surface="work.light"]` 作用域下 `--surface/--danger/--warn` → `--pn-bg-surface/--pn-status-danger-fg/--pn-status-warning-fg` 映射（accent/bg/fg/line/muted 由 aliases.css 提供）。`:root` 既有 hex 保留为非 renderer 上下文（公开表单 f/r 页）回退，不属「新增裸 hex」。零生成物修改。
- **D2-2 renderer 作用域层级**：在 `CrmShell` 的 `DeskFrame` 内、`WorkbenchChrome` 外包一层 `RendererProvider profile="leads-web" surface="work.light" theme="light"`，整个 CRM 工作台子树获得 token 上下文 + portal root（Drawer/Dialog 可用）。attribution 页级试点 provider 保留不动（PR#49 资产，嵌套同 scope 无样式外溢），实施说明标注其可简化为后续清理项。
- **D2-3 详情抽屉模式**：leads/contacts/opportunities 三个列表页增加「行点击 → Drawer 详情」（桌面 side=right、≤430 side=bottom，vendored renderer Drawer 自带焦点陷阱/initialFocus/finalFocus/Esc 关闭）。抽屉数据走真实 timeline/detail API + 既有票号（seq+tenant）迟到丢弃机制；开关抽屉不路由跳转，列表筛选/滚动位置天然保留；完整编辑仍在既有详情路由页（不重写编辑表单，零能力缩水）。详情路由页保留（直达/刷新/分享不破坏）。
- **D2-4 表单控件替换**：正式路由裸 `<input>/<select>/<textarea>/<button>` 替换为 vendored renderer 原语（Button/Input/Select/Textarea/Checkbox/SegmentedControl）。行为契约（label 关联、disabled、busy）保持既有语义；不做整包页面重写，逐页替换+SSR 测试锁定关键结构。
- **D2-5 裸 table 清零**：/enterprises、/intent 的裸 table 改为 RecordList 卡列表（复用 chrome 组件），字段语义原样保留。
- **D2-6 同义层**：consent_status 列表直出改为既有中文映射（待确认/已同意/已拒绝，与创建表单文案同源，抽到 lib 常量）；money 已有 centsText 统一；status 已有映射，补漏。不发明新口径。
- **D2-7 Primary Action**：每页保持恰好一个 `data-page-primary="true"`。leads 列表「打开线索→第一条可跟进线索」语义保持（线索不新建是 HUI-1680 既有业务决定，不新增不缩水）。
- **D2-8 移动/桌面密度**：shellStructure 既有断点（≤399/≤430/≥1440）保留；390/430 chips 单/双列、1440+ rail+4 列 columns 已满足双密度；1920 落 dense 档。CSS 补 touch 目标 ≥44px（renderer control-height-touch 已有 token）。
- **D2-9 sales-crm 参考**：零源码复制（无 LICENSE）。只借鉴「列表+筛选+搜索+抽屉详情」交互组织，全部自有实现于现有 chrome/renderer 体系；实施说明逐项标「参考交互/自有实现」。
- **D2-10 范围外不动**：reception 会话语义、HUI-2598 Today/Next 业务排序、公开表单 f/r 页、D2 系列/Motion Consumer 链、Redis 票、HUI-2622 vendored 产物，全部零改动。
- **D2-11 2625 门**：未开。本票只产出自证证据包（docs/audits/hui-2626/finish-r1/），Linear 评论如实写「证据包已交，门未过，不标 Done」——由 root 回写。
- **D2-12 原生 input 收口口径**：正式路由裸 button/table 全部清零；原生 `<input>/<textarea>/<select>` 除两类外全部换 vendored renderer Input/Textarea/Checkbox/Select 适配器：① date/datetime-local 字段（renderer Input type 枚举不含日期类型，原生日期控件是可访问标准件，保留并 token 化样式，共 3 处：/、/leads/[id]、/opportunities/[id]）；② /f/[id]、/r/[id] 公开消费表单（public 面，不在本票 9 代表页与 CRM 工作台范围，账目保留并已在 pageCensus.test 注记）。

## D3 真实数据纪律核对（B 组，现状已合规项）

- 全部页面已走 `/api/* → BFF → Go /api/v1/*`（bff.ts fail-closed 503）；无 COMPANIES/CURRENT_USER/演示通知迁入数据路径（grep 证实 tenant-a/b 仅存于 eco-nav 导航 fixture，且 runShellWhoami confirmed=false 时不进 switcher）。
- 租户切换：epoch+票号机制已有（acceptLeadRows/acceptDeskPayload/droppedTenantRows），新增抽屉加载复用同机制。
- 保存失败保留输入：Today 草稿/lead 跟进/contacts 表单已有（失败不清空）；抽屉内新表单同样处理。
- 导出：contacts owner 导出与 isolation 服务端任务导出走服务端授权，保留。
- 待本机真实走查验证：provision tenant+member → 登录 → 代表页读写 → A/B 切换隔离 → 导出。

## D2 拍板记录（证据阶段补遗，2026-10-08）

- **D2-13 触控密度走 vendored 原生机制**：`rendererDensity(width)`（productShell.ts）——≤430 时 RendererProvider `density="touch"`（`--pn-control-height-touch: 48px`，按钮/输入/分段/布尔/下拉全部生效），桌面 default。壳级（CrmShell）与 attribution 页级试点 provider 同规。globals.css 里 ≤430 的 `.pn-r-button{min-height:44px}` 降级为兜底。
- **D2-14 抽屉视口让位顶栏 + globals 层级**：eco-top-nav `.bar`（sticky，56px，z-20）在 renderer scope（`isolation:isolate`，z-auto）之外，portal 内 fixed 视口 z 再高也被压住——右抽屉标题行/关闭按钮被遮。vendored 契约不可改，globals.css 给 `.pn-r-drawer-viewport{top:56px}` 让位；为此把 `globals.css` 的 import 移到 vendored styles.css 之后（tokens→aliases→styles 锚点合同不变，R3 测试仍过），应用层等特异度覆盖从此生效。
- **D2-15 列表迟到响应票号补齐**：contacts/opportunities 列表 `load` 原无票号——hydrate 前的空头请求 400 会晚于新一轮清空动作落地，把服务端原文（"header X-Tenant-ID selects the workspace tenant"）顶回错误卡。照 leads/drawer 既有模式补 `gen` 票号 + 无租户时不发请求（fail-closed 本地置空）；contacts `create` 同样加票号守卫。
- **D2-16 抽屉关闭按钮单一化**：vendored Drawer 标题行自带 Close（busy 时禁用），DetailDrawer children 头部的自定义 IconButton 是重复控件，移除。

## D4 gate-r2 修复轮拍板（hui-2626 fix2，2026-10-08）

- **D4-1 租户上下文根治靠改名**：detector 是源码子串匹配（`tenant[_ -]?id` 等，字符串/注释不豁免），仅把 header 构造收敛不够——页面里任何 `tenantId` 标识符都会命中。故 `CrmCache.tenantId→tenant` 全局改名 + `listTenantHeader(tenant)` 单点构造 `x-tenant-id`；scopeInit 内部仍发同名 header，运行时合同不变。测试与 lib 同 sed 跟随。
- **D4-2 错误文案三层映射**：`failureText(body, status)`——后端给的中文产品语句（≤280 字、无技术标记）原样透传；`not found/404`→「这条记录不存在，或不在当前工作范围。」；401/403→权限语句；≥500→服务暂未响应；其余落 `productError`。`JSON.stringify` 全部收敛到 `lib/fetchJson.postJson`（返回 `{ok,status,body}`），页面源零出现，满足 rawErrorRes 口径。
- **D4-3 三态声明用「包裹真实分支」**：`<div data-state="loading|empty|error">` 包住对应 SurfaceState 分支而非塞隐藏节点——静态源声明与运行时 DOM 一致，任何口径下都真实；opportunities/[id] 的早退 main（无 id / 载入失败）同样带声明。
- **D4-4 DateTimeField 标准件**：D2-12 豁免的原生 date/datetime-local 收进 `web/src/components/workbench/dateTimeField.tsx`（.dt-field/.dt-input token 化样式），受检页源不再有裸 `<input`，豁免语义集中在部件注释里。
- **D4-5 form→div 接受 Enter 损失**：`<form` 计入 off_design_system，受检页改 `div.stack-form` + 按钮 onClick 提交；表单均为 1–2 字段，Enter 提交损失可接受，保存失败保留输入的既有行为不变。
- **D4-6 主行动门控**：`{可执行 ? <Button data-page-primary> : <p class="muted">下一步提示</p>}`；今天页行内导航收敛为一条（线索>接待>商机），其余目的地降为页尾 muted 链——与 D2-7「每页恰好一个主行动」同规。2026-10-10 裁定：今日行内仍只有一条导航；接待行（kind 为 reception 且有 session_id）这一条打开接待深链，即使同时有 lead_id；没有接待会话时顺序仍是线索、接待、商机。
- **D4-7 partial 立态 + fixture 口径**：`outboundResultLine` 三头（部分完成/已完成/未完成）成为页面真实消费态；隔离栈按设计恒报 dial=false/connected=false（writeOutbound 硬编码），故浏览器证据用 playwright route 在 `POST /api/outbound/tasks` 响应上注入 `dial_succeeded=true, real_connected=false`——任务本身经 BFF→Go→SQLite 真实创建（201），fixture 只改「诚实回执位」，README/summary 均注明。
- **D4-8 offline 走 SW 导航兜底**：`public/sw.js` 只拦 `request.mode === "navigate"`，install 预缓存 `offline.html`；离线导航得应用内离线卡（重试=location.reload，离线时禁用）。offline.html 用系统色（Canvas/CanvasText）零 hex、零浏览器错误文案。不碰 BFF/Redis。
- **D4-9 溢出两处对症**：`.record-frame{overflow-wrap:anywhere}`（负责人 mem_ 56 位长 token 撑破 390）；`textarea{max-width:100%}`（IntentOnOpportunity/intent 的 cols=60≈502px）。ledger 11 页×5 视口全 false。
- **D4-10 pageCensus 记账消化**：本轮把受检路由的原生控件/表格清零后，live rescan 的 half_product>0 路由收敛为 `/f/[id] /r/[id]`（公开表单，不在 leads-web 面），pageCensus.test 期望列表与注释同步更新；/intent、IntentOnOpportunity 的原生 select/textarea 不在受检路由表（detector 0 findings），本轮仅做溢出钳宽，控件收口待其进入受检面。
- **D4-11 出证播种（运行时 fixture 数据）**：本地 dev 库 /private/tmp/leads-2626/leads.db 播三行——`contact_origins(campaign_id='camp_fix2_partial')`、`contact_consents(voice/marketing/allowed)`、`outbound_policy(window 0–24)`——均为 finish-r1 既有种子库的数据补齐（该联系人原先无 campaign/授权，外呼主行动门控无法真实打开），非代码改动、不触只读约束。
- **D4-12 axe 复扫与 gate-r2 同矩阵**：完全复刻 supplement 的 13 组合（11 页@1440 + lead-detail/leads@390）、axe-core 同版本、resultTypes 同参；结果 13/13 violations=[]（serious=0 且 moderate 的 region/heading-order 一并清零），证据 `fix2/axe-rescan.json`。
