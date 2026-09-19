# 计划:HUI-1749【平台生态 L1】适用商机转创意服务需求——受限交接发送端

- 日期:2026-09-19
- 票:HUI-1749(L1);分支 `hui-1749-l1-opportunity-handoff`(基于 main=f500da9,本地提交,**不 push**)
- 对端:接单接收端 O2(HUI-1751)已合并于 guanlan-order origin/main;契约 public-ai origin/main=1f3c4f1(只读引用)
- brainstorm 指挥者已代答(拍板见票面);本计划把拍板落为工程事实,冲突点显式记录(见「自行决策」)

## 目标(本票只做)

对 `business_category=creative_service` 且权限满足的商机,提供**用户显式发起+确认**的
「创建服务需求草稿」:预览(缺失字段标缺失)→ 确认 → 按 E1 source-profile/v1 冻结线格式
构造受限交接文档 → 投递到登记的接单接收端;**幂等**(双击/超时/重启恢复同一 handoff 引用);
投影接单侧受限状态(**投递成功≠成交/已支付**);未接受可撤销、已接受引导走目标变更流程。
商家经营销售(merchant_customer)商机**不出现该动作**(API+UI 双侧断言)。

**不做**:AI 打分/自动触发、导出联系人档案或跟进历史、改写商机阶段语义、目标侧撤销触发面
(O2 内部接缝,归共测票)、E4 回执消费端装配。

## 硬边界(验收红线)

1. 无自动触发:唯一入口是用户在详情页显式 POST;won 阶段、商机创建、任何后台路径都不产生交接。
2. 最小授权:文档只含用户确认过的字段+显式选择的资产引用;**不含**联系人 phone/email/notes/
   tags/followups 任何字段(载荷断言测试钉住);CRM 备注绝不成为确认事实来源。
3. 幂等:同 opportunity+同内容指纹 → 同一 handoff_id 同字节重试;接收端同键同内容 200 duplicate、
   同键异内容 409 → 本地快照**绝不覆盖**;新内容 = 新确认 = 新快照新版本新 handoff_id。
4. 文案守卫:投影与 UI 绝不出现「已成交/已支付/已收款」;商机阶段/金额在交接全流程**不变**。
5. 开关语义:FEATURE_SERVICE_DRAFT 默认 off → 全部 6 条路由不注册(404 不可见);on → 真实实现。
6. 类别隔离:merchant_customer 商机调任一 service-draft 端点 → 422 category_not_applicable。

## 数据模型(迁移 0004,additive)

新表 `opportunity_handoffs`(一行 = 一次确认快照):
- 定位:opportunity_id、source_version(每商机单调递增)、fingerprint(确认内容 sha256)
- 线格式:handoff_id(UNIQUE)、doc_json(**精确线字节**,重试原样重发)、doc_sha256、source_revision(=fingerprint)
- 授权:principal_id、actor_issuer、actor_subject、target_app、return_target_id
- 状态:local_status(confirmed→delivered|delivery_failed;revoked 终态)、draft_ref、target_status、
  target_dirty、target_revoked、last_http_status、last_error
- 审计:confirmed_by、confirmed_at、delivered_at、revoked_at
- 约束:UNIQUE(opportunity_id, fingerprint)(同内容同快照)、UNIQUE(opportunity_id, source_version)

## 契约实现(新包 internal/handoffsender)

| 文件 | 内容 |
|---|---|
| `manifest.json` + `registry.go` | app-registry/v1 静态清单(go:embed + 严格加载:版本/未知字段/URL 纪律);登记 leads-engine(自身:能力 `leads.handoff`、return_target_id `rc-leads-engine-main`)与接收端 `orders`(guanlan-order,standalone);sender 事实全部从登记读 |
| `document.go` | ConfirmedInput(需求摘要/服务类别 slug/预算?/截止?/资产引用[])→ 冻结线格式字节;确定性构造;无 order_ref/stage_ref;`source_revision`=fingerprint;`brief_version`=str(version);window=issued+600s(≤900) |
| `validate.go` | 发送端自校验器(严格重解析:17 键封闭、重复键/未知键/null/尾随拒绝、actor.app_id=source_app、窗口 0<x≤900s、scopes 闭合枚举、profile 全属性);**共享向量内嵌 testdata/source-profile-vectors.json 独立通过**(4 handoff 结论+3 时钟+3 租户) |
| `deliver.go` | 投递客户端:POST 登记接收端 intake(配置注入,绝无客户端可传 URL);头 X-Internal-Token;原样重发存储字节;201 created / 200 duplicate / 409 HANDOFF_ID_CONFLICT(typed,不覆盖)/ 422 code(typed)/ 传输失败(可重试,快照保留);投影查询(POST 接收端 projection) |
| `service.go` | 确认编排:fingerprint 查重(命中→幂等返回,未投递则补投递)→ 未命中→新版本新 handoff_id→构建→自校验→落库→投递;同事务 UNIQUE 兜底并发双击;superseded 检测(旧版本重发显式拒绝);撤销守卫(目标 status=draft 可撤,非 draft → 明确「走接单侧变更流程」,接收端不可达 fail-closed 拒绝盲撤) |

### E1 对齐(O2 字段对照结论,逐条)

