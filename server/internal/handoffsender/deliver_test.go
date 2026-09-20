// deliver_test.go — D-B1(hui-1749 跨仓共测)回归测试:投递与投影查询的
// 内部通道认证头必须与 O2 接收端 requireInternalToken 约定对齐——标准
// `Authorization: Bearer <token>`;旧 `X-Internal-Token` 头绝迹(真实部署
// 直投 401 的根因)。其余投递语义(存储字节、状态码矩阵、幂等、撤销守卫)
// 由 handler_service_draft_test.go 的 ecoStub 全链路测试覆盖。
package handoffsender

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testDeliverToken = "test-internal-token"

const deliverCreatedBody = `{"result":{"handoff_id":"h-b1","draft_ref":"demand:1","status":"draft","duplicate":false,"dirty":false,"revoked":false,"source_app":"leads-engine","source_kind":"standalone","source_ref":"opp-b1","missing":[],"scopes":["project.resume","asset.import"]}}`

const projectionOKBody = `{"projection":{"handoff_id":"h-b1","draft_ref":"demand:1","status":"draft","dirty":false,"revoked":false,"source_app":"leads-engine","source_ref":"opp-b1","updated_at":1}}`

// capturedAuth 记录假接收端看到的线级认证头。
type capturedAuth struct {
	authorization string // Authorization 头原值(必须是 "Bearer "+令牌)
	legacyToken   string // X-Internal-Token 头原值(旧约定,必须为空)
}

// newHeaderCapturingReceiver 起一个 httptest 假接收端:记录每个 POST 的
// 认证头,按给定状态码回应一份合法 JSON 体。
func newHeaderCapturingReceiver(t *testing.T, status int, body string) (*capturedAuth, *Deliverer) {
	t.Helper()
	seen := &capturedAuth{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, MaxDocBytes))
		seen.authorization = r.Header.Get("Authorization")
		seen.legacyToken = r.Header.Get("X-Internal-Token")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	d := &Deliverer{IntakeURL: srv.URL + "/internal/eco/leads/handoffs", Token: testDeliverToken}
	return seen, d
}

func TestDeliverAuthenticatesWithBearerHeader(t *testing.T) {
	seen, d := newHeaderCapturingReceiver(t, http.StatusCreated, deliverCreatedBody)
	res, err := d.Deliver(context.Background(), []byte(`{"handoff_id":"h-b1"}`))
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if res.HandoffID != "h-b1" || res.DraftRef != "demand:1" {
		t.Fatalf("result = %+v", res)
	}
	if want := "Bearer " + testDeliverToken; seen.authorization != want {
		t.Fatalf("Authorization = %q, want %q (O2 requireInternalToken 约定)", seen.authorization, want)
	}
	if seen.legacyToken != "" {
		t.Fatalf("legacy X-Internal-Token header must be gone, got %q", seen.legacyToken)
	}
}

func TestFetchProjectionAuthenticatesWithBearerHeader(t *testing.T) {
	seen, d := newHeaderCapturingReceiver(t, http.StatusOK, projectionOKBody)
	proj, err := d.FetchProjection(context.Background(), "h-b1")
	if err != nil {
		t.Fatalf("fetch projection: %v", err)
	}
	if proj.HandoffID != "h-b1" || proj.Status != "draft" {
		t.Fatalf("projection = %+v", proj)
	}
	if want := "Bearer " + testDeliverToken; seen.authorization != want {
		t.Fatalf("Authorization = %q, want %q (O2 requireInternalToken 约定)", seen.authorization, want)
	}
	if seen.legacyToken != "" {
		t.Fatalf("legacy X-Internal-Token header must be gone, got %q", seen.legacyToken)
	}
}
