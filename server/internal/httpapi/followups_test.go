// HUI-1692 / FEAT-0193 销售跟进记录管理(FEATURE_FOLLOWUPS 闸控)测试矩阵:
//   - 开关 off:全部路由不注册(404 不可见);on:CRUD 全路径;
//   - 记录挂 contact(必填)/lead(可选);记录级授权作用域 = 挂靠 lead(优先)
//     或 contact 的 assigned_member_id,沿用 L0(非 assignee 404 掩码,跨租户 403);
//   - next_follow_up_at:RFC3339 校验 + 服务端规范化 UTC(跨时区确定性到期排序);
//   - completed_at 语义:完结/重开幂等;完结记录不进「我的到期跟进」;
//   - 到期面:<=now 升序、完结排除、软删 contact 排除、只含当前成员分配的记录;
//   - PII:note 全文不入日志(只允许 redact.Note 长度摘要)、不入 URL。
package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newFollowupsHarness(t *testing.T) *harness {
	return newHarnessOpts(t, harnessOpts{featureFollowups: true})
}

func fuRFC3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fuCreate is the create helper: returns the decoded response body.
func fuCreate(t *testing.T, h *harness, session, tenant, body string, wantStatus int) map[string]any {
	t.Helper()
	return h.mustDo("POST", "/api/v1/follow-ups", session, tenant, body, wantStatus)
}

// ---- 开关:FEATURE_FOLLOWUPS 默认 off -> 路由不存在 -----------------------------

