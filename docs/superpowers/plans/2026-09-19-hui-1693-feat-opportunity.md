# 计划:HUI-1693【FEAT-0194】商机管理(获客原生域)

- 日期:2026-09-19
- 票:HUI-1693 / FEAT-0194;分支 `hui-1693-feat-opportunity`(本地,不 push)
- 基线:main = 3ffd157(L0 底座)
- brainstorm 指挥者已代答(拍板见票面,本计划直接落为工程事实)

## 目标(本票只做)

商机阶段管理、金额/概率/预计成交,商机域**由获客原生管理**、不从公共 Task/接单状态派生。
商家经营销售(merchant_customer)与创意服务(creative_service)同库记录但**成交额/漏斗/后续动作不得混算**。
**不做**:服务需求草稿实体(HUI-1749/1751,本票只留挂载点)、接单域投影、支付/收款事实。

## 硬边界(验收红线)

1. **无跨类别合计端点**:统计必须显式带 `business_category`,缺省 400;响应内只有该类别。
2. **无「已收款」语义**:won = 人工标记成交;金额字段 `amount_cents`(可空,NULL=未知)+ `amount_source`(manual|unknown);预计成交/标记成交不是收款事实;代码与 UI 文案不出现「已收款/已支付」。
3. **阶段转换必须审计**:唯一入口 `POST /opportunities/{id}/stage`,写 `opportunity_stage_history`;PATCH 不再接受 stage(显式 400)。
4. **幂等**:同 from→to 重复请求返回 200 现状,不产生重复历史行。
5. **挂载点默认关闭**:`FEATURE_SERVICE_DRAFT` 默认 off → 路由不注册 → 404/不可见;on 时也只返回 501 骨架(归 HUI-1749/1751)。
6. 缺少可核实金额或成交结果显示「未知」,不补造数据(NULL ≠ 0)。

## 数据模型(迁移 0002)

- `opportunities` 重建(SQLite 改 CHECK 走建新表→拷贝→改名):
  - `stage` CHECK 扩为 `open/qualified/proposal/negotiation/won/closed_lost`(存量 `lost` 映射 `closed_lost`)
  - 新列 `amount_cents INTEGER NULL`、`amount_source TEXT NOT NULL DEFAULT 'unknown' CHECK(manual|unknown)`、`probability INTEGER NOT NULL DEFAULT 0 CHECK(0..100)`、`expected_close_at TEXT NULL`
- 新表 `opportunity_stage_history(id, tenant_id, opportunity_id, from_stage, to_stage, changed_by→members.id, changed_at, note)`,每行租户作用域;创建商机写 `from_stage=''` 起始行,时间线完整可回查。关闭/重开=普通阶段转换,同一审计。

## API(Go server /api/v1,BFF 透明转发,无业务判断)

| 端点 | 说明 |
|---|---|
| `POST /opportunities` | 扩展:amount_cents/probability/expected_close_at 可选入参;amount_source 派生(有金额=manual,无=unknown);写起始审计行 |
| `PATCH /opportunities/{id}` | 移除 stage;新增 amount_cents(显式 null=清空→unknown)、probability、expected_close_at;JSON null 与缺省需区分(自定义 Unmarshal) |
| `POST /opportunities/{id}/stage` | `{to_stage, note}`;权限=ActionUpdate(owner 全部,sales/agent 仅 assignee);幂等;审计 |
| `GET /opportunities/{id}/stage-history` | ReadRecord 权限;按 changed_at 升序 |
| `GET /opportunities/stats?category=X` | **category 必填**,缺省/非法 400;漏斗 6 阶段计数 + `known_total_cents`(仅非 NULL)+ `unknown_count`;sales/agent 只见自己(沿用列表过滤),不泄漏他人漏斗 |
| `GET /opportunities?category=X` | **category 必填**(列表按类别隔离,无跨类别列表) |
| `POST /opportunities/{id}/service-draft-intent` | 仅 `FEATURE_SERVICE_DRAFT=on` 注册;on 时 501 not_implemented(归 HUI-1749/1751);off 时 404 |

- `whoami` 增加 `member_id`(web 按权限显隐按钮所需,非业务判断)。

## 权限(沿用 L0 authz,不新增动作)

阶段转换/编辑 = `ActionUpdate`:owner 任意本租户记录;sales/agent 仅自己名下;未分配他人记录 404 掩码;跨租户 403;disabled 403;agent 无 grant 403。

## web(Next.js,BFF 不加判断)

- `src/lib/opportunity.ts` 纯函数:阶段枚举/中文标签(won=「人工标记成交」)、金额格式化(NULL→「未知」)、`canTransitionStage`(镜像服务端矩阵)、类别标签;vitest 覆盖 + 文案守卫(断言无「已收款/已支付」)。
- `/opportunities` 页:类别 tab(merchant_customer/creative_service 隔离)+ 该类别漏斗统计 + 列表。
- `/opportunities/[id]` 页:详情 + 阶段时间线 + 金额来源标识 + 阶段操作按钮按 whoami(role×assignee)显隐。

## 测试先行(TDD)

Go(`httpapi/opportunity_test.go`,沿用 harness):
1. 两类别样例各自推进全阶段,历史行完整(含创建行,changed_by 断言);
2. 幂等:重复同 to_stage → 200 且历史行数不变;
3. PATCH 带 stage → 400;
4. 类别隔离:stats 缺参 400;两类别计数/金额互不混;NULL 金额进 unknown_count 不当 0;
5. 权限矩阵:owner/assignee sales/非 assignee sales(404 掩码)/跨租户 B(403)/disabled(403)/agent 无 grant(403);
6. 挂载点:默认 off → 404;on → 501 且需会话。

web(vitest):标签/金额未知/权限显隐纯函数 + 禁词守卫。

验收命令:`go test ./...`(server)、`npm test`、`npm run build`(web)。

## 风险与回退

- SQLite 重建表迁移:tests 全新库走 0002;存量库 lost→closed_lost 映射;外键顺序(先建 history 表依赖新 opportunities?)→ 用「新建 opportunities_new→拷贝→DROP 旧→改名」并在其后建 history 表,避免悬空引用。
- 路由优先级:`/opportunities/stats` 字面量优先于 `{id}`(ServeMux 精确优先),测试覆盖。
