# 接待工作台操作 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让现有接待工作台和访客页走完接管、人工回复、发送、交回、结案、受权留资和跟进。

**Architecture:** 请求形状和提交条件放在 `web/src/lib/reception.ts`，页面只渲染并调用已有 BFF。服务端 epoch 与线索资格不在前端放宽。

**Tech Stack:** Next.js 15、现有全局样式、Vitest、已有 Go 接待 API。

**Spec:** `docs/superpowers/specs/2026-09-28-hui-1688-reception-desk-ops-design.md`

## Global Constraints

- 只改现有接待工作台和访客页，不新建 CRM、知识库或公共服务。
- 售后和一般问答不建线索；留资必须目的 `sales_followup`、允许联系、姓名、电话、告知版本都由操作者填写。
- 跟进必须已有受权线索。
- 旧 epoch 未发送回复不能发送；以服务端 epoch 为准。
- 不接真实供应商，不扣费，不外呼，不伪造企微回调、post_id、支付凭证。
- 不关闭 HUI-20。
- 沿用现有页面样式，不引入 shadcn。

---

### Task 1: 工作台操作条件

**Files:**
- Modify: `web/src/lib/reception.ts`
- Test: `web/tests/reception.test.ts`

把接管、交回、人工回复、发送、批准、留资、跟进的路径和拒绝条件做成纯函数，并补测试。

### Task 2: 工作台与访客页

**Files:**
- Modify: `web/src/app/reception/page.tsx`
- Create: `web/src/app/reception/session-panel.tsx`
- Modify: `web/src/app/r/[id]/page.tsx`

工作台打开会话后调用上述函数。访客页刷新已发送回复。

### Task 3: 浏览器链

本机打开接待，H5 问营业时间看到依据，问价格不编造，工作台接管并发送人工回复，访客可见，再明确受权并跟进。
