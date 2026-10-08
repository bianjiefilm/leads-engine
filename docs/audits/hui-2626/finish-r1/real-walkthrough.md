# HUI-2626 finish-r1 真实数据走查记录（B 组）

日期：2026-10-08。本机：Go leads-server :29300 + web(Next dev) :29330 + 协议兼容 identity 替身 :29310。
库：SQLite /private/tmp/leads-2626/leads.db（真实 Go store，非内存替身）。

## 依赖范围（真实 vs 替身）

| 组件 | 状态 |
|---|---|
| Go leads-server（server/internal/*） | 真实构建运行（config_issues:0，GOWORK=off） |
| SQLite 存储层 | 真实（provision-tenant/member 落库） |
| web BFF（/api/* → Go /api/v1/*） | 真实（fail-closed 合同在链路上生效） |
| 平台 identity | 本机协议兼容替身（/internal/v1/identity/{login,refresh,revocations,session/resolve}，入仓零改动；替身源码在 /private/tmp，非仓库产物） |
| 通知/上传/外呼线路等平台依赖 | 未接（页面如实显示不可用/未知，不伪造成功） |

## 账户与租户

- 租户 A `tnt_fedce63fd2cc8e57f13dd6afeefffe5e`（范围甲）；租户 B `tnt_0fed731d37b27f919b82966c54b03d68`（范围乙）
- usr_owner_walk：A owner + B owner（双租户成员，用于切换验证）
- usr_sales_walk：仅 A sales

## 走查命令与结果（全部经 web BFF :29330）

1. 登录 `POST /api/auth/login` owner → `{"authenticated":true}`，HttpOnly 会话 Cookie 下发。
2. `GET /api/whoami`（x-tenant-id: A）→ 200：role=owner, tenant_id=A, member_id=mem_956e…（whoami 带租户头修复在链上生效）。
3. `POST /api/contacts`（A）→ 200 `con_b8a714…`，`tenant_id=A` 落库（王客户甲）。
4. 切换租户：`GET /api/whoami`（x-tenant-id: B）→ tenant_id=B；`GET /api/contacts`（B）→ `{"items":[]}`（A 的客户不串入）。
5. `POST /api/contacts`（B）→ `con_d904ce…` 落 B；A 列表仅含 A 客户。
6. 负例（身份纪律）：sales 会话 + x-tenant-id: B → `403 not_member`（whoami 与 /api/contacts/{B 的 id} 同样 403，不泄露存在性）。
7. 非 owner 列表口径：sales 读 A 列表 → `{"items":[]}`（服务端只回名下记录）。
8. `POST /api/contacts/con_b8a714…/followups`（A）→ 200 落库；`GET …/followups` 读回一致。
9. `PATCH /api/contacts/con_b8a714…`（编辑 email/tags/notes）→ 200；重新 GET 持久化字段一致（刷新不丢）。
10. 导出鉴权：owner `GET /api/contacts/export?format=csv` → 200 CSV（含 BOM 表头）；sales → `403`。
11. `POST /api/leads`（A，contact_id=con_b8a714…）→ 200 `lead_bdc3b0…`，assigned_member_id=owner 成员。
12. `GET /api/workbench`（A）→ 服务端分组 + money/joint_chain 真实口径（unprocessed 含新线索）。
13. `GET /api/leads/lead_bdc3b0…/timeline` → 200 真实事件（source/assignment）。
14. `POST /api/leads/lead_bdc3b0…/follow-through`（记跟进+安排 2026-10-09 下一步）→ 200 follow_up_id + next；再读 workbench：该线索已离开今日队列（下一步在明天，分组口径服务端决定）。

## 结论

- 真实保存/权限/隔离/导出路径全部可复跑（命令本节即脚本，重启 :29300/:29330 即可重放）。
- 无夹具租户参与任何数据路径；identity 替身只认证会话，租户/成员/授权判定全部在 Go 服务端 SQLite 上真实发生。
- UI 级 A/B 切换器依赖 eco-nav registry（PUBLIC_AI_ECO_NAV_URL）；无 registry 时受控切换器只显示「当前登录范围」（fail-closed，不落夹具 tenant-a/b），与 PR#41 复审结论一致。B 租户切换验证改以直读 API（x-tenant-id）+ 服务端 403 证明隔离。
