// Integration tests: the full role x assignment x tenant matrix over HTTP,
// identity discipline, provenance, fail-closed behavior and persistence.
// A fake platform-identity (httptest) plays the identity side; the server
// production code path (identity mode = platform) is unchanged.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/provision"
)

// ---- fake identity ------------------------------------------------------------

const (
	sessionOwnerA   = "sess-owner-a"
	sessionSalesA1  = "sess-sales-a1"
	sessionSalesA2  = "sess-sales-a2"
	sessionAgentA   = "sess-agent-a"
	sessionDisabled = "sess-disabled-a"
	sessionOwnerB   = "sess-owner-b"
	sessionStranger = "sess-stranger"
)

const (
	principalOwnerA   = "usr_owner_a"
	principalSalesA1  = "usr_sales_a1"
	principalSalesA2  = "usr_sales_a2"
	principalAgentA   = "usr_agent_a"
	principalDisabled = "usr_disabled_a"
	principalOwnerB   = "usr_owner_b"
	principalStranger = "usr_stranger"
)

func fakeIdentity(t *testing.T) *httptest.Server {
	t.Helper()
	sessions := map[string]string{
		sessionOwnerA:   principalOwnerA,
		sessionSalesA1:  principalSalesA1,
		sessionSalesA2:  principalSalesA2,
		sessionAgentA:   principalAgentA,
		sessionDisabled: principalDisabled,
		sessionOwnerB:   principalOwnerB,
		sessionStranger: principalStranger,
	}
	accounts := map[string]bool{
		"owner-a@example.com": true, "sales-a1@example.com": true, "sales-a2@example.com": true,
		"agent-a@example.com": true, "disabled@example.com": true, "owner-b@example.com": true,
		"stranger@example.com": true,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/identity/session/resolve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			SessionToken string `json:"session_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.Header.Get("X-App-ID") != "leads-engine" || r.Header.Get("X-PilotSeaView-Internal-Token") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		principal, ok := sessions[in.SessionToken]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true,
			"app_id":        "leads-engine",
			"session":       map[string]any{"principal_id": principal, "email": "someone@example.com"},
		})
	})
	mux.HandleFunc("POST /internal/v1/identity/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			AppID    string `json:"app_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.AppID != "leads-engine" || !accounts[in.Email] || in.Password == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": sessionOwnerA, "refresh_token": "rt-" + in.Email, "expires_in": 3600,
		})
	})
	mux.HandleFunc("POST /internal/v1/identity/refresh", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RefreshToken string `json:"refresh_token"`
			AppID        string `json:"app_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.AppID != "leads-engine" || in.RefreshToken == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": sessionOwnerA, "refresh_token": in.RefreshToken + "-r", "expires_in": 3600,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// ---- harness --------------------------------------------------------------------

type harness struct {
	t             *testing.T
	srv           *httptest.Server
	api           *Server // the raw server (tests reach into Eco wiring)
	cfg           config.Config
	identitySrv   *httptest.Server
	eco           *ecoStub // non-nil when opts.ecoOn
	dbPath        string
	internalToken string
	logs          *bytes.Buffer // non-nil only when opts.captureLog is set
}

type harnessOpts struct {
	omitIdentityToken   bool // config gate intentionally unsatisfied
	featureServiceDraft bool // FEATURE_SERVICE_DRAFT=on (mount point registered)
	ecoOn               bool // FEATURE_SERVICE_DRAFT=on + all ECO_HANDOFF_* facts wired to the stub receiver
	ecoIntakeDead       bool // ecoOn but the intake URL is a dead port (delivery-failure paths)
	captureLog          bool // capture server log lines for redaction assertions
	omitDedupPepper     bool // HUI-1683: intake fails closed when the pepper is missing
	featureLeadsFilter  bool // FEATURE_LEADS_FILTER=on (HUI-1686 无效线索过滤)
	featureFollowups    bool // FEATURE_FOLLOWUPS=on (HUI-1692 / FEAT-0193 跟进记录)
	featureLeadsAssign  bool // FEATURE_LEADS_ASSIGN=on (HUI-1685 / FEAT-0186 线索自动分配)
	featureFunnel       bool // FEATURE_FUNNEL=on (HUI-1694 / FEAT-0195 全漏斗分析)
}

func newHarnessOpts(t *testing.T, opts harnessOpts) *harness {
	t.Helper()
	fake := fakeIdentity(t)
	dbPath := filepath.Join(t.TempDir(), "leads.db")
	internalToken := "test-internal-token"
	ecoURL := ""
	var eco *ecoStub
	if opts.ecoOn {
		eco, ecoURL = newEcoStub(t)
		if opts.ecoIntakeDead {
			// 死端口:投递必然传输失败(快照保留路径)。
			ecoURL = "http://127.0.0.1:1/api/v1/internal/eco/leads/handoffs"
		}
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "LEADS_DB_PATH":
			return dbPath
		case "LEADS_INTERNAL_TOKEN":
			return internalToken
		case "PLATFORM_IDENTITY_BASE_URL":
			return fake.URL
		case "PLATFORM_IDENTITY_TOKEN":
			if opts.omitIdentityToken {
				return "" // gate problem on purpose
			}
			return "test-identity-token"
		case "FEATURE_SERVICE_DRAFT":
			if opts.featureServiceDraft || opts.ecoOn {
				return "true"
			}
			return ""
		case "FEATURE_LEADS_FILTER":
			if opts.featureLeadsFilter {
				return "true"
			}
			return ""
		case "FEATURE_FOLLOWUPS":
			if opts.featureFollowups {
				return "true"
			}
			return ""
		case "FEATURE_LEADS_ASSIGN":
			if opts.featureLeadsAssign {
				return "true"
			}
			return ""
		case "FEATURE_FUNNEL":
			if opts.featureFunnel {
				return "true"
			}
			return ""
		case "ECO_HANDOFF_TARGET_APP":
			return "orders"
		case "ECO_HANDOFF_INTAKE_URL":
			return ecoURL
		case "ECO_HANDOFF_TOKEN":
			return "test-eco-token"
		case "ECO_HANDOFF_TENANT_SCOPE":
			return "deploy-scope-a"
		case "ECO_HANDOFF_PROOF_SALT":
			return "test-proof-salt"
		case "LEADS_DEDUP_PEPPER":
			if opts.omitDedupPepper {
				return ""
			}
			return "test-dedup-pepper"
		}
		return ""
	})
	var logBuf *bytes.Buffer
	logger := log.New(io.Discard, "", 0)
	if opts.captureLog {
		logBuf = &bytes.Buffer{}
		logger = log.New(logBuf, "", 0)
	}
	s, err := Open(cfg, logger)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return &harness{t: t, srv: hs, api: s, cfg: cfg, identitySrv: fake, eco: eco, dbPath: dbPath, internalToken: internalToken, logs: logBuf}
}

func newHarness(t *testing.T) *harness { return newHarnessOpts(t, harnessOpts{}) }

func (h *harness) do(method, path, sessionToken, tenant, body string) (int, map[string]any, http.Header) {
	h.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.srv.URL+path, rdr)
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Internal-Token", h.internalToken)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if sessionToken != "" {
		req.Header.Set("X-Session-Token", sessionToken)
	}
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out, res.Header
}

func (h *harness) mustDo(method, path, session, tenant, body string, wantStatus int) map[string]any {
	h.t.Helper()
	status, out, _ := h.do(method, path, session, tenant, body)
	if status != wantStatus {
		h.t.Fatalf("%s %s: status %d, want %d (body %v)", method, path, status, wantStatus, out)
	}
	return out
}

// doRaw is the raw-body variant used for non-JSON responses (CSV export).
func (h *harness) doRaw(method, path, sessionToken, tenant, body string) (int, []byte, http.Header) {
	h.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.srv.URL+path, rdr)
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Internal-Token", h.internalToken)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if sessionToken != "" {
		req.Header.Set("X-Session-Token", sessionToken)
	}
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, raw, res.Header
}

func (h *harness) provision(args ...string) string {
	h.t.Helper()
	fn := args[0]
	rest := args[1:]
	var id string
	var err error
	switch fn {
	case "tenant":
		id, err = provision.Tenant(h.dbPath, rest[0])
	case "member":
		// tenant, principal, role, name, disabled
		disabled := len(rest) > 4 && rest[4] == "disabled"
		id, err = provision.Member(h.dbPath, rest[0], rest[1], rest[2], rest[3], !disabled)
	case "grant":
		id, err = provision.Grant(h.dbPath, rest[0], rest[1])
	default:
		h.t.Fatalf("unknown provision fn %s", fn)
	}
	if err != nil {
		h.t.Fatalf("provision %v: %v", args, err)
	}
	return id
}

// seed provisions two tenants with full membership fixture and one tenant-A
// contact assigned to sales-a1. Returns (tenantA, tenantB, contactA1).
func (h *harness) seed() (tenantA, tenantB, contactA1 string) {
	h.t.Helper()
	tenantA = h.provision("tenant", "Tenant A")
	tenantB = h.provision("tenant", "Tenant B")
	h.provision("member", tenantA, principalOwnerA, "owner", "Owner A")
	h.provision("member", tenantA, principalSalesA1, "sales", "Sales A1")
	h.provision("member", tenantA, principalSalesA2, "sales", "Sales A2")
	h.provision("member", tenantA, principalAgentA, "agent", "Agent A")
	h.provision("member", tenantA, principalDisabled, "sales", "Disabled", "disabled")
	h.provision("member", tenantB, principalOwnerB, "owner", "Owner B")

	created := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"甲商家","phone":"13812345678","email":"jia@shop.cn","business_category":"merchant_customer",
		  "source_type":"manual","consent_status":"granted"}`, http.StatusCreated)
	contactA1, _ = created["id"].(string)
	if contactA1 == "" {
		h.t.Fatalf("seed contact: no id in %v", created)
	}
	sales1ID := h.memberID(tenantA, principalSalesA1)
	h.mustDo("PATCH", "/api/v1/contacts/"+contactA1, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, sales1ID), http.StatusOK)
	return tenantA, tenantB, contactA1
}

