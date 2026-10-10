# Today 会话上下文与回复草稿 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 从今天的会话行看到来源/客户/会话上下文，把回复草稿保存到服务端且不发送，再回到同一条继续。

**Architecture:** 服务端在已有 Today 投影上补无线索会话的上下文，并新增只落 `generated` 草稿的接口。页面只用查询参数打开该会话并返回原焦点。缺 GoBoost、Motion、Matrix 时不改对应按钮的失败语义。

**Tech Stack:** Go net/http + SQLite store、Next.js BFF、vitest。

**Spec:** 本文件同时是设计裁定。普通问题已自答，不另开票。

## Global Constraints

- 真实发送仍走已有 `POST /reception/sessions/{id}/replies/{replyId}/send`，且必须已有回执授权；本切片不得把草稿标成 `sent`。
- 访客可见规则不变：`kind=draft` 且未发送的回复不进公开 transcript。
- 不编造客户姓名、渠道、活动或手机号。没有线索时来源保持未记录，上下文只声明「会话」，并询问客户姓名与来源。
- 不在 CRM 里创建视频、数字人、Motion 或矩阵工程。
- 不改 touch-engine。AG07 的活动/同意/来源仍由对端写入。
- 不 reset、不覆盖他人工作树。HUI-1688 历史树里的未提交规格保持不动。

## 裁定

- 无线索会话的下一步是「先写回复草稿」，`auto_call` 与 `auto_message` 保持 false。有线索时仍沿用该线索的下一步，只追加事实「会话」。
- 草稿接口：`POST /api/v1/workbench/sessions/{id}/reply-draft`，正文经 `SaveReplyDraft`，状态 `generated`、种类 `draft`。同一 `client_reply_id` 同正文为重复，异正文 409。
- 返回继续用 `/?focus=` 与 `/reception?session=&focus=`。查询值只接受最多 128 位的 token。
- BFF 补 `PUT`，否则轻文案保存到不了 Go。浏览器真实保存本轮不冒充已测。
- 本环境不能派第二个实现者，也不能做非作者两轴审查。审查缺口保留，不标通过。

### Task 1: 无线索会话的 Today 上下文

**Files:**
- Modify: `server/internal/workbench/workbench.go`
- Test: `server/internal/workbench/today_test.go`

- [x] 写失败测试 `TestTodaySessionWithoutLeadKeepsSessionContext`
- [x] 跑测试确认红
- [x] 实现 `ProjectSessionContext`、待处理摘要、`write_reply_draft`
- [x] 跑测试确认绿

### Task 2: 服务端保存回复草稿且不发送

**Files:**
- Modify: `server/internal/workbench/workbench.go`
- Modify: `server/internal/httpapi/handlers_workbench.go`
- Modify: `server/internal/httpapi/server.go`
- Test: `server/internal/httpapi/today_test.go`

- [x] 写失败 HTTP 测试
- [x] 跑测试确认 404
- [x] 实现保存、幂等、关闭会话拒绝、跨租户 404
- [x] 跑测试确认访客 transcript 不含草稿

### Task 3: 打开该会话并返回继续；BFF 转发 PUT

**Files:**
- Modify: `web/src/lib/workbench.ts`
- Modify: `web/src/app/(shell)/page.tsx`
- Modify: `web/src/app/reception/page.tsx`
- Modify: `web/src/app/api/[...path]/route.ts`
- Test: `web/tests/workbench.test.ts`
- Test: `web/tests/bff.test.ts`

- [x] 写 href / token / PUT 导出测试
- [x] 跑 vitest 确认红
- [x] 接上今日草稿保存与接待深链
- [x] 跑 vitest 确认绿
