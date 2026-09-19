// handler_service_draft_test.go — HUI-1749 HTTP surface tests.
//
// 覆盖(对票面验收逐条):
//   - 类别隔离:merchant_customer 调任一 service-draft 端点 → 422;
//   - 预览零持久化 + 缺失标记;确认幂等(双击同引用);
//   - 投递丢失 → 快照保留 → retry 原字节同引用恢复;
//   - 内容变更 → 新版本新引用;旧版本显式 superseded;
//   - 撤销守卫三态(draft 可撤 / 已接受引导变更流 / 接收端不可达 fail-closed);
//   - 接收端 409 同键异内容:本地快照不覆盖;422 显式上浮;
//   - 载荷最小化:联系人档案/跟进字段绝不进线上字节;
//   - 投递成功 ≠ 成交:响应文案无「已成交」,商机状态不变。
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ---- stub receiver (O2-shaped intake + projection) ------------------------------

const ecoToken = "test-eco-token"

type ecoStub struct {
	mu           sync.Mutex
	url          string
	seen         map[string]string // handoff_id -> doc sha256
	force422     string            // non-empty → handoffs answers 422 with this code
	targetStatus string            // projection status override ("" → "draft")
	down         bool              // true → abort connection (transport failure)
	hits         int               // handoffs POST count
	lastBody     []byte
}