func (h *harness) memberID(tenant, principal string) string {
	h.t.Helper()
	out := h.mustDo("GET", "/api/v1/admin/members", sessionOwnerA, tenant, "", http.StatusOK)
	items, _ := out["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["principal_ref"] == principal {
			s, _ := m["id"].(string)
			return s
		}
	}
	h.t.Fatalf("member %s not found", principal)
	return ""
}

// ---- the role x assignment x tenant matrix over HTTP -------------------------------

func TestHTTPPermissionMatrix(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, contactA1 := h.seed()

	t.Run("owner reads own tenant list", func(t *testing.T) {
		out := h.mustDo("GET", "/api/v1/contacts", sessionOwnerA, tenantA, "", http.StatusOK)
		items := out["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("want 1 contact, got %d", len(items))
		}
	})

	t.Run("assigned sales reads own record; unassigned sales gets 404", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts/"+contactA1, sessionSalesA1, tenantA, "", http.StatusOK)
		h.mustDo("GET", "/api/v1/contacts/"+contactA1, sessionSalesA2, tenantA, "", http.StatusNotFound)
	})

	t.Run("unassigned sales cannot update others' record (404 mask)", func(t *testing.T) {
		h.mustDo("PATCH", "/api/v1/contacts/"+contactA1, sessionSalesA2, tenantA,
			`{"name":"劫持"}`, http.StatusNotFound)
	})

	t.Run("sales cannot manage members, export, or read others via list", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/admin/members", sessionSalesA1, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/admin/members", sessionSalesA1, tenantA,
			`{"principal_ref":"usr_new","role":"sales"}`, http.StatusForbidden)
		h.mustDo("GET", "/api/v1/admin/export", sessionSalesA1, tenantA, "", http.StatusForbidden)
	})

	t.Run("cross tenant: owner B touching tenant A is 403 everywhere", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts/"+contactA1, sessionOwnerB, tenantA, "", http.StatusForbidden)
		h.mustDo("GET", "/api/v1/contacts", sessionOwnerB, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/contacts", sessionOwnerB, tenantA,
			`{"name":"越租户","business_category":"merchant_customer","source_type":"manual"}`, http.StatusForbidden)
		h.mustDo("GET", "/api/v1/admin/export", sessionOwnerB, tenantA, "", http.StatusForbidden)
	})

	t.Run("stranger principal is 403, never auto-provisioned", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts", sessionStranger, tenantA, "", http.StatusForbidden)
		out := h.mustDo("GET", "/api/v1/admin/members", sessionOwnerA, tenantA, "", http.StatusOK)
		for _, it := range out["items"].([]any) {
			m := it.(map[string]any)
			if m["principal_ref"] == principalStranger {
				t.Fatal("stranger was auto-provisioned as member — identity discipline broken")
			}
		}
	})

	t.Run("disabled member is 403 on everything", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts", sessionDisabled, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/contacts", sessionDisabled, tenantA,
			`{"name":"x","business_category":"merchant_customer","source_type":"manual"}`, http.StatusForbidden)
		h.mustDo("GET", "/api/v1/admin/export", sessionDisabled, tenantA, "", http.StatusForbidden)
	})

	t.Run("agent without grant is 403; with per-tenant grant can work; revoke cuts access", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/contacts", sessionAgentA, tenantA,
			`{"name":"代理建","business_category":"merchant_customer","source_type":"manual"}`, http.StatusForbidden)
		h.mustDo("GET", "/api/v1/contacts", sessionAgentA, tenantA, "", http.StatusForbidden)

		h.mustDo("POST", "/api/v1/admin/agent-grants", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"principal_ref":%q}`, principalAgentA), http.StatusCreated)
		created := h.mustDo("POST", "/api/v1/contacts", sessionAgentA, tenantA,
			`{"name":"代理建","business_category":"merchant_customer","source_type":"manual"}`, http.StatusCreated)
		agentID := h.memberID(tenantA, principalAgentA)
		if created["assigned_member_id"] != agentID {
			t.Fatalf("agent-created record must be self-assigned, got %v want %s", created["assigned_member_id"], agentID)
		}
		h.mustDo("GET", "/api/v1/contacts", sessionAgentA, tenantA, "", http.StatusOK)

		// grant is tenant specific: it unlocks only tenant A
		h.mustDo("GET", "/api/v1/contacts", sessionAgentA, tenantB, "", http.StatusForbidden)

		// revoke and verify access is cut immediately
		grants := h.mustDo("GET", "/api/v1/admin/agent-grants", sessionOwnerA, tenantA, "", http.StatusOK)
		gid, _ := grants["items"].([]any)[0].(map[string]any)["id"].(string)
		h.mustDo("DELETE", "/api/v1/admin/agent-grants/"+gid, sessionOwnerA, tenantA, "", http.StatusOK)
		h.mustDo("GET", "/api/v1/contacts", sessionAgentA, tenantA, "", http.StatusForbidden)
	})

	t.Run("agent grant requires agent membership in the tenant", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/admin/agent-grants", sessionOwnerA, tenantA,
			`{"principal_ref":"usr_nobody"}`, http.StatusBadRequest)
	})

	t.Run("leads follow the same matrix", func(t *testing.T) {
		lead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, contactA1), http.StatusCreated)
		leadID, _ := lead["id"].(string)
		h.mustDo("GET", "/api/v1/leads/"+leadID, sessionSalesA1, tenantA, "", http.StatusOK)
		h.mustDo("GET", "/api/v1/leads/"+leadID, sessionSalesA2, tenantA, "", http.StatusNotFound)
		h.mustDo("PATCH", "/api/v1/leads/"+leadID, sessionSalesA1, tenantA,
			`{"status":"in_progress"}`, http.StatusOK)
		// lead for a contact of another tenant is impossible
		h.mustDo("POST", "/api/v1/leads", sessionOwnerB, tenantB,
			fmt.Sprintf(`{"contact_id":%q}`, contactA1), http.StatusBadRequest)
	})

	t.Run("opportunities follow the same matrix", func(t *testing.T) {
		opp := h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"title":"首单合作","stage":"open","business_category":"merchant_customer"}`, contactA1), http.StatusCreated)
		oppID, _ := opp["id"].(string)
		h.mustDo("GET", "/api/v1/opportunities/"+oppID, sessionSalesA2, tenantA, "", http.StatusNotFound)
		// opp belongs to the owner: for sales it is masked as not-found
		h.mustDo("PATCH", "/api/v1/opportunities/"+oppID, sessionSalesA1, tenantA,
			`{"stage":"won"}`, http.StatusNotFound)
	})
}

