// HUI-1749 service tests over a real sqlite store: fingerprint idempotency,
// version progression/supersede, delivery bookkeeping (O2 semantics via an
// in-process stub) and the revoke guard.
package handoffsender

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/appregistry"
	"github.com/bianjiefilm/leads-engine/server/internal/db"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// ---- O2-shaped receiver stub ---------------------------------------------------

// stubReceiver mimics the guanlan-order restricted intake semantics:
// 201 created / 200 duplicate (same key same content) / 409 HANDOFF_ID_CONFLICT
// (same key different content) / 422 machine rejection / projection query.
type stubReceiver struct {
	mu        sync.Mutex
	intakes   map[string]string // handoff_id -> doc sha256
	force422  string            // when set, answer 422 with this code
	forceDown bool              // answer 500 (transport-level failure)
	// targetStatus overrides the projection status (simulate acceptance).
	targetStatus string
	dropped      int // deliveries accepted then dropped (simulated lost response)
}

func newStub() *stubReceiver { return &stubReceiver{intakes: map[string]string{}} }

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *stubReceiver) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/internal/eco/leads/handoffs", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.forceDown {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if s.force422 != "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"` + s.force422 + `","reason":"stub"}`))
			return
		}
		id := t2body(body)
		digest := sha256hex(body)
		prev, seen := s.intakes[id]
		if seen {
			if prev != digest {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"HANDOFF_ID_CONFLICT","message":"同键异内容，原快照保留"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":{"handoff_id":"` + id + `","draft_ref":"demand:1","status":"draft","duplicate":true,"dirty":false,"revoked":false,"source_app":"leads-engine","source_kind":"standalone","source_ref":"opp_abc"}}`))
			return
		}
		s.intakes[id] = digest
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"result":{"handoff_id":"` + id + `","draft_ref":"demand:1","status":"draft","duplicate":false,"dirty":false,"revoked":false,"source_app":"leads-engine","source_kind":"standalone","source_ref":"opp_abc"}}`))
	})
	mux.HandleFunc("POST /api/v1/internal/eco/leads/projection", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		id := t2body(body)
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.intakes[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"NOT_FOUND","message":"接口不存在"}`))
			return
		}
		status := "draft"
		if s.targetStatus != "" {
			status = s.targetStatus
		}
		_, _ = w.Write([]byte(`{"projection":{"handoff_id":"` + id + `","draft_ref":"demand:1","status":"` + status + `","dirty":false,"revoked":false,"source_app":"leads-engine","source_ref":"opp_abc","updated_at":1}}`))
	})
	return mux
}

// t2body extracts the handoff id from the stored raw JSON without a full parse.
func t2body(raw []byte) string {
	i := strings.Index(string(raw), `"handoff_id":"`)
	if i < 0 {
		return ""
	}
	rest := raw[i+len(`"handoff_id":"`):]
	j := strings.Index(string(rest), `"`)
	return string(rest[:j])
}

// ---- harness -------------------------------------------------------------------

type srv struct {
	*Service
	stub *stubReceiver
}

func newService(t *testing.T, mutate func(cfg *Config, d *Deliverer)) *srv {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "leads.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	seedParents(t, database)
	stub := newStub()
	hs := httptest.NewServer(stub.handler())
	t.Cleanup(hs.Close)
	cfg := Config{
		TargetAppID: "orders",
		TenantScope: "tenant-77",
		IntakeURL:   hs.URL + "/api/v1/internal/eco/leads/handoffs",
		Token:       "test-token",
		ProofSalt:   "test-salt",
	}
	d := &Deliverer{IntakeURL: cfg.IntakeURL, Token: cfg.Token, Client: hs.Client()}
	if mutate != nil {
		mutate(&cfg, d)
	}
	reg, err := appregistry.Embedded()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return &srv{Service: &Service{
		St:       store.New(database),
		Registry: reg.WithSelf("leads-engine"),
		Deliver:  d,
		Cfg:      cfg,
		Now:      func() time.Time { return clock },
	}, stub: stub}
}

