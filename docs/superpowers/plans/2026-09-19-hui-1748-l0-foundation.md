# 计划:HUI-1748【平台生态 L0】获客独立应用底座

- 日期:2026-09-19
- 票:HUI-1748;分支 `hui-1748-l0-foundation`(本地,不 push)
- 边界输入(只读,经 `git show origin/main:<path>`):
  - public-ai `docs/integration-guide.md`(identity login/refresh/session resolve/revocations 端点、内部令牌头 `X-PilotSeaView-Internal-Token`、`X-App-ID`)
  - public-ai `docs/adr/0001-platform-ecosystem-e0-incremental-baseline.md`(D3 所有权:联系人/线索/商机归获客应用;D4:CRM 联系人不自动成为平台登录用户;D5:身份≠租户成员≠资源授权≠付费权限)
  - public-ai `docs/public-services-checklist-v3.1.md`(产品 env 键名、BFF-only 红线)
  - public-ai `docs/order-ecosystem/2026-09-19-hui-1723-repos-sha-inventory.md` §4(平台服务配置键名)
  - public-ai `examples/compat-handoff-consumer/main.go`(契约消费姿态参考)
  - guanlan-order Go 工程惯例(目录划分、测试姿态),不复制业务

## 目标(本票只做)

独立获客应用(CRM)的 L0 底座:独立部署单元 + 平台接入面 + 领域根骨架 + 权限/身份纪律的可测试约束。**不做**:联系人去重/字段扩展(HUI-1691)、线索接收(1683/1680)、跟进(1692)、商机详情(1693/FEAT-0194)。

## 架构拍板(票面已定,此处落为工程事实)

1. `server/`:Go,`net/http`(1.22+ ServeMux,不引路由库)+ `modernc.org/sqlite`(纯 Go,无 cgo,Windows 可测)+ `embed` 迁移 + env 配置。监听 `127.0.0.1:18230`。
2. `web/`:Next.js app router,BFF 即 `src/app/api/*`,页面只打 `/api/*`;dev/start 端口 `18330`。
3. `deploy/`:env 样例(只有键名与注释,无真值)。
4. 身份链:浏览器 Cookie → Next BFF 转发 → Go server 校验 `X-Internal-Token` + 自行向 platform-identity `POST /internal/v1/identity/session/resolve` 解析 session(不过 BFF 转述的 principal,防伪造)。解析失败/未配置 → fail-closed 显式 503。
5. E2E 身份桩:仿 platform-identity 的独立小程序,**只放 `_reports/hui-1748-l0/evidence/`**,不进生产代码路径(server 生产路径只有 `IDENTITY_MODE=platform` 一种)。报告标注 limitation。

## 领域根(最小表;每行 tenant 作用域 + created_at/updated_at/created_by 审计)

| 表 | 最小字段 | 后续票扩展点 |
|---|---|---|
| tenants | id,name | — |
| members | tenant_id, principal_ref(平台 principal,不可变), role(owner/sales/agent), enabled | HUI-1692 组织结构 |
| agent_grants | tenant_id, principal_ref, granted_by(agent 逐租户授权;无全局客户池) | — |
| contacts | name/phone/email, business_category(merchant_customer/creative_service), source_type(manual/form/touch_campaign), consent_status, source_ref_id, assigned_member_id | HUI-1691 字段+去重 |
| source_refs | source_app, source_ref, auth_scope_snapshot(来源留痕) | HUI-1683/1680 线索接收 |
| leads | contact_id, source_ref_id, status(new/in_progress/converted/closed) | HUI-1692 跟进 |
| opportunities | contact_id, title, stage(open/won/lost), business_category | FEAT-0194 详细字段 |

## 身份纪律(硬边界,测试先行)

1. members 只能经 owner 管理端点以 identity 解析出的 principal_ref 创建;不接受用户输入的 email/手机号派生 principal。
2. 建 contact/lead/opportunity 绝不创建 members 或平台账户;无任何"从 CRM 数据自动开户"代码路径。
3. members.principal_ref 不可变(PATCH 拒改);不从手机号/同名数字 ID 自动合并平台用户。
4. principal 无 member 行 → 403,不自动入租户;disabled member → 403;跨租户 → 403。
5. agent 仅在存在 (tenant, principal) agent_grants 行时才可访问该租户;无全局共享。

## 权限矩阵(服务端强制;测试逐格覆盖)

| 动作 | owner | sales(记录 assignee=self) | sales(未分配/他人记录) | agent(有 grant) | agent(无 grant) | disabled 任何角色 |
|---|---|---|---|---|---|---|
| 建记录 | ✅ | ✅(自动 assign self) | — | ✅(自动 assign self) | ❌403 | ❌403 |
| 读本租户全部 | ✅ | ❌(仅自己) | ❌ | ✅ | ❌ | ❌ |
| 改/删自己记录 | ✅ | ✅ | ❌403/404 | ✅ | ❌ | ❌ |
| 成员管理/导出 | ✅ | ❌403 | ❌ | ❌ | ❌ | ❌ |
| 跨租户任何读写 | ❌403 | ❌403 | ❌403 | ❌403 | ❌403 | ❌403 |

## 平台接入(本票范围)

- Identity:登录/刷新/登出/会话解析走 BFF→server→identity;`LEADS_APP_ID=leads-engine`;内部令牌按服务专用(键名见 deploy 样例)。
- Notify/Upload:**只做 client 脚手架 + env 配置 + `FEATURE_NOTIFY`/`FEATURE_UPLOAD` 开关(默认 off)**;off 或缺 token 时调用返回显式 `ErrFeatureDisabled`,不伪造事件/上传成功。
- 配置门:缺 identity base URL / 内部令牌 → 启动仍可(健康探针可用)但一切鉴权动作显式报错;绝不伪造成功。

## 安全基线

- 日志脱敏:phone/email 掩码函数,所有结构化日志经它;测试断言无明文手机号。
- 来源留痕:凡 source_type≠manual 必须带 source_ref(source_app+source_ref+授权范围快照)。
- 审计字段全表必备;SQL 全参数化。

## 任务序(TDD:先测试后实现)

1. Go 模块脚手架 + `go.mod`(modernc.org/sqlite)+ 配置加载与配置门测试。
2. db:embed 迁移 + 迁移测试(表/约束存在)。
3. redact 脱敏测试。
4. identity client(session resolve 契约测试,httptest 假 identity)。
5. authz:租户/角色/分配/grant 矩阵测试(纯域逻辑)。
6. store + httpapi:成员管理、contacts/leads/opportunities CRUD,矩阵×租户集成测试(通过 HTTP 层)。
7. 身份纪律测试(不可变 principal_ref、无自动开户、无 member 403、disabled 403)。
8. Notify/Upload 脚手架 + 开关测试。
9. web/:Next.js BFF 代理 + auth 路由;vitest:代理语义(转发 cookie、不可提权、状态码透传、缺内部令牌 fail-closed)+ 角色×分配×租户矩阵经 BFF 层复验。
10. `npm run build` 绿;`go test ./...` 绿。
11. E2E 脚本(_reports/evidence):identity stub + server 起停,验收五条(登录→建记录→重登恢复;跨租户 403;未分配受限;disabled 403;关依赖 fail-closed)。
12. README、deploy 样例、REPORT.md、提交(带 HUI-1748,不 push)。

## 验收对照

- [x] E2E 五条(23/23 PASS,_reports/hui-1748-l0 本地证据);go test 全绿(60 例);BFF 矩阵测试(25 例);npm run build 成功;无真实密钥;无全局客户池;无自动开户。