// ---- identity discipline ----------------------------------------------------------

func TestIdentityDiscipline(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()

	t.Run("members cannot be created with email or phone as principal_ref", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/admin/members", sessionOwnerA, tenantA,
			`{"principal_ref":"someone@example.com","role":"sales"}`, http.StatusBadRequest)
		h.mustDo("POST", "/api/v1/admin/members", sessionOwnerA, tenantA,
			`{"principal_ref":"13812345678","role":"sales"}`, http.StatusBadRequest)
	})

	t.Run("principal_ref is immutable on member patch", func(t *testing.T) {
		memID := h.memberID(tenantA, principalSalesA1)
		h.mustDo("PATCH", "/api/v1/admin/members/"+memID, sessionOwnerA, tenantA,
			`{"principal_ref":"usr_other","enabled":true}`, http.StatusBadRequest)
	})

	t.Run("CRM record create never creates members or platform accounts", func(t *testing.T) {
		before := h.memberCount(tenantA)
		h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
			`{"name":"丙","phone":"13999998888","email":"bing@x.cn","business_category":"creative_service","source_type":"manual"}`, http.StatusCreated)
		if after := h.memberCount(tenantA); after != before {
			t.Fatalf("contact create changed member count %d -> %d", before, after)
		}
	})

	t.Run("disable cuts access immediately; re-enable restores", func(t *testing.T) {
		sales2 := h.memberID(tenantA, principalSalesA2)
		h.mustDo("PATCH", "/api/v1/admin/members/"+sales2, sessionOwnerA, tenantA,
			`{"enabled":false}`, http.StatusOK)
		h.mustDo("GET", "/api/v1/contacts", sessionSalesA2, tenantA, "", http.StatusForbidden)
		h.mustDo("PATCH", "/api/v1/admin/members/"+sales2, sessionOwnerA, tenantA,
			`{"enabled":true}`, http.StatusOK)
		h.mustDo("GET", "/api/v1/contacts", sessionSalesA2, tenantA, "", http.StatusOK)
	})

	t.Run("membership is per tenant; joining B does not grant A", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts", sessionOwnerB, tenantB, "", http.StatusOK)
		h.mustDo("GET", "/api/v1/contacts", sessionOwnerB, tenantA, "", http.StatusForbidden)
	})

	t.Run("owner reassignment moves visibility; sales cannot reassign", func(t *testing.T) {
		created := h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
			`{"name":"丁","business_category":"merchant_customer","source_type":"manual"}`, http.StatusCreated)
		id, _ := created["id"].(string)
		h.mustDo("GET", "/api/v1/contacts/"+id, sessionSalesA1, tenantA, "", http.StatusOK)
		sales2 := h.memberID(tenantA, principalSalesA2)
		h.mustDo("PATCH", "/api/v1/contacts/"+id, sessionSalesA1, tenantA,
			fmt.Sprintf(`{"assigned_member_id":%q}`, sales2), http.StatusForbidden)
		h.mustDo("PATCH", "/api/v1/contacts/"+id, sessionOwnerA, tenantA,
			fmt.Sprintf(`{"assigned_member_id":%q}`, sales2), http.StatusOK)
		h.mustDo("GET", "/api/v1/contacts/"+id, sessionSalesA1, tenantA, "", http.StatusNotFound)
		h.mustDo("GET", "/api/v1/contacts/"+id, sessionSalesA2, tenantA, "", http.StatusOK)
	})
}