| 契约点 | L1 取值 | 依据 |
|---|---|---|
| schema_version | order-handoff/v1 | 冻结 |
| source_profile | source-profile/v1, source_kind=standalone,无 campaign_ref | O2 仅收 standalone |
| source_app | `leads-engine`(registry 自条目) | O2 `LookupApp` 白名单 |
| target_app | `orders`(registry 接收端条目;=O2 自我标识,其 receipt source_app 与测试夹具同值) | 自行决策 D1 |
| capabilities | `["leads.handoff"]` | O2 能力白名单 |
| constraints | `purpose:service_procurement`、`category:<slug>`(+可选 `budget_cents:`/`deadline:` 不透明引用) | O2 消费前缀约定 |
| tenant_scope | `ECO_HANDOFF_TENANT_SCOPE`(部署共配;空=fail-closed 不出单) | O2 精确比对 |
| principal_id / actor.subject | 调用者 principal_ref(真实主体) | 复用真实授权 |
| actor.issuer | PLATFORM_IDENTITY_BASE_URL(真实配置,不造假) | 自行决策 D2 |
| binding | binding_ref=`binding-<rand>`、proof_digest=确定性 sha256(salt 可配) | PROVISIONAL(identity HUI-656 域) |
| scopes | `["project.resume","asset.import"]` | 草稿导入最小面 |
| return_target_id | `rc-leads-engine-main` | O2 期望 #4 |
| assets | 用户显式引用(ref/sha256/size/media_type 全字段,空数组=纯文字需求) | 无档案导出 |

## API(Go server,全部 FEATURE_SERVICE_DRAFT 闸控 + ActionUpdate 权限 + 类别闸)

| 端点 | 说明 |
|---|---|
| `POST /opportunities/{id}/service-draft-intent` | **挂载点真实化**。body `confirm:false`=预览(200:摘要/已选资产/预算/截止/接收方/授权范围/missing 标缺失,零持久化);`confirm:true`=确认提交(201 新快照已投递 / 200 幂等恢复同引用) |
| `GET /opportunities/{id}/service-draft` | 最近快照受限投影(handoff_id/source_version/local_status/draft_ref/target_status/dirty/revoked;守卫文案字段) |
| `POST /opportunities/{id}/service-draft/refresh` | 实时拉接收端 projection 更新本地投影 |
| `POST /opportunities/{id}/service-draft/retry` | 重发**最新**快照原字节;带 `handoff_id` 且非最新 → 409 superseded_version |
| `POST /opportunities/{id}/service-draft/revoke` | 撤销守卫流(见 service.go);已接受 → 409 `accepted_change_via_target` |

配置(config.go):`ECO_HANDOFF_INTAKE_URL` / `ECO_HANDOFF_INTAKE_TOKEN` / `ECO_HANDOFF_TENANT_SCOPE` /
`ECO_HANDOFF_TARGET_APP`(默认 orders)/ `ECO_HANDOFF_PROOF_SALT`(可选)。
FEATURE_SERVICE_DRAFT=on 且 eco 配置缺失 → 该组端点 503 `config_gate_eco`(显式列键,fail-closed;非全局 Gate)。
签名:冻结 handoff schema `additionalProperties:false` 无签名字段——投递通道鉴权=内部令牌;
HMAC 属 E4 回执方向(O2→leads),非发送端职责(记录入报告)。

## web(Next.js,BFF 零判断透明转发)

- `src/lib/opportunity.ts` 纯函数:`canCreateServiceDraft`(creative_service ∧ ActionUpdate 镜像)、
  `serviceDraftMissingFields`、`projectionLine`(守卫文案:「仅状态投影——投递成功不代表成交/已支付」)、
  接收方/授权范围标签;vitest 守卫(merchant_customer 无动作、全量文案无「已成交/已支付/已收款」)。
- `/opportunities/[id]` 详情页:按钮按显隐规则渲染;预览面板(缺失标缺失)→「确认并提交到接单」→
  结果(handoff 引用+草稿引用+受限状态);retry/refresh/revoke 按钮;商机阶段区块在交接后原样不变。

## 测试先行(TDD)

Go handoffsender(纯面):golden 构造+自校验;共享向量 10 条独立通过;载荷最小化(键封闭集+无 PII 值);
指纹幂等(同输入同 handoff_id 同字节);新内容新版本+superseded;registry 事实来源;未登记 target 拒绝。
Go httpapi(harness 全链):off→404(扩既有 TestServiceDraftIntentMount);类别隔离(merchant_customer
六端点全 422;creative_service 全链通);双击并发幂等(同 handoff_id,接收端仅见同字节);超时/停机重试
(stub 黑洞→delivery_failed→retry 同字节→duplicate=true 同 draft_ref);重启恢复(重开库);负例矩阵
(错租户 404/非 assignee 404/未授权资产引用缺字段拒绝/旧版本 409/接收端 409 不覆盖);撤销守卫三态;
eco 配置门 503;投影后商机记录不变+无成交文案。
web vitest:纯函数+文案守卫+按钮显隐。
E2E 证据(_reports/hui-1749-l1/,gitignore):跨进程脚本(stub 接收端 + 假 identity + leads-server 实机)
跑幂等矩阵/负例/类别隔离;`go test`/`vitest`/`next build` 全量输出存档。

## 自行决策记录(无指挥者在场,依票面拍板延伸)

- D1 `target_app` 取 `orders`:O2 自我标识(receipt sourceApp 与其测试夹具均为 orders),非仓库名
  guanlan-order;从 registry 条目读,不改发送逻辑即可换。语义=接单应用。
- D2 actor.issuer 取 PLATFORM_IDENTITY_BASE_URL:复用真实身份配置;接收端不校验 issuer 值。
- D3 服务类别 `category:<slug>` 为**确认必填输入**(用户显式事实,不从备注/联系人猜测)。
- D4 资产引用必须用户逐条显式提供全字段(CRM 无素材库,缺 hash 的引用=未授权引用,拒绝);
  重试通道结构上不可能注入快照外资产。
- D5 预算/截止经 constraints 不透明引用+描述文本双通道携带(schema 无业务字段;O2 透传不解释)。
- D6 多租户→tenant_scope 映射:本期单一部署共配值(接单侧同款单值),多租户映射归共测/后续票。
