# HUI-1688 统一接待核心 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 leads-engine 落地统一接待核心 V1，挡住数字人 HUI-20 的业务会话可以复用同一套知识、接管和留资。

**Architecture:** `FEATURE_RECEPTION` 默认关闭。打开后，纯函数 `reception.Compose` 决定依据和不足项，SQLite 保存会话 epoch 与回复生命周期，HTTP 分公共 H5 和员工工作台。价格只来自 `FactBook`。用量行 `live_charge` 恒为 0。

**Tech Stack:** Go net/http、SQLite、Next.js。

**Spec:** `docs/superpowers/specs/2026-09-27-hui-1688-reception-core-design.md`

## Global Constraints

- 不向真实客户扣费，不配置扣费 URL。
- 匿名访客不注册 principal，默认不进营销池。
- 人设不进入回复正文。
- 文本和音频共用 `MayDeliver`。
- 结案和售后不建线索。只有 `sales_followup` 且允许联系才调用 `IntakeLeadInTx`。
- 公共租户只来自 widget 归属。

## Tasks

### Task 1: 领域与迁移

**Files:**
- Create: `server/internal/db/migrations/0012_reception.sql`
- Create: `server/internal/reception/engine.go`
- Test: `server/internal/reception/engine_test.go`

- [ ] 先写失败测试：FAQ 引用、人设不进正文、过期价格不编造、退款不发送、接管后文本和音频都不可发。
- [ ] 实现 `Compose` 与 `MayDeliver`。
- [ ] `go test ./internal/reception/`

### Task 2: 存储、HTTP 与工作台

**Files:**
- Create: `server/internal/store/reception.go`
- Create: `server/internal/httpapi/handlers_reception.go`
- Test: `server/internal/httpapi/reception_test.go`
- Modify: `server/internal/httpapi/server.go`
- Modify: `server/internal/config/config.go`
- Modify: `server/internal/authz/authz.go`

- [ ] 两租户隔离、重复消息不重复用量和线索、H5 到人工到授权跟进、`live_charge=1` 被拒绝。
- [ ] `go test ./...`

### Task 3: H5 与接待工作台

**Files:**
- Create: `web/src/app/r/[id]/page.tsx`
- Create: `web/src/app/reception/page.tsx`
- Test: `web/tests/reception.test.ts`

- [ ] 浏览器走通一条 H5 提问。
- [ ] `npm test`