func (h *harness) memberCount(tenant string) int {
	out := h.mustDo("GET", "/api/v1/admin/members", sessionOwnerA, tenant, "", http.StatusOK)
	items, _ := out["items"].([]any)
	return len(items)
}

// ---- provenance -----------------------------------------------------------------

func TestSourceProvenance(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	t.Run("non-manual source without provenance is rejected", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
			`{"name":"无留痕","business_category":"merchant_customer","source_type":"form"}`, http.StatusBadRequest)
	})
	t.Run("touch_campaign record stores source ref + auth scope snapshot", func(t *testing.T) {
		created := h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
			`{"name":"活动客","business_category":"merchant_customer","source_type":"touch_campaign",
			  "source":{"source_app":"touch-engine","source_ref":"campaign-42","auth_scope_snapshot":"grant:read-contacts;rev:sha256:deadbeef"}}`,
			http.StatusCreated)
		if created["source_ref_id"] == "" {
			t.Fatalf("source_ref_id missing: %v", created)
		}
	})
	t.Run("manual contact has no source ref", func(t *testing.T) {
		created := h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
			`{"name":"手工客","business_category":"creative_service","source_type":"manual"}`, http.StatusCreated)
		if _, present := created["source_ref_id"]; present {
			t.Fatalf("manual contact must not carry source ref, got %v", created["source_ref_id"])
		}
	})
}

