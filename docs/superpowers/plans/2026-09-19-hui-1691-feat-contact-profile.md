# 计划:HUI-1691【FEAT-0192】客户档案管理(联系人档案域)

- 日期:2026-09-19
- 票:HUI-1691 / FEAT-0192;分支 `hui-1691-feat-contact-profile`(本地,不 push)
- 基线:main = 3ed09cb(L0 底座 + FEAT-0194 商机域)
- brainstorm 指挥者已代答(拍板见票面,本计划直接落为工程事实)

## 目标(本票只做)

联系人/客户**档案模型、页面、访问控制**:
备注/标签、按 (联系人, 来源提交/渠道) 维度的授权(consent)记录与不可逆撤销、
跟进时间线、owner 专属 CSV 导出、软删除。
**不做**:联系人合并端点、线索去重规则(HUI-1683)、接收渠道/webhook(HUI-1680)、
自动触达、向其他产品同步、任何平台侧开户。

## 硬边界(验收红线)

1. **身份纪律不放松**:手机号绝不派生平台账号;CRM 写路径绝不改 members 行;
   principal_ref 只来自 identity 会话解析(沿用 L0 validPrincipalRef / provision 检查)。
2. **consent 撤销不可逆**:`revoked_at` 一旦写入即为持久标记;
   同 consent 键 (contact_id, source_submission_ref, source_channel) 的事件重放
   只能幂等返回现状,**不得清除/改写 revoked_at**;重新授权必须换新提交引用(新键)。
3. **consent 按来源维度保留**:多次线索可关联同一联系人,不同来源的授权条件
   (notice_version/purpose/marketing_allowed/撤销状态)各自成行、独立可查、独立撤销;
   撤销来源 A 不影响来源 B。
4. **URL/日志/公共 Context 不承载联系方式或跟进全文**:
   - 列表搜索只接受姓名/标签;出现 `phone` 参数或 7 位以上连续数字的搜索值 → 400;
   - 请求日志只记 path 不记 query;跟进/备注内容永不入日志,只记长度;
   - 手机号/邮箱入日志必须经 redact 掩码(扩展 redact 管道覆盖新字段)。
5. **导出是显式特权动作**:`GET /api/v1/contacts/export?format=csv` 仅 owner
   (authz ActionExport);CSV 流式输出;服务端审计一行(谁/何时/多少条);
   导出仅档案字段,**不含跟进全文**。sales/agent 一律 403。
6. **删除/停止营销/最小审计三者分别定义**:
   - 删除 = 软删除(owner 专属):`deleted_at` + 字段脱敏占位,行保留以维持引用;
     删除后档案对所有角色 404,不再出现在列表/导出;
   - 停止营销 = `marketing_revoked`:批量撤销该联系人全部有效营销授权(幂等,不可逆);
   - 最小审计 = consents / followups 行保留(删除后仍在库,供依法留存)。
7. **停用成员**:403 全局拒绝(沿用);其历史 followups 保留(member_id 不变)但
   跟进时间线本身只追加、无编辑端点,天然满足「不可再编辑」。
8. **权限矩阵沿用 L0**:owner 全部;sales/agent 仅 assignee 相关记录;
   非 assignee 一律 404 掩码;跨租户/disabled/无 grant agent 403。

## 数据模型(迁移 0003,全部 additive)

- `contacts` 扩列:`notes TEXT NOT NULL DEFAULT ''`、`tags TEXT NOT NULL DEFAULT ''`
  (逗号分隔、服务端归一化:去空白、去重)、`deleted_at TEXT NULL`(软删除)。
- 新表 `contact_consents`:
  `(id, tenant_id→tenants, contact_id→contacts, tenant_scope, source_submission_ref,
  source_channel, notice_version, purpose, marketing_allowed 0/1, revoked_at NULL,
  revoked_reason, created_at)`,
  `UNIQUE(contact_id, source_submission_ref, source_channel)`。
  `source_submission_ref` 字段名即 T1(1747)投递端对齐锚点(撞名即可,无前置依赖)。
  `tenant_scope` 记写入时的租户快照(审计锚)。
- 新表 `contact_followups`:
  `(id, tenant_id→tenants, contact_id→contacts, member_id→members, note, created_at)`
  只追加时间线,note 全文不进日志。
- 索引:consents/followups 按 contact_id;contacts 按 (tenant_id, deleted_at)。

## API(Go server /api/v1,BFF 透明转发,无业务判断)