func newEcoStub(t *testing.T) (*ecoStub, string) {
	t.Helper()
	st := &ecoStub{seen: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/internal/eco/leads/handoffs", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.hits++
		body, _ := io.ReadAll(r.Body)
		st.lastBody = body
		down, force := st.down, st.force422
		st.mu.Unlock()
		if down {
			panic(http.ErrAbortHandler) // no response → transport failure
		}
		if r.Header.Get("X-Internal-Token") != ecoToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sum := sha256hexEco(body)
		var doc struct {
			HandoffID string   `json:"handoff_id"`
			SourceRef string   `json:"source_ref"`
			Scopes    []string `json:"scopes"`
		}
		_ = json.Unmarshal(body, &doc)
		st.mu.Lock()
		prev, seen := st.seen[doc.HandoffID]
		if !seen {
			st.seen[doc.HandoffID] = sum
		}
		st.mu.Unlock()
		if seen && prev != sum {
			// 同键异内容:O2 语义 = 409 HANDOFF_ID_CONFLICT。
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": "HANDOFF_ID_CONFLICT", "message": "same handoff_id, different content"},
			})
			return
		}
		if force != "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": force, "message": "forced by test"},
			})
			return
		}
		code := http.StatusCreated
		if seen {
			code = http.StatusOK
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
			"handoff_id": doc.HandoffID, "draft_ref": "demand:1", "status": "draft",
			"duplicate": seen, "dirty": false, "revoked": false,
			"source_app": "leads-engine", "source_kind": "standalone",
			"source_ref": doc.SourceRef, "missing": []any{}, "scopes": doc.Scopes,
		}})
	})
	mux.HandleFunc("POST /api/v1/internal/eco/leads/projection", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.down {
			panic(http.ErrAbortHandler)
		}
		if r.Header.Get("X-Internal-Token") != ecoToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in struct {
			HandoffID string `json:"handoff_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if _, ok := st.seen[in.HandoffID]; !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "HANDOFF_NOT_FOUND"}})
			return
		}
		status := st.targetStatus
		if status == "" {
			status = "draft"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"projection": map[string]any{
			"handoff_id": in.HandoffID, "draft_ref": "demand:1", "status": status,
			"dirty": false, "revoked": false, "source_app": "leads-engine",
			"source_ref": "opp-under-test", "updated_at": 1,
		}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	st.url = srv.URL + "/api/v1/internal/eco/leads/handoffs"
	return st, st.url
}

func sha256hexEco(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (st *ecoStub) poison(id, sum string) {
	st.mu.Lock()
	st.seen[id] = sum
	st.mu.Unlock()
}

func (st *ecoStub) reset() {
	st.mu.Lock()
	st.seen = map[string]string{}
	st.mu.Unlock()
}

// ---- fixtures -----------------------------------------------------------------

const ecoHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func ecoConfirmBody(summary string) string {
	return `{"confirm":true,"summary":` + jsonQuote(summary) + `,"service_category":"video-editing","budget_cents":150000,"deadline":"2026-10-31",` +
		`"assets":[{"asset_ref":"brand/logo.png","sha256":"` + ecoHex + `","size_bytes":1024,"media_type":"image/png"}]}`
}

func ecoPreviewBody(summary string) string {
	return strings.Replace(ecoConfirmBody(summary), `"confirm":true`, `"confirm":false`, 1)
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func ecoPath(oppID, suffix string) string {
	return "/api/v1/opportunities/" + oppID + "/service-draft" + suffix
}

func handoffIDOf(t *testing.T, out map[string]any) string {
	t.Helper()
	h, _ := out["handoff"].(map[string]any)
	if h == nil {
		t.Fatalf("no handoff object in %v", out)
	}
	id, _ := h["handoff_id"].(string)
	if id == "" {
		t.Fatalf("no handoff_id in %v", h)
	}
	return id
}

// ---- tests ---------------------------------------------------------------------

func TestServiceDraftCategoryIsolation(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{ecoOn: true})
	tenantA, _, contactA1 := h.seed()
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "商家经营商机", "merchant_customer", "")

	// 动作对不适用类别不存在:任一端点都 422,绝不落到投递/配置层。
	for _, tc := range []struct {
		method, path, body string
	}{
		{"POST", ecoPath(opp, "-intent"), `{}`},
		{"POST", ecoPath(opp, "-intent"), ecoConfirmBody("试试往商家商机上塞草稿")},
		{"GET", ecoPath(opp, ""), ""},
		{"POST", ecoPath(opp, "/refresh"), `{}`},
		{"POST", ecoPath(opp, "/retry"), `{}`},
		{"POST", ecoPath(opp, "/revoke"), `{}`},
	} {
		out := h.mustDo(tc.method, tc.path, sessionSalesA1, tenantA, tc.body, http.StatusUnprocessableEntity)
		if out["error"] != "category_not_applicable" {
			t.Fatalf("%s %s: error = %v, want category_not_applicable", tc.method, tc.path, out["error"])
		}
	}
	if hits := func() int { h.eco.mu.Lock(); defer h.eco.mu.Unlock(); return h.eco.hits }(); hits != 0 {
		t.Fatalf("receiver saw %d posts, want 0", hits)
	}
}

func TestServiceDraftPreviewAndConfirmIdempotent(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{ecoOn: true})
	tenantA, _, contactA1 := h.seed()
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "宣传片需求", "creative_service", "")
	summary := "需要一支 60 秒品牌宣传片剪辑,竖版两版"

	t.Run("preview renders facts, receiver and missing marks; zero persistence", func(t *testing.T) {
		out := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoPreviewBody(summary), http.StatusOK)
		pv, _ := out["preview"].(map[string]any)
		if pv == nil {
			t.Fatalf("no preview in %v", out)
		}
		if pv["summary"] != summary || pv["service_category"] != "video-editing" || pv["budget_cents"] == nil {
			t.Fatalf("preview facts incomplete: %v", pv)
		}
		if m, _ := pv["missing"].([]any); len(m) != 0 {
			t.Fatalf("missing = %v, want empty (everything provided)", m)
		}
		rc, _ := pv["receiver"].(map[string]any)
		if rc == nil || rc["target_app"] != "orders" || rc["return_target_id"] != "rc-leads-engine-main" {
			t.Fatalf("receiver facts = %v", rc)
		}
		// 预览零持久化:GET 必须还是 404。
		h.mustDo("GET", ecoPath(opp, ""), sessionSalesA1, tenantA, "", http.StatusNotFound)
	})

	t.Run("preview with nothing provided marks missing instead of guessing", func(t *testing.T) {
		out := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, `{}`, http.StatusOK)
		pv, _ := out["preview"].(map[string]any)
		got := map[string]bool{}
		for _, m := range pv["missing"].([]any) {
			got[m.(string)] = true
		}
		for _, want := range []string{"summary", "budget_cents", "deadline", "assets"} {
			if !got[want] {
				t.Fatalf("missing marks = %v, want %s", got, want)
			}
		}
		if s, _ := pv["note"].(string); !strings.Contains(s, "不代表成交") || !strings.Contains(s, "猜测") {
			t.Fatalf("preview note missing guard copy: %q", s)
		}
	})

	t.Run("confirm delivers one snapshot; opportunity stays untouched", func(t *testing.T) {
		before := h.mustDo("GET", "/api/v1/opportunities/"+opp, sessionSalesA1, tenantA, "", http.StatusOK)
		out := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoConfirmBody(summary), http.StatusCreated)
		hid := handoffIDOf(t, out)
		hh, _ := out["handoff"].(map[string]any)
		if hh["local_status"] != "delivered" || hh["draft_ref"] != "demand:1" || hh["target_status"] != "draft" {
			t.Fatalf("handoff projection = %v", hh)
		}
		if out["duplicate"] != false || out["delivered"] != true {
			t.Fatalf("flags = %v/%v, want false/true", out["duplicate"], out["delivered"])
		}
		if s, _ := hh["note"].(string); strings.Contains(s, "已成交") {
			t.Fatalf("note carries deal wording: %q", s)
		}
		// 投递成功 ≠ 成交:商机状态/类别/金额一律不变(无自动 won)。
		after := h.mustDo("GET", "/api/v1/opportunities/"+opp, sessionSalesA1, tenantA, "", http.StatusOK)
		if after["stage"] != before["stage"] || after["business_category"] != before["business_category"] ||
			after["amount_cents"] != before["amount_cents"] || after["updated_at"] != before["updated_at"] {
			t.Fatalf("opportunity changed by delivery: before=%v after=%v", before, after)
		}

		// 双击/重放:同内容 → 200 幂等恢复同引用,接收端只见过一次 POST。
		out2 := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoConfirmBody(summary), http.StatusOK)
		if out2["duplicate"] != true || handoffIDOf(t, out2) != hid {
			t.Fatalf("double confirm = %v (id %s), want duplicate same %s", out2["duplicate"], handoffIDOf(t, out2), hid)
		}
		h.eco.mu.Lock()
		hits := h.eco.hits
		h.eco.mu.Unlock()
		if hits != 1 {
			t.Fatalf("receiver saw %d posts, want 1", hits)
		}

		// 载荷最小化:联系人档案/跟进字段绝不进线上字节。
		h.eco.mu.Lock()
		raw := string(h.eco.lastBody)
		h.eco.mu.Unlock()
		for _, banned := range []string{"13812345678", "jia@shop.cn", "甲商家", "phone", "email", "notes", "followup", "tags", "consent"} {
			if strings.Contains(raw, banned) {
				t.Fatalf("wire payload leaks %q: %s", banned, raw)
			}
		}
	})
}

func TestServiceDraftLostDeliveryRetryRecoversSameReference(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{ecoOn: true, ecoIntakeDead: true})
	tenantA, _, contactA1 := h.seed()
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "拍摄需求", "creative_service", "")

	// 接收端不可达:确认仍成功保存快照(确认事实不丢失),显式可重试注记。
	out := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
		ecoConfirmBody("需要产品拍摄与剪辑"), http.StatusOK)
	hid := handoffIDOf(t, out)
	hh, _ := out["handoff"].(map[string]any)
	if out["delivered"] != false || hh["local_status"] != "delivery_failed" {
		t.Fatalf("lost delivery confirm = %v / %v", out["delivered"], hh["local_status"])
	}
	h.mustDo("GET", ecoPath(opp, ""), sessionSalesA1, tenantA, "", http.StatusOK) // 投影可读

	// 恢复接收端 → retry:原字节重发,同 handoff 引用。
	h.api.Eco.Deliver.IntakeURL = h.eco.url
	out2 := h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA, "", http.StatusOK)
	if out2["delivered"] != true || handoffIDOf(t, out2) != hid {
		t.Fatalf("retry = delivered %v id %s, want true / %s", out2["delivered"], handoffIDOf(t, out2), hid)
	}
	h.eco.mu.Lock()
	hits := h.eco.hits
	h.eco.mu.Unlock()
	if hits != 1 {
		t.Fatalf("receiver saw %d posts, want exactly the one retry", hits)
	}
}

func TestServiceDraftChangedContentNewVersionAndSuperseded(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{ecoOn: true})
	tenantA, _, contactA1 := h.seed()
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "剪辑需求", "creative_service", "")

	v1 := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
		ecoConfirmBody("基础剪辑需求"), http.StatusCreated)
	v1id := handoffIDOf(t, v1)

	// 内容变更 = 新确认 = 新版本新 handoff_id(绝不覆盖)。
	v2 := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
		ecoConfirmBody("基础剪辑需求(追加:需要竖版两版)"), http.StatusCreated)
	v2id := handoffIDOf(t, v2)
	if v1id == v2id {
		t.Fatal("changed content must mint a new handoff_id")
	}
	latest := h.mustDo("GET", ecoPath(opp, ""), sessionSalesA1, tenantA, "", http.StatusOK)
	lh, _ := latest["handoff"].(map[string]any)
	if lh["source_version"] != float64(2) || lh["handoff_id"] != v2id {
		t.Fatalf("latest = %v, want version 2 / %s", lh, v2id)
	}

	// 旧版本重试:显式 superseded 拒绝,不静默重发。
	out := h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA,
		`{"handoff_id":"`+v1id+`"}`, http.StatusConflict)
	if out["error"] != "superseded_version" {
		t.Fatalf("error = %v, want superseded_version", out["error"])
	}

	// 裸重试 = 重发最新快照。
	h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA, "", http.StatusOK)

	// 与 v1 完全同内容再次确认:指纹命中 → 仍返回 v1 原引用(幂等按内容,不按时间)。
	again := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
		ecoConfirmBody("基础剪辑需求"), http.StatusOK)
	if again["duplicate"] != true || handoffIDOf(t, again) != v1id {
		t.Fatalf("same-content reconfirm = %v id %s, want duplicate v1 %s", again["duplicate"], handoffIDOf(t, again), v1id)
	}
}

func TestServiceDraftRevokeGuard(t *testing.T) {
	newOpp := func(t *testing.T, h *harness) (string, string) {
		t.Helper()
		tenantA, _, contactA1 := h.seed()
		opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "撤销样例", "creative_service", "")
		return tenantA, opp
	}

	t.Run("target still draft: revoke allowed; retry/refirm then refuse", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{ecoOn: true})
		tenantA, opp := newOpp(t, h)
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoConfirmBody("待撤销的需求"), http.StatusCreated)

		out := h.mustDo("POST", ecoPath(opp, "/revoke"), sessionSalesA1, tenantA, "", http.StatusOK)
		hh, _ := out["handoff"].(map[string]any)
		if hh["local_status"] != "revoked" || hh["revoked_at"] == nil {
			t.Fatalf("revoke projection = %v", hh)
		}
		h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA, "", http.StatusConflict)
		if out := h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA, "", http.StatusConflict); out["error"] != "handoff_revoked" {
			t.Fatalf("retry error = %v, want handoff_revoked", out["error"])
		}
		if out := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoConfirmBody("待撤销的需求"), http.StatusConflict); out["error"] != "handoff_revoked" {
			t.Fatalf("refirm error = %v, want handoff_revoked", out["error"])
		}
		// 撤销幂等。
		out2 := h.mustDo("POST", ecoPath(opp, "/revoke"), sessionSalesA1, tenantA, "", http.StatusOK)
		h2, _ := out2["handoff"].(map[string]any)
		if h2["local_status"] != "revoked" {
			t.Fatalf("idempotent revoke = %v", h2)
		}
		// 新内容 = 新确认,可以继续(生成新版本新引用)。
		changed := ecoConfirmBody("撤销后的新需求(换方案)")
		out3 := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, changed, http.StatusCreated)
		if handoffIDOf(t, out3) == handoffIDOf(t, out) {
			t.Fatal("post-revoke confirmation must mint a new reference")
		}
	})

	t.Run("target accepted: revoke refused, guided to the target change flow", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{ecoOn: true})
		tenantA, opp := newOpp(t, h)
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoConfirmBody("已被接受的需求"), http.StatusCreated)
		h.eco.mu.Lock()
		h.eco.targetStatus = "accepted" // 接单侧已离开草稿
		h.eco.mu.Unlock()
		h.mustDo("POST", ecoPath(opp, "/refresh"), sessionSalesA1, tenantA, "", http.StatusOK)

		out := h.mustDo("POST", ecoPath(opp, "/revoke"), sessionSalesA1, tenantA, "", http.StatusConflict)
		if out["error"] != "accepted_change_via_target" {
			t.Fatalf("error = %v, want accepted_change_via_target", out["error"])
		}
		if s, _ := out["message"].(string); !strings.Contains(s, "变更") {
			t.Fatalf("refusal must guide to the target change flow: %q", s)
		}
	})

	t.Run("receiver unreachable: fail-closed, no blind revoke", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{ecoOn: true})
		tenantA, opp := newOpp(t, h)
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoConfirmBody("接收端失联样例"), http.StatusCreated)
		h.eco.mu.Lock()
		h.eco.down = true
		h.eco.mu.Unlock()

		out := h.mustDo("POST", ecoPath(opp, "/revoke"), sessionSalesA1, tenantA, "", http.StatusServiceUnavailable)
		if out["error"] != "target_state_unconfirmed" {
			t.Fatalf("error = %v, want target_state_unconfirmed", out["error"])
		}
	})
}

func TestServiceDraftReceiverConflictAndRejection(t *testing.T) {
	t.Run("409 same key different content: local snapshot never overwritten", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{ecoOn: true})
		tenantA, _, contactA1 := h.seed()
		opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "冲突样例", "creative_service", "")
		first := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
			ecoConfirmBody("冲突前内容"), http.StatusCreated)
		hid := handoffIDOf(t, first)

		h.eco.poison(hid, strings.Repeat("ff", 32)) // 同键异内容
		out := h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA, "", http.StatusConflict)
		if out["error"] != "handoff_id_conflict" {
			t.Fatalf("error = %v, want handoff_id_conflict", out["error"])
		}
		snap := h.mustDo("GET", ecoPath(opp, ""), sessionSalesA1, tenantA, "", http.StatusOK)
		sh, _ := snap["handoff"].(map[string]any)
		if sh["handoff_id"] != hid || sh["local_status"] != "delivered" {
			t.Fatalf("snapshot disturbed: %v", sh)
		}
		if s, _ := sh["last_error"].(string); s == "" {
			t.Fatalf("conflict must be recorded for human disposition")
		}

		// 解毒后重试:同一引用恢复(快照从未被覆盖)。
		h.eco.reset()
		out2 := h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA, "", http.StatusOK)
		if out2["delivered"] != true || handoffIDOf(t, out2) != hid {
			t.Fatalf("post-conflict retry = %v id %s, want delivered same %s", out2["delivered"], handoffIDOf(t, out2), hid)
		}
	})

	t.Run("422 machine rejection surfaces; snapshot retained for retry", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{ecoOn: true})
		tenantA, _, contactA1 := h.seed()
		opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "拒绝样例", "creative_service", "")
		h.eco.mu.Lock()
		h.eco.force422 = "TENANT_MISMATCH"
		h.eco.mu.Unlock()

		out := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
			ecoConfirmBody("会被拒的内容"), http.StatusUnprocessableEntity)
		if out["error"] != "receiver_rejected" {
			t.Fatalf("error = %v, want receiver_rejected", out["error"])
		}
		snap := h.mustDo("GET", ecoPath(opp, ""), sessionSalesA1, tenantA, "", http.StatusOK)
		sh, _ := snap["handoff"].(map[string]any)
		if sh["handoff_id"] == "" || sh["local_status"] == "delivered" {
			t.Fatalf("snapshot must be retained undelivered: %v", sh)
		}

		h.eco.mu.Lock()
		h.eco.force422 = ""
		h.eco.mu.Unlock()
		out2 := h.mustDo("POST", ecoPath(opp, "/retry"), sessionSalesA1, tenantA, "", http.StatusOK)
		if out2["delivered"] != true {
			t.Fatalf("retry after unforce = %v", out2)
		}
	})
}

func TestServiceDraftNegatives(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{ecoOn: true})
	tenantA, tenantB, contactA1 := h.seed()
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "权限样例", "creative_service", "")

	t.Run("wrong tenant masked as 404", func(t *testing.T) {
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionOwnerB, tenantB, `{}`, http.StatusNotFound)
		h.mustDo("GET", ecoPath(opp, ""), sessionOwnerB, tenantB, "", http.StatusNotFound)
	})

	t.Run("same tenant non-assignee masked as 404", func(t *testing.T) {
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA2, tenantA, `{}`, http.StatusNotFound)
	})

	t.Run("owner and assignee may act", func(t *testing.T) {
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionOwnerA, tenantA, ecoPreviewBody("owner 预览"), http.StatusOK)
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA, ecoPreviewBody("assignee 预览"), http.StatusOK)
	})

	t.Run("unauthenticated 401 on every endpoint", func(t *testing.T) {
		h.mustDo("POST", ecoPath(opp, "-intent"), "", tenantA, `{}`, http.StatusUnauthorized)
		h.mustDo("GET", ecoPath(opp, ""), "", tenantA, "", http.StatusUnauthorized)
		h.mustDo("POST", ecoPath(opp, "/revoke"), "", tenantA, `{}`, http.StatusUnauthorized)
	})

	t.Run("unauthorized asset reference (missing user-confirmed hash) rejected", func(t *testing.T) {
		out := h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
			`{"confirm":true,"summary":"缺 hash 的资产","service_category":"video-editing",
			  "assets":[{"asset_ref":"brand/logo.png","size_bytes":1024,"media_type":"image/png"}]}`,
			http.StatusBadRequest)
		if s, _ := out["message"].(string); !strings.Contains(s, "sha256") {
			t.Fatalf("message = %v, want sha256 guidance", out["message"])
		}
		// 预览同样拒绝不完整资产引用(而不是默默替用户补齐)。
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
			`{"confirm":false,"summary":"预览缺 hash","service_category":"video-editing",
			  "assets":[{"asset_ref":"brand/logo.png","size_bytes":1024,"media_type":"image/png"}]}`,
			http.StatusBadRequest)
	})

	t.Run("malformed provided facts rejected in preview too", func(t *testing.T) {
		h.mustDo("POST", ecoPath(opp, "-intent"), sessionSalesA1, tenantA,
			`{"confirm":false,"summary":"x","service_category":"Not A Slug","deadline":"2026/10/31"}`, http.StatusBadRequest)
	})
}

func TestServiceDraftConfigGateEco(t *testing.T) {
	// 功能开、部署事实缺:五个端点全部 503 config_gate_eco(fail-closed)。
	h := newHarnessOpts(t, harnessOpts{featureServiceDraft: true})
	tenantA, _, contactA1 := h.seed()
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "配置门样例", "creative_service", "")

	for _, tc := range []struct{ method, path, body string }{
		{"POST", ecoPath(opp, "-intent"), ecoConfirmBody("配置缺失也绝不伪造投递")},
		{"GET", ecoPath(opp, ""), ""},
		{"POST", ecoPath(opp, "/refresh"), `{}`},
		{"POST", ecoPath(opp, "/retry"), `{}`},
		{"POST", ecoPath(opp, "/revoke"), `{}`},
	} {
		out := h.mustDo(tc.method, tc.path, sessionSalesA1, tenantA, tc.body, http.StatusServiceUnavailable)
		if out["error"] != "config_gate_eco" {
			t.Fatalf("%s %s: error = %v, want config_gate_eco", tc.method, tc.path, out["error"])
		}
	}
}