var ctx = context.Background()

// seedParents inserts the tenant/member/contact/opportunity FK parents the
// ledger references (opportunity = creative_service, assignee = mem_1).
func seedParents(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_a','T','2026-09-19T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		VALUES('mem_1','tnt_a','usr_sales_a1','sales',1,'S','mem_1','2026-09-19T00:00:00Z','2026-09-19T00:00:00Z')`); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO contacts(id,tenant_id,name,phone,email,business_category,source_type,consent_status,notes,tags,created_by,created_at,updated_at)
		VALUES('con_1','tnt_a','甲客户','','','creative_service','manual','granted','','','mem_1','2026-09-19T00:00:00Z','2026-09-19T00:00:00Z')`); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO opportunities(id,tenant_id,contact_id,title,stage,business_category,amount_cents,amount_source,probability,expected_close_at,assigned_member_id,created_by,created_at,updated_at)
		VALUES('opp_abc','tnt_a','con_1','品牌焕新视频','open','creative_service',NULL,'unknown',0,NULL,'mem_1','mem_1','2026-09-19T00:00:00Z','2026-09-19T00:00:00Z')`); err != nil {
		t.Fatalf("seed opportunity: %v", err)
	}
}

// Double-click / replay: same fingerprint ⇒ same snapshot, same handoff_id,
// receiver sees byte-identical documents only.
func TestConfirmIdempotentSameFingerprint(t *testing.T) {
	s := newService(t, nil)
	first, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://127.0.0.1:18101", "mem_1")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if first.Duplicate || !first.Delivered {
		t.Fatalf("first confirm = duplicate=%v delivered=%v", first.Duplicate, first.Delivered)
	}
	if first.Handoff.LocalStatus != StatusDelivered || first.Handoff.DraftRef != "demand:1" {
		t.Fatalf("first = %+v", first.Handoff)
	}

	// 双击(相同输入)→ 幂等恢复同引用。
	second, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://127.0.0.1:18101", "mem_1")
	if err != nil {
		t.Fatalf("re-confirm: %v", err)
	}
	if !second.Duplicate {
		t.Fatal("second confirm must be a duplicate hit")
	}
	if second.Handoff.HandoffID != first.Handoff.HandoffID {
		t.Fatalf("handoff id changed: %s != %s", second.Handoff.HandoffID, first.Handoff.HandoffID)
	}
	if second.Handoff.DocJSON != first.Handoff.DocJSON {
		t.Fatal("doc bytes must be identical across retries (byte-identical replay)")
	}
	rows, err := s.St.ListOpportunityHandoffs("opp_abc", "tnt_a")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d (%v), want 1", len(rows), err)
	}
}

// 响应丢失(黑洞)→ 快照保留 → retry 原样重发 → 接收端同键同内容幂等恢复。
func TestDeliveryLostThenRetryRecoversSameReference(t *testing.T) {
	s := newService(t, nil)
	res, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://127.0.0.1:18101", "mem_1")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Simulate a lost response: point the deliverer at a dead port and
	// redeliver the same snapshot bytes; the transport failure must surface
	// while the snapshot survives as delivery_failed.
	goodURL := s.Cfg.IntakeURL
	s.Deliver.IntakeURL = "http://127.0.0.1:1/none"
	if err := s.deliver(ctx, res.Handoff); err == nil {
		t.Fatal("expected the forced failure to surface")
	}
	s.Deliver.IntakeURL = goodURL // restore for the retry
	failed, _ := s.St.LatestOpportunityHandoff("opp_abc", "tnt_a")
	if failed.LocalStatus != StatusDeliveryFailed {
		t.Fatalf("status = %s, want delivery_failed (snapshot kept)", failed.LocalStatus)
	}
	storedBytes := failed.DocJSON

	// 重试:原字节原 id。
	retry, err := s.Retry(ctx, "opp_abc", "tnt_a", "")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !retry.Delivered || retry.Handoff.HandoffID != res.Handoff.HandoffID {
		t.Fatalf("retry = %+v", retry)
	}
	if retry.Handoff.DocJSON != storedBytes {
		t.Fatal("retry must re-send the exact stored bytes")
	}
	if retry.Handoff.DraftRef != "demand:1" {
		t.Fatalf("draft_ref = %q, want the same reference", retry.Handoff.DraftRef)
	}
}

// 内容变更 → 新版本新 handoff_id;旧版本显式 superseded,不可静默重发。
func TestChangedContentNewVersionAndSuperseded(t *testing.T) {
	s := newService(t, nil)
	v1, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1")
	if err != nil {
		t.Fatalf("confirm v1: %v", err)
	}
	changed := baseInput()
	changed.Summary += "(追加:需要竖版剪辑)"
	v2, err := s.Confirm(ctx, changed, "usr_sales_a1", "http://x", "mem_1")
	if err != nil {
		t.Fatalf("confirm v2: %v", err)
	}
	if v2.Handoff.HandoffID == v1.Handoff.HandoffID || v2.Handoff.SourceVersion != 2 {
		t.Fatalf("changed content must create a NEW snapshot: v1=%s v2=%s ver=%d",
			v1.Handoff.HandoffID, v2.Handoff.HandoffID, v2.Handoff.SourceVersion)
	}
	if v2.Duplicate {
		t.Fatal("changed content must not be a duplicate hit")
	}
	// 旧版本引用不可重发:显式 superseded。
	if _, err := s.Retry(ctx, "opp_abc", "tnt_a", v1.Handoff.HandoffID); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("old version retry err = %v, want ErrSuperseded", err)
	}
	// 不带引用的 retry 只发最新版。
	got, err := s.Retry(ctx, "opp_abc", "tnt_a", "")
	if err != nil || got.Handoff.SourceVersion != 2 {
		t.Fatalf("latest retry = %+v err=%v", got, err)
	}
	rows, _ := s.St.ListOpportunityHandoffs("opp_abc", "tnt_a")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (both snapshots preserved)", len(rows))
	}
}

// 撤销守卫三态:未接受可撤;已接受显式引导走接单侧变更;不可达 fail-closed。
func TestRevokeGuardThreeStates(t *testing.T) {
	t.Run("target still in draft -> revoke allowed", func(t *testing.T) {
		s := newService(t, nil)
		if _, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1"); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		revoked, err := s.Revoke(ctx, "opp_abc", "tnt_a")
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if revoked.LocalStatus != StatusRevoked {
			t.Fatalf("status = %s, want revoked", revoked.LocalStatus)
		}
		// 撤销后重试/再确认都被拒绝(不得复活已撤内容)。
		if _, err := s.Retry(ctx, "opp_abc", "tnt_a", ""); !errors.Is(err, ErrAlreadyRevoked) {
			t.Fatalf("retry after revoke = %v, want ErrAlreadyRevoked", err)
		}
		if _, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1"); !errors.Is(err, ErrAlreadyRevoked) {
			t.Fatalf("re-confirm after revoke = %v, want ErrAlreadyRevoked", err)
		}
	})
	t.Run("target accepted -> explicit change-flow refusal", func(t *testing.T) {
		s := newService(t, nil)
		if _, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1"); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		s.stub.mu.Lock()
		s.stub.targetStatus = "published" // 接单侧已离开草稿 = 已接受
		s.stub.mu.Unlock()
		_, err := s.Revoke(ctx, "opp_abc", "tnt_a")
		if !errors.Is(err, ErrAcceptedByTarget) {
			t.Fatalf("revoke after acceptance = %v, want ErrAcceptedByTarget", err)
		}
	})
	t.Run("receiver unreachable -> fail-closed, no blind revoke", func(t *testing.T) {
		s := newService(t, nil)
		if _, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1"); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		// 接单侧随后不可达:撤销守卫无法确认目标状态 → fail-closed 拒绝盲撤。
		s.Deliver.IntakeURL = "http://127.0.0.1:1/none"
		_, err := s.Revoke(ctx, "opp_abc", "tnt_a")
		if !errors.Is(err, ErrCannotConfirmTargetState) {
			t.Fatalf("revoke with dead receiver = %v, want ErrCannotConfirmTargetState", err)
		}
		row, _ := s.St.LatestOpportunityHandoff("opp_abc", "tnt_a")
		if row.LocalStatus == StatusRevoked {
			t.Fatal("a failed guard must not mark the snapshot revoked")
		}
	})
}

// 并发双击:两路同指纹确认 → 唯一快照(UNIQUE 兜底,输方回读)。
func TestConcurrentConfirmSingleSnapshot(t *testing.T) {
	s := newService(t, nil)
	var wg sync.WaitGroup
	ids := make([]string, 8)
	var firstErr error
	var mu sync.Mutex
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1")
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if res != nil {
				ids[i] = res.Handoff.HandoffID
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("concurrent confirm error: %v", firstErr)
	}
	unique := map[string]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatalf("concurrent double-click produced %d distinct handoffs: %v", len(unique), unique)
	}
	rows, _ := s.St.ListOpportunityHandoffs("opp_abc", "tnt_a")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want exactly 1", len(rows))
	}
}

// 接收端 409(同键异内容):快照原样保留,显式错误。
func TestReceiverConflictKeepsSnapshot(t *testing.T) {
	s := newService(t, nil)
	res, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Poison the receiver: same handoff id, different content.
	s.stub.mu.Lock()
	s.stub.intakes[res.Handoff.HandoffID] = "different"
	s.stub.mu.Unlock()
	_, err = s.Retry(ctx, "opp_abc", "tnt_a", "")
	if !errors.Is(err, ErrReceiverConflict) {
		t.Fatalf("retry err = %v, want ErrReceiverConflict", err)
	}
	row, _ := s.St.OpportunityHandoffByHandoffID(res.Handoff.HandoffID, "tnt_a")
	if row.DocJSON != res.Handoff.DocJSON {
		t.Fatal("local snapshot must never be overwritten on receiver conflict")
	}
	if row.LocalStatus == StatusRevoked {
		t.Fatal("conflict must not flip the snapshot to a terminal state")
	}
}

// 接收端 422 显式拒绝码透传(快照保留 + 结构性错误上浮)。
func TestReceiverRejectionSurfaced(t *testing.T) {
	s := newService(t, nil)
	s.stub.mu.Lock()
	s.stub.force422 = "TENANT_MISMATCH"
	s.stub.mu.Unlock()
	_, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1")
	if err == nil {
		t.Fatal("a 422 machine rejection must surface as an error")
	}
	if !strings.Contains(err.Error(), "TENANT_MISMATCH") {
		t.Fatalf("err = %v, want the machine code", err)
	}
	// 快照保留原样(零半成品纪律不适用于台账:确认事实与拒绝原因都在案)。
	row, _ := s.St.LatestOpportunityHandoff("opp_abc", "tnt_a")
	if row == nil || !strings.Contains(row.LastError, "TENANT_MISMATCH") {
		t.Fatalf("last_error = %v, want the machine code on the persisted snapshot", row)
	}
	if row.LocalStatus == StatusDelivered {
		t.Fatal("must not report delivered on 422")
	}
}

// 配置缺失 fail-closed:不出文档、不投递。
func TestNotConfiguredFailsClosed(t *testing.T) {
	s := newService(t, func(cfg *Config, d *Deliverer) { cfg.Token = "" })
	if _, err := s.Confirm(ctx, baseInput(), "usr_sales_a1", "http://x", "mem_1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}
