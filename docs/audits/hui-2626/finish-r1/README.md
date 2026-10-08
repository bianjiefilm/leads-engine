# HUI-2626 finish-r1 证据包（浏览器走查）

- 日期：2026-10-08
- 栈：真实三段链 `web dev :29330（BFF）→ Go :29300 /api/v1/* → SQLite`，identity stand-in :29310。
- 账户：`owner@leads.local`（租户 A=平台舵主 tnt_fedce63…，另有租户 B 数据用于隔离走查，见 real-walkthrough.md）。
- 浏览器：系统 Chrome（Playwright `channel:"chrome"`，headless，无浏览器下载）。租户上下文以 localStorage
  `leads_tenant_id` 注入，等效平台门户下发的工作范围；授权仍由服务端每请求校验（无头/无注入时 fail-closed）。
- 截图脚本不入仓（/private/tmp/leads-2626/shots.mjs）；本目录只存产出物。

## 1. 截图矩阵（screens/，56 张）

代表页 9 个：今天 `/`、线索 `/leads`、客户 `/contacts`、接待 `/reception`、商机 `/opportunities`、
跟进提醒 `/sop`、外呼 `/outbound`、来源与费用 `/attribution`、订阅与用量 `/subscription`。
另有 `/intent /enterprises /channel-interactions /isolation` 等页在上一轮 DOM 断言测试覆盖（finishR1.redline）。

视口 × 状态覆盖：

| 文件模式 | 视口 | 状态 |
| --- | --- | --- |
| `<page>-1920/1440/1024.png` | 1920×1080 / 1440×900 / 1024×768 | 数据态（真实王客户甲记录） |
| `<page>-430/390.png` | 430×932 / 390×844 | 数据态 + renderer touch 密度（48px 控件） |
| `contacts-drawer-open-<vp>.png` | 全部 5 视口 | 抽屉打开态（桌面右侧 / ≤760 底部） |

覆盖说明：loading/empty 态由 SurfaceState 组件统一渲染并已被 `finishR1.*` SSR 测试断言
（骨架屏 + role 属性），error 态在真实走查中以租户 B 403 负例验证（real-walkthrough.md 第 9 步），
未逐视口重复截图。

## 2. 键盘/焦点证据

- 原始数据：`keyboard-focus.json`
- 截图：`screens/kb-0-before.png`（基线）、`kb-1-focus-ring.png`（行「查看详情」聚焦环）、
  `kb-2-drawer-open.png`（抽屉打开，焦点在抽屉内）、`kb-3-after-close.png`（关闭后）。

实测（1440×900，真实键盘 Tab/Enter/Esc）：
- Tab 26 次到达首行「查看详情」（`pn-r-button`），聚焦环可见；
- Enter 打开抽屉，`.pn-r-drawer` 出现，焦点落在抽屉内；
- Esc 关闭抽屉，焦点返回触发按钮「查看详情」（vendored Drawer finalFocus + DetailDrawer trigger 记忆）。

## 3. 触控目标证据（390/430）

- 原始数据：`touch-targets.json`（全部可点控件的可见宽高）
- 截图：`screens/touch-targets-390.png`、`touch-targets-430.png`

结论：≤430 走 vendored 原生 touch 密度（`RendererProvider density="touch"` → 48px 控件高度），
壳内原生导航链接/范围下拉以 globals.css ≤430 媒体查询兜底 ≥44px。实测仅剩两类 <44px 元素：
1. renderer Checkbox 的视觉隐藏原生 input（1×1，可访问性标准模式，实际命中区为 `.pn-r-boolean` ≥44px）；
2. 页脚行内文字链接「返回首页 · 商机管理」（inline link，WCAG 2.5.8 行内例外，不改版式）。

## 4. 去 Logo 产品族对比材料

同族产品（数海获客 CRM / 获客工作台）共用 painuo token 体系（tokens.css + aliases.css）与
vendored renderer 组件（Button/Drawer/SegmentedControl/SurfaceState 等），截图矩阵即对比材料：
- 同一 `leads-web` profile、`work.light` surface 在 9 个代表页一致（同一套 --pn-* token）；
- 页面无独立 Logo 资产，品牌区为文字「数海」（eco-top-nav），顶栏/导航/卡片/控件均由共享组件渲染；
- 390/430 与 1440/1920 双密度：compact(touch)+chips 单列 vs dense+rail+grid 4 列，见矩阵对比。

## 5. 已知边界（如实记账）

- UI 级 A/B 租户切换器依赖 eco-nav registry（PUBLIC_AI_ECO_NAV_URL）；本机无 registry 时切换器只显示
  「当前登录范围」（fail-closed）。A/B 隔离以 API 级验证为准（real-walkthrough.md）。
- 截图中的负责人字段显示成员 ID（`mem_…`）——服务端当前返回的就是成员引用，未做「成员 ID → 显示名」
  映射（后端无该查询端点，本票不新增后端能力）；如实显示真实数据，不属夹具。
- 浏览器截图为 owner 视角；sales 视角（非 owner 只看名下）由 API 级走查与既有 Go 测试覆盖。