// ---- fail-closed ----------------------------------------------------------------

func TestFailClosed(t *testing.T) {
	t.Run("missing internal token is 401", func(t *testing.T) {
		h := newHarness(t)
		tenantA, _, _ := h.seed()
		req, _ := http.NewRequestWithContext(context.Background(), "GET", h.srv.URL+"/api/v1/contacts", nil)
		req.Header.Set("X-Tenant-ID", tenantA)
		req.Header.Set("X-Session-Token", sessionOwnerA)
		res, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", res.StatusCode)
		}
	})

	t.Run("identity down => 503 identity_unavailable, nothing fabricated", func(t *testing.T) {
		h := newHarness(t)
		tenantA, _, _ := h.seed()
		h.identitySrv.Close() // kill the platform dependency
		status, body, _ := h.do("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			`{"name":"不该被创建","business_category":"merchant_customer","source_type":"manual"}`)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("want 503, got %d body=%v", status, body)
		}
		if body["error"] != "identity_unavailable" {
			t.Fatalf("error code = %v", body["error"])
		}
	})

	t.Run("config gate unsatisfied => 503 config_gate listing missing keys", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{omitIdentityToken: true})
		tenantA := h.provision("tenant", "Gate A")
		h.provision("member", tenantA, principalOwnerA, "owner", "Owner A")
		status, body, _ := h.do("GET", "/api/v1/contacts", sessionOwnerA, tenantA, "")
		if status != http.StatusServiceUnavailable || body["error"] != "config_gate" {
			t.Fatalf("want 503 config_gate, got %d %v", status, body)
		}
		detail, _ := body["detail"].([]any)
		if len(detail) == 0 {
			t.Fatal("config_gate must list the missing keys")
		}
	})

	t.Run("healthz always answers and shows platform identity mode", func(t *testing.T) {
		h := newHarness(t)
		status, body, _ := h.do("GET", "/healthz", "", "", "")
		if status != http.StatusOK {
			t.Fatalf("healthz: %d", status)
		}
		if body["identity_mode"] != "platform" {
			t.Fatalf("identity_mode = %v (must always be platform)", body["identity_mode"])
		}
	})
}

