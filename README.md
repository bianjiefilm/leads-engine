# leads-engine(数海获客 · 独立商家 CRM)

获客系统独立应用底座(平台生态 L0/HUI-1748):Next.js + Go/SQLite 独立商家 CRM,租户工作台/联系人归属/平台接入,不内嵌接单库、不做 public-ai 全局客户库。
领域边界见 public-ai `docs/adr/0001`(D3:联系人/线索/商机归获客应用;D4:CRM 联系人不自动成为平台登录用户)。

## 布局

```
server/    Go 服务:net/http + modernc.org/sqlite(纯 Go)+ embed 迁移,监听 127.0.0.1:18230
web/       Next.js app router:BFF 即 src/app/api/*,页面只打 /api/*,dev/start 端口 18330
deploy/    env 配置样例(只有键名,无真值;真值放 /etc/leads-engine/)
docs/      计划与文档
_reports/  本地验收证据(不入库,gitignore)
```

## 本机运行

```bash
# 1) 服务端
cd server
LEADS_HTTP_ADDR=127.0.0.1:18230 \
LEADS_DB_PATH=data/leads.db \
LEADS_INTERNAL_TOKEN=<随机串> \
PLATFORM_IDENTITY_BASE_URL=http://127.0.0.1:18101 \
PLATFORM_IDENTITY_TOKEN=<本 app 专用 identity 令牌> \
go run ./cmd/leads-server

# 2) 前端 BFF
cd web
LEADS_SERVER_URL=http://127.0.0.1:18230 \
LEADS_INTERNAL_TOKEN=<同上随机串> \
npm run dev
# 浏览器只访问 http://localhost:18330,全部 API 走 /api/* 代理
```

配置门:缺任一平台依赖键时服务仍可启动(健康探针 `GET /healthz` 可见 degraded 与缺键清单),
但一切鉴权动作 fail-closed 显式报错(503 `config_gate` / `identity_unavailable`),绝不伪造成功。

## 测试

```bash
cd server && go test ./...      # 60 用例:权限矩阵/身份纪律/来源留痕/配置门/持久化
cd web && npm test              # BFF 转发语义 + 角色×分配×租户矩阵经 BFF 层复验
cd web && npm run build
```

## 领域根(最小表,全部 tenant 作用域 + 审计字段)

| 表 | 说明 |
|---|---|
| tenants / members / agent_grants | 租户、平台 principal 的成员引用(owner/sales/agent)、agent 逐租户授权 |
| contacts | 最小身份字段 + business_category + source_type + consent_status(扩展归 HUI-1691) |
| leads | 引用 contact + 来源引用 + 状态最小集(new/in_progress/converted/closed) |
| opportunities | contact + title + stage(open/won/lost)+ business_category(详细字段归 FEAT-0194) |
| source_refs | 来源 app、来源引用、授权范围快照(来源留痕) |

## 身份纪律(硬边界)

- 平台登录 principal(identity `usr_*` 派生)≠ 租户成员 ≠ CRM 联系人 ≠ 客户企业;
- principal_ref 只能来自 identity 会话解析;成员管理端点拒绝邮箱/手机号形态;
- `members.principal_ref` 不可变;不从手机号或同名数字 ID 自动合并平台用户;
- 建 CRM 记录绝不自动开平台账户/成员;无成员行的 principal 一律 403;
- agent 跨商家访问需 (tenant, principal) 逐租户 `agent_grants` 授权行,无全局共享客户池。

## 服务端权限(概览)

- owner:本租户全量读写 + 成员管理 + 导出;
- sales/agent:仅分配给自己的记录(他人/未分配记录以 404 掩码,防探测);不能成员管理/导出/改派;
- 跨租户任何动作 403;disabled 成员 403;矩阵逐格测试见 `server/internal/httpapi/server_test.go`。

## 平台接入(对齐 public-ai checklist v3.1 / integration-guide)

- Identity:BFF→server→`POST /internal/v1/identity/{login,refresh,revocations,session/resolve}`,
  头部 `X-App-ID` + `X-PilotSeaView-Internal-Token`(本 app 专用令牌,禁万能 token);refresh 必带 app_id。
- Notify/Upload:仅 client 脚手架 + 配置 + `FEATURE_NOTIFY`/`FEATURE_UPLOAD`(默认 off);
  off 或缺专用 token 时调用返回显式错误,不伪造事件/上传成功。

## 后续 FEAT 集成落点(本票未实现)

| 票 | 落点 |
|---|---|
| HUI-1691 联系人字段/去重 | `contacts` 表扩展字段 + 去重策略(当前无唯一约束、无自动合并) |
| HUI-1683/1680 线索接收 | `POST /api/v1/leads` + `source_refs`(非 manual 来源必须留痕已就位);notify client 挂 `server/internal/platform` |
| HUI-1692 跟进 | leads 状态机细化 + 跟进记录新表 |
| HUI-1693 / FEAT-0194 商机 | `opportunities` 扩展字段与阶段机 |