| 端点 | 权限 | 说明 |
|---|---|---|
| `POST /contacts` | ActionCreate | 扩展入参 notes/tags;来源留痕规则不变 |
| `GET /contacts?name=&tag=` | ActionReadList | 姓名/标签搜索;`phone` 参数或疑似手机号搜索值 400;sales/agent 仍只见自己 |
| `GET /contacts/export?format=csv` | ActionExport(owner) | CSV 流式;审计一行;format≠csv 400 |
| `GET /contacts/{id}` | ActionReadRecord | 含 notes/tags;已删除 → 404 |
| `PATCH /contacts/{id}` | ActionUpdate | 扩展 notes/tags;reassign 仍 owner 专属 |
| `DELETE /contacts/{id}` | ActionDelete(owner,新增) | 软删除 + 脱敏占位;幂等目标缺失 404 |
| `GET /contacts/{id}/consents` | ActionReadRecord | 全部来源授权明细 + 汇总 |
| `POST /contacts/{id}/consents` | ActionUpdate | 记录/刷新同键授权;撤销后重放 → 200 现状不变(`state=replay_unchanged`) |
| `POST /contacts/{id}/consents/{cid}/revoke` | ActionUpdate | 撤销单来源;重复撤销幂等(revoked_at 不变) |
| `POST /contacts/{id}/revoke-marketing` | ActionUpdate | 停止营销:批量撤销全部有效营销授权,返回撤销条数 |
| `GET /contacts/{id}/followups` | ActionReadRecord | 时间线,created_at 升序 |
| `POST /contacts/{id}/followups` | ActionUpdate | 追加跟进(author=调用者);note 限长,不入日志 |

- 入参限长:source_submission_ref ≤128、channel ≤64、notice_version ≤64、
  purpose ≤64(默认 marketing)、撤销原因 ≤200、tags ≤10 个且单个 ≤30 rune、
  notes/followup note ≤2000 rune,超限 400。
- `marketing_allowed` 缺省 false(缺省即不授权,fail-closed)。

## authz / redact 扩展

- `authz`:新增 `ActionDelete`,与 ActionExport 同为 owner-only;其余沿用。
- `redact`:新增 `Note(content)`(只输出 `note=redacted(len=N)`,内容零泄漏)与
  `TagSummary(tags)`(只输出标签个数);`Person/MaskPhone/MaskEmail` 不变。

## web(Next.js,BFF 不加判断)

- `src/lib/contact.ts` 纯函数 + vitest:consent 状态语义(撤销=「已撤销(不可恢复)」)、
  权限显隐镜像(canEdit=owner|assignee、canExport/canDelete=owner、disabled 一律否)、
  `isPhoneLike` 搜索输入守卫(镜像服务端 400)、标签解析。
- `/contacts` 列表页:姓名/标签搜索(无手机号输入)、新建表单、owner 导出按钮。
- `/contacts/[id]` 详情页:基本信息/联系方式(展示层)/标签/备注/consent 来源明细
  (含各来源撤销状态与撤销按钮)/跟进时间线/追加跟进;编辑与「停止营销」按钮按
  whoami(role×assignee)显隐;删除按钮仅 owner。
- 首页入口加「客户档案」链接。

## 测试先行(TDD)

Go(`httpapi/contact_profile_test.go`,沿用 harness;新增 captureLog 选项与 doRaw):
1. **consent 生命周期**:两来源并存 → 撤销来源 A → B 的 marketing 权限不受影响;
   重放 A 键 → 200 且 revoked_at 逐字节不变;重复撤销幂等;
   revoke-marketing 批量撤销并幂等。
2. **权限矩阵**(consents/followups/export/delete 全覆盖):
   owner A ✓;assignee sales ✓;非 assignee sales 404;owner B 跨租户 403;
   disabled 403;sales/agent export 403;delete 仅 owner。
3. **日志脱敏断言**:captureLog 下造含手机号的联系人 + 含标记串的跟进 + 导出,
   断言日志不含手机号明文/跟进标记串,含导出审计行与掩码形态。
4. **URL 纪律**:`?phone=` 与疑似手机号搜索值 → 400。
5. **软删除**:删除后 404、列表/导出不见、consents/followups 行保留(直查 DB)。
6. **停用成员**:403;其历史 followups 仍对 owner 可见。
7. **保存/重登恢复**:consent+followup 跨进程重启仍在(仿 TestPersistenceAcrossRestart)。
8. **身份纪律**:CRM 写路径不改 members 行数(沿用断言覆盖新端点)。
9. db_test 表清单扩展 contact_consents/contact_followups;redact 新用例。

vitest:contact.ts 语义守卫(撤销文案不可恢复、无「自动开户」措辞、权限镜像、
isPhoneLike 守卫)。`go test ./...`(62+ 现有用例不劣化)、vitest、next build 全绿。

## 自查清单(code-review)

无自动开户 / 无合并端点 / 无去重规则 / 无接收渠道;无手机号入日志或 URL;
consent 撤销无任何恢复路径;导出与删除均 owner-only 且留审计;
BFF 零业务判断;不做触达/同步。