// ---- auth endpoints over the fake identity ------------------------------------------

func TestAuthEndpoints(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	t.Run("login sets session cookies", func(t *testing.T) {
		status, body, headers := h.do("POST", "/api/v1/auth/login", "", "",
			`{"email":"owner-a@example.com","password":"pw"}`)
		if status != http.StatusOK || body["authenticated"] != true {
			t.Fatalf("login: %d %v", status, body)
		}
		cookies := headers["Set-Cookie"]
		if len(cookies) < 2 {
			t.Fatalf("expected session+refresh cookies, got %v", cookies)
		}
	})

	t.Run("bad login is 401 without cookies", func(t *testing.T) {
		status, _, headers := h.do("POST", "/api/v1/auth/login", "", "",
			`{"email":"nope@example.com","password":"pw"}`)
		if status != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", status)
		}
		if len(headers["Set-Cookie"]) != 0 {
			t.Fatal("failed login must not set cookies")
		}
	})

	t.Run("whoami resolves member context", func(t *testing.T) {
		out := h.mustDo("GET", "/api/v1/whoami", sessionOwnerA, tenantA, "", http.StatusOK)
		if out["role"] != "owner" || out["tenant_id"] != tenantA {
			t.Fatalf("whoami = %v", out)
		}
		if out["email"] != "s***@example.com" {
			t.Fatalf("whoami email must be masked, got %v", out["email"])
		}
	})

	t.Run("whoami without session is 401", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/whoami", "", tenantA, "", http.StatusUnauthorized)
	})

	t.Run("requests without tenant header are 400", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts", sessionOwnerA, "", "", http.StatusBadRequest)
	})
}