func TestFollowupsFeatureOffRoutesInvisible(t *testing.T) {
	h := newHarness(t) // 默认 off
	tenantA, _, contactA1 := h.seed()
	h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"note":"x"}`, contactA1), http.StatusNotFound)
	h.mustDo("GET", "/api/v1/follow-ups/due", sessionOwnerA, tenantA, "", http.StatusNotFound)
	h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups", sessionOwnerA, tenantA, "", http.StatusNotFound)
	h.mustDo("GET", "/api/v1/follow-ups/fup_none", sessionOwnerA, tenantA, "", http.StatusNotFound)
	h.mustDo("PATCH", "/api/v1/follow-ups/fup_none", sessionOwnerA, tenantA, `{"note":"y"}`, http.StatusNotFound)
	h.mustDo("POST", "/api/v1/follow-ups/fup_none/complete", sessionOwnerA, tenantA, "", http.StatusNotFound)
	h.mustDo("POST", "/api/v1/follow-ups/fup_none/reopen", sessionOwnerA, tenantA, "", http.StatusNotFound)
	h.mustDo("GET", "/api/v1/leads/lead_none/follow-ups", sessionOwnerA, tenantA, "", http.StatusNotFound)
}

// ---- CRUD 全路径 ---------------------------------------------------------------

func TestFollowupsCRUDLifecycle(t *testing.T) {
	h := newFollowupsHarness(t)
	tenantA, _, contactA1 := h.seed()

	t.Run("create on assigned contact; next_follow_up_at normalized to UTC", func(t *testing.T) {
		out := fuCreate(t, h, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":"首次电话沟通","next_follow_up_at":"2026-09-21T18:00:00+08:00"}`, contactA1),
			http.StatusCreated)
		id, _ := out["id"].(string)
		if !strings.HasPrefix(id, "fup_") {
			t.Fatalf("id prefix = %q, want fup_*", id)
		}
		if out["next_follow_up_at"] != "2026-09-21T10:00:00Z" {
			t.Fatalf("next_follow_up_at = %v, want UTC-normalized 2026-09-21T10:00:00Z", out["next_follow_up_at"])
		}
		if _, present := out["completed_at"]; present {
			t.Fatalf("new record must not carry completed_at, got %v", out["completed_at"])
		}
		if out["created_by"] == "" || out["tenant_id"] != tenantA || out["contact_id"] != contactA1 {
			t.Fatalf("create response missing identity fields: %v", out)
		}
	})

	t.Run("lead-attached create requires lead in tenant and matching contact", func(t *testing.T) {
		lead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, contactA1), http.StatusCreated)
		leadID, _ := lead["id"].(string)
		out := fuCreate(t, h, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"线索推进"}`, contactA1, leadID), http.StatusCreated)
		if out["lead_id"] != leadID {
			t.Fatalf("lead_id = %v, want %s", out["lead_id"], leadID)
		}
		// owner 建一个自己的 contact,用它配别人的 lead:归属不匹配必须 400。
		contactB := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			`{"name":"乙","business_category":"merchant_customer","source_type":"manual"}`, http.StatusCreated)
		contactBID, _ := contactB["id"].(string)
		fuCreate(t, h, sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"错配"}`, contactBID, leadID), http.StatusBadRequest)
		fuCreate(t, h, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"lead_id":"lead_missing","note":"幽灵线索"}`, contactA1), http.StatusBadRequest)
	})

	t.Run("create on unknown or soft-deleted contact is 400", func(t *testing.T) {
		fuCreate(t, h, sessionOwnerA, tenantA, `{"contact_id":"con_missing","note":"x"}`, http.StatusBadRequest)
	})

	t.Run("read one; unknown id is 404", func(t *testing.T) {
		out := fuCreate(t, h, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":"待查"}`, contactA1), http.StatusCreated)
		id, _ := out["id"].(string)
		got := h.mustDo("GET", "/api/v1/follow-ups/"+id, sessionSalesA1, tenantA, "", http.StatusOK)
		if got["id"] != id || got["note"] != "待查" {
			t.Fatalf("read one mismatch: %v", got)
		}
		h.mustDo("GET", "/api/v1/follow-ups/fup_none", sessionSalesA1, tenantA, "", http.StatusNotFound)
	})

	t.Run("patch edits note and next_follow_up_at; explicit null clears; completed_at untouched by patch", func(t *testing.T) {
		out := fuCreate(t, h, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":"初版","next_follow_up_at":"2026-01-01T00:00:00Z"}`, contactA1), http.StatusCreated)
		id, _ := out["id"].(string)
		h.mustDo("POST", "/api/v1/follow-ups/"+id+"/complete", sessionSalesA1, tenantA, "", http.StatusOK)
		patched := h.mustDo("PATCH", "/api/v1/follow-ups/"+id, sessionSalesA1, tenantA,
			`{"note":"修订版","next_follow_up_at":null}`, http.StatusOK)
		if patched["note"] != "修订版" {
			t.Fatalf("note not patched: %v", patched["note"])
		}
		if _, present := patched["next_follow_up_at"]; present {
			t.Fatalf("explicit null must clear next_follow_up_at, got %v", patched["next_follow_up_at"])
		}
		if _, present := patched["completed_at"]; !present {
			t.Fatal("patch must not alter completed_at semantics (still completed)")
		}
		// 编辑非法时间与空 note 均为 400。
		h.mustDo("PATCH", "/api/v1/follow-ups/"+id, sessionSalesA1, tenantA,
			`{"next_follow_up_at":"not-a-date"}`, http.StatusBadRequest)
		h.mustDo("PATCH", "/api/v1/follow-ups/"+id, sessionSalesA1, tenantA, `{"note":"  "}`, http.StatusBadRequest)
	})

	t.Run("per-contact list is paginated, newest first, with total", func(t *testing.T) {
		var newestID string
		for i := 1; i <= 5; i++ {
			out := fuCreate(t, h, sessionSalesA1, tenantA,
				fmt.Sprintf(`{"contact_id":%q,"note":"跟进-%d"}`, contactA1, i), http.StatusCreated)
			if i == 5 {
				newestID, _ = out["id"].(string)
			}
		}
		page1 := h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups?limit=2&offset=0",
			sessionSalesA1, tenantA, "", http.StatusOK)
		if int(page1["total"].(float64)) != 9 { // 4 earlier + 5 here
			t.Fatalf("total = %v, want 9", page1["total"])
		}
		items := page1["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("page1 len = %d, want 2", len(items))
		}
		first, _ := items[0].(map[string]any)
		if first["id"] != newestID {
			t.Fatalf("newest-first violated: first=%v want %s", first["id"], newestID)
		}
		page2 := h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups?limit=2&offset=2",
			sessionSalesA1, tenantA, "", http.StatusOK)
		if got := len(page2["items"].([]any)); got != 2 {
			t.Fatalf("page2 len = %d, want 2", got)
		}
		pageN := h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups?limit=2&offset=9",
			sessionSalesA1, tenantA, "", http.StatusOK)
		if got := len(pageN["items"].([]any)); got != 0 {
			t.Fatalf("past-end page len = %d, want 0", got)
		}
		// 分页参数确定性校验。
		h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups?limit=0", sessionSalesA1, tenantA, "", http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups?limit=201", sessionSalesA1, tenantA, "", http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups?limit=abc", sessionSalesA1, tenantA, "", http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups?offset=-1", sessionSalesA1, tenantA, "", http.StatusBadRequest)
	})

	t.Run("per-lead list returns only lead-attached records", func(t *testing.T) {
		lead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, contactA1), http.StatusCreated)
		leadID, _ := lead["id"].(string)
		fuCreate(t, h, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"线索面"}`, contactA1, leadID), http.StatusCreated)
		out := h.mustDo("GET", "/api/v1/leads/"+leadID+"/follow-ups", sessionSalesA1, tenantA, "", http.StatusOK)
		items := out["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("lead list len = %d, want 1", len(items))
		}
		row, _ := items[0].(map[string]any)
		if row["lead_id"] != leadID {
			t.Fatalf("lead list leaked non-lead record: %v", row["id"])
		}
	})

	t.Run("complete and reopen are idempotent; unknown id 404", func(t *testing.T) {
		out := fuCreate(t, h, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":"完结流"}`, contactA1), http.StatusCreated)
		id, _ := out["id"].(string)
		done := h.mustDo("POST", "/api/v1/follow-ups/"+id+"/complete", sessionSalesA1, tenantA, "", http.StatusOK)
		stamp, _ := done["completed_at"].(string)
		if stamp == "" {
			t.Fatal("complete must stamp completed_at")
		}
		again := h.mustDo("POST", "/api/v1/follow-ups/"+id+"/complete", sessionSalesA1, tenantA, "", http.StatusOK)
		if again["completed_at"] != stamp {
			t.Fatalf("re-complete must keep first stamp: %v vs %v", again["completed_at"], stamp)
		}
		reopened := h.mustDo("POST", "/api/v1/follow-ups/"+id+"/reopen", sessionSalesA1, tenantA, "", http.StatusOK)
		if _, present := reopened["completed_at"]; present {
			t.Fatalf("reopen must clear completed_at, got %v", reopened["completed_at"])
		}
		reagain := h.mustDo("POST", "/api/v1/follow-ups/"+id+"/reopen", sessionSalesA1, tenantA, "", http.StatusOK)
		if _, present := reagain["completed_at"]; present {
			t.Fatal("reopen on an open row must stay open")
		}
		h.mustDo("POST", "/api/v1/follow-ups/fup_none/complete", sessionSalesA1, tenantA, "", http.StatusNotFound)
		h.mustDo("POST", "/api/v1/follow-ups/fup_none/reopen", sessionSalesA1, tenantA, "", http.StatusNotFound)
	})

	t.Run("validation: note caps in runes and RFC3339 only", func(t *testing.T) {
		fuCreate(t, h, sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":%q}`, contactA1, strings.Repeat("进", 2000)), http.StatusCreated)
		fuCreate(t, h, sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":%q}`, contactA1, strings.Repeat("进", 2001)), http.StatusBadRequest)
		fuCreate(t, h, sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":""}`, contactA1), http.StatusBadRequest)
		for _, bad := range []string{"21/09/2026", "2026-13-01T00:00:00Z", "2026-09-21 10:00:00", ""} {
			fuCreate(t, h, sessionOwnerA, tenantA,
				fmt.Sprintf(`{"contact_id":%q,"note":"x","next_follow_up_at":%q}`, contactA1, bad), http.StatusBadRequest)
		}
	})
}

// ---- 「我的到期跟进」:排序、过滤、作用域 ------------------------------------------

func TestFollowupsDueSurface(t *testing.T) {
	h := newFollowupsHarness(t)
	tenantA, _, contactS1 := h.seed() // contactA1 assigned to sales1

	// sales2 的 contact。
	contactS2 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"乙商家","business_category":"merchant_customer","source_type":"manual"}`, http.StatusCreated)
	contactS2ID, _ := contactS2["id"].(string)
	sales2ID := h.memberID(tenantA, principalSalesA2)
	h.mustDo("PATCH", "/api/v1/contacts/"+contactS2ID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, sales2ID), http.StatusOK)

	// 挂在 sales2 的 contact 上、但 lead 分配给 sales1:lead 挂靠记录的作用域取 lead。
	leadS1 := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, contactS2ID), http.StatusCreated)
	leadS1ID, _ := leadS1["id"].(string)
	sales1ID := h.memberID(tenantA, principalSalesA1)
	h.mustDo("PATCH", "/api/v1/leads/"+leadS1ID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, sales1ID), http.StatusOK)

	now := time.Now().UTC()
	pastOld := fuRFC3339(now.Add(-3 * time.Hour))
	pastMid := fuRFC3339(now.Add(-2 * time.Hour))
	// 带 +08:00 偏移的过去时间(等效 UTC 比过去 1 小时更早 30 分钟)。
	pastShifted := now.Add(-90 * time.Minute).In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339)
	future := fuRFC3339(now.Add(24 * time.Hour))

	// sales1 作用域:contact 挂靠 + lead 挂靠 + 跨时区表达。
	fuContact := mkWithNext(t, h, sessionOwnerA, tenantA, contactS1, "", pastMid, "s1-contact")
	fuLead := mkWithNext(t, h, sessionOwnerA, tenantA, contactS2ID, fmt.Sprintf(`,"lead_id":%q`, leadS1ID), pastOld, "s1-lead")
	fuTZ := mkWithNext(t, h, sessionOwnerA, tenantA, contactS1, "", pastShifted, "s1-tz")
	// sales2 作用域与未来(未到期)。
	fuS2 := mkWithNext(t, h, sessionOwnerA, tenantA, contactS2ID, "", pastMid, "s2")
	_ = fuS2
	fuFuture := mkWithNext(t, h, sessionOwnerA, tenantA, contactS1, "", future, "not-due")
	_ = fuFuture

	dueIDs := func(session string) []string {
		out := h.mustDo("GET", "/api/v1/follow-ups/due", session, tenantA, "", http.StatusOK)
		items, _ := out["items"].([]any)
		var ids []string
		for _, it := range items {
			m, _ := it.(map[string]any)
			s, _ := m["id"].(string)
			ids = append(ids, s)
		}
		return ids
	}

	t.Run("due list is scoped to the caller's assignments, ascending, due-or-overdue", func(t *testing.T) {
		got := dueIDs(sessionSalesA1)
		// 升序(最早义务在前):fuLead(-3h) < fuContact(-2h) < fuTZ(-90m,+08:00 表达);
		// future 排除。
		want := []string{fuLead, fuContact, fuTZ}
		if len(got) != len(want) {
			t.Fatalf("sales1 due = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("sales1 due order = %v, want %v", got, want)
			}
		}
		gotS2 := dueIDs(sessionSalesA2)
		if len(gotS2) != 1 || gotS2[0] != fuS2 {
			t.Fatalf("sales2 due = %v, want [%s]", gotS2, fuS2)
		}
	})

	t.Run("completed records leave the due surface and return on reopen", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/follow-ups/"+fuContact+"/complete", sessionOwnerA, tenantA, "", http.StatusOK)
		got := dueIDs(sessionSalesA1)
		if len(got) != 2 || got[0] != fuLead || got[1] != fuTZ {
			t.Fatalf("completed must be excluded: %v", got)
		}
		h.mustDo("POST", "/api/v1/follow-ups/"+fuContact+"/reopen", sessionOwnerA, tenantA, "", http.StatusOK)
		if got = dueIDs(sessionSalesA1); len(got) != 3 {
			t.Fatalf("reopen must restore due: %v", got)
		}
	})

	t.Run("owner due surface is the owner's own assignments, not a tenant-wide view", func(t *testing.T) {
		if got := dueIDs(sessionOwnerA); len(got) != 0 {
			t.Fatalf("owner due must be empty (no records assigned to owner), got %v", got)
		}
	})
}

func mkWithNext(t *testing.T, h *harness, session, tenant, contactID, extra, when, note string) string {
	t.Helper()
	out := fuCreate(t, h, session, tenant,
		fmt.Sprintf(`{"contact_id":%q%s,"note":%q,"next_follow_up_at":%q}`, contactID, extra, note, when),
		http.StatusCreated)
	id, _ := out["id"].(string)
	return id
}

// ---- 权限矩阵:非 assignee 404 掩码 / 跨租户 / 停用 / agent grant / 软删 ----------

func TestFollowupsAuthzMatrix(t *testing.T) {
	h := newFollowupsHarness(t)
	tenantA, tenantB, contactA1 := h.seed()
	fuID := mkWithNext(t, h, sessionSalesA1, tenantA, contactA1, "", fuRFC3339(time.Now().UTC().Add(-time.Hour)), "sales1 的跟进")

	t.Run("non-assignee sales is masked 404 on every path", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/follow-ups/"+fuID, sessionSalesA2, tenantA, "", http.StatusNotFound)
		h.mustDo("PATCH", "/api/v1/follow-ups/"+fuID, sessionSalesA2, tenantA, `{"note":"劫持"}`, http.StatusNotFound)
		h.mustDo("POST", "/api/v1/follow-ups/"+fuID+"/complete", sessionSalesA2, tenantA, "", http.StatusNotFound)
		h.mustDo("POST", "/api/v1/follow-ups/"+fuID+"/reopen", sessionSalesA2, tenantA, "", http.StatusNotFound)
		h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/follow-ups", sessionSalesA2, tenantA, "", http.StatusNotFound)
		h.mustDo("POST", "/api/v1/follow-ups", sessionSalesA2, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":"越权写"}`, contactA1), http.StatusNotFound)
	})

	t.Run("cross tenant is 403 at session level", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/follow-ups/due", sessionOwnerB, tenantA, "", http.StatusForbidden)
		h.mustDo("GET", "/api/v1/follow-ups/"+fuID, sessionOwnerB, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerB, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":"越租户"}`, contactA1), http.StatusForbidden)
	})

	t.Run("disabled member and stranger are 403", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/follow-ups/due", sessionDisabled, tenantA, "", http.StatusForbidden)
		h.mustDo("GET", "/api/v1/follow-ups/due", sessionStranger, tenantA, "", http.StatusForbidden)
	})

	t.Run("agent: no grant is 403; per-tenant grant unlocks self-scoped work", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/follow-ups/due", sessionAgentA, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/admin/agent-grants", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"principal_ref":%q}`, principalAgentA), http.StatusCreated)
		agentContact := h.mustDo("POST", "/api/v1/contacts", sessionAgentA, tenantA,
			`{"name":"代理客","business_category":"merchant_customer","source_type":"manual"}`, http.StatusCreated)
		agentContactID, _ := agentContact["id"].(string)
		fuAgent := mkWithNext(t, h, sessionAgentA, tenantA, agentContactID, "", fuRFC3339(time.Now().UTC().Add(-time.Minute)), "代理跟进")
		due := h.mustDo("GET", "/api/v1/follow-ups/due", sessionAgentA, tenantA, "", http.StatusOK)
		items := due["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("agent due len = %d, want 1", len(items))
		}
		row, _ := items[0].(map[string]any)
		if row["id"] != fuAgent {
			t.Fatalf("agent due = %v, want [%s]", row["id"], fuAgent)
		}
		// grant 只解锁自己的租户。
		h.mustDo("GET", "/api/v1/follow-ups/due", sessionAgentA, tenantB, "", http.StatusForbidden)
	})

	t.Run("soft-deleted contact masks its follow-ups everywhere", func(t *testing.T) {
		delContact := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			`{"name":"将删","business_category":"merchant_customer","source_type":"manual"}`, http.StatusCreated)
		delID, _ := delContact["id"].(string)
		delLead := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, delID), http.StatusCreated)
		delLeadID, _ := delLead["id"].(string)
		delFu := mkWithNext(t, h, sessionOwnerA, tenantA, delID, "", fuRFC3339(time.Now().UTC().Add(-2*time.Hour)), "将删跟进")
		h.mustDo("DELETE", "/api/v1/contacts/"+delID, sessionOwnerA, tenantA, "", http.StatusOK)
		h.mustDo("GET", "/api/v1/follow-ups/"+delFu, sessionOwnerA, tenantA, "", http.StatusNotFound)
		h.mustDo("PATCH", "/api/v1/follow-ups/"+delFu, sessionOwnerA, tenantA, `{"note":"x"}`, http.StatusNotFound)
		h.mustDo("POST", "/api/v1/follow-ups/"+delFu+"/complete", sessionOwnerA, tenantA, "", http.StatusNotFound)
		h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"note":"x"}`, delID), http.StatusBadRequest)
		// 软删后也不得借仍存在的 lead 路径写入「影子记录」。
		fuCreate(t, h, sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"影子"}`, delID, delLeadID), http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/contacts/"+delID+"/follow-ups", sessionOwnerA, tenantA, "", http.StatusNotFound)
		// 到期面不再出现。
		due := h.mustDo("GET", "/api/v1/follow-ups/due", sessionOwnerA, tenantA, "", http.StatusOK)
		items := due["items"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m["id"] == delFu {
				t.Fatal("follow-up of a soft-deleted contact must not appear in the due surface")
			}
		}
	})
}

// ---- PII 纪律:note 全文不入日志 ---------------------------------------------------

func TestFollowupsLogsNeverCarryNoteContent(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureFollowups: true, captureLog: true})
	tenantA, _, contactA1 := h.seed()
	note := "机密跟进内容SECRET-987654"
	fuCreate(t, h, sessionSalesA1, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"note":%q,"next_follow_up_at":"2026-09-21T18:00:00+08:00"}`, contactA1, note),
		http.StatusCreated)
	logs := h.logs.String()
	if strings.Contains(logs, "SECRET-987654") || strings.Contains(logs, "机密跟进内容") {
		t.Fatalf("follow-up note content leaked into logs: %s", logs)
	}
	if !strings.Contains(logs, "note=redacted(len=") {
		t.Fatalf("expected redact.Note length summary in logs: %s", logs)
	}
}