// ---- persistence: save / quit / relogin / recover ------------------------------------

func TestPersistenceAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "leads.db")
	fake := fakeIdentity(t)
	internalToken := "tok"
	mkCfg := func() config.Config {
		return config.Load(func(k string) string {
			switch k {
			case "LEADS_DB_PATH":
				return dbPath
			case "LEADS_INTERNAL_TOKEN":
				return internalToken
			case "PLATFORM_IDENTITY_BASE_URL":
				return fake.URL
			case "PLATFORM_IDENTITY_TOKEN":
				return "tok"
			}
			return ""
		})
	}

	tenantA, err := provision.Tenant(dbPath, "Persist A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provision.Member(dbPath, tenantA, principalOwnerA, "owner", "Owner A", true); err != nil {
		t.Fatal(err)
	}

	// first process: create a contact, then "exit" (close)
	s1, err := Open(mkCfg(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	hs1 := httptest.NewServer(s1.Handler())
	h1 := &harness{t: t, srv: hs1, cfg: mkCfg(), identitySrv: fake, dbPath: dbPath, internalToken: internalToken}
	created := h1.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"留存客户","business_category":"merchant_customer","source_type":"manual","consent_status":"granted"}`, http.StatusCreated)
	contactID, _ := created["id"].(string)
	hs1.Close()
	s1.Close()

	// second process: relogin (same session contract) and recover the record
	s2, err := Open(mkCfg(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	hs2 := httptest.NewServer(s2.Handler())
	t.Cleanup(func() { hs2.Close() })
	h2 := &harness{t: t, srv: hs2, cfg: mkCfg(), identitySrv: fake, dbPath: dbPath, internalToken: internalToken}
	got := h2.mustDo("GET", "/api/v1/contacts/"+contactID, sessionOwnerA, tenantA, "", http.StatusOK)
	if got["name"] != "留存客户" {
		t.Fatalf("record did not survive restart: %v", got)
	}
}
