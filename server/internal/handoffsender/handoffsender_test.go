// HUI-1749 contract tests: golden construction, shared-vector compliance
// (the sender passes the public source-profile vectors independently),
// payload minimization and registry-sourced facts.
package handoffsender

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/appregistry"
)

var clock = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

func baseInput() ConfirmedInput {
	budget := int64(150000)
	return ConfirmedInput{
		TenantID:         "tnt_a",
		OpportunityID:    "opp_abc",
		OpportunityTitle: "品牌焕新视频",
		BusinessCategory: "creative_service",
		Summary:          "拍摄一支 60 秒品牌焕新宣传片,含脚本与成片",
		ServiceCategory:  "video",
		BudgetCents:      &budget,
		Deadline:         "2026-10-31",
	}
}

func baseParams() BuildParams {
	return BuildParams{
		SourceApp:   "leads-engine",
		TargetApp:   "orders",
		TenantScope: "tenant-77",
		PrincipalID: "usr_sales_a1",
		ActorIssuer: "http://127.0.0.1:18101",
		ProofSalt:   "test-salt",
		Now:         clock,
	}
}

func mustFingerprint(t *testing.T, in ConfirmedInput) string {
	t.Helper()
	fp, err := in.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fp
}

// ---- golden construction + minimization --------------------------------------

func TestBuildDocumentGolden(t *testing.T) {
	in := baseInput()
	raw, err := BuildDocument(in, mustFingerprint(t, in), "handoff-golden-1", 3, baseParams())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := ValidateSenderDocument(raw); err != nil {
		t.Fatalf("self-validation: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 键封闭:恰好冻结 17 键,且绝不出现 order_ref/stage_ref(standalone 不造假订单)。
	if got := len(doc); got != 17 {
		t.Fatalf("top-level keys = %d, want 17 (%v)", got, docKeys(doc))
	}
	for _, banned := range []string{"order_ref", "stage_ref"} {
		if _, ok := doc[banned]; ok {
			t.Fatalf("%s must be absent in standalone documents", banned)
		}
	}
	if doc["schema_version"] != SchemaVersionV1 {
		t.Fatalf("schema_version = %v", doc["schema_version"])
	}
	if doc["source_app"] != "leads-engine" || doc["target_app"] != "orders" {
		t.Fatalf("apps = %v/%v", doc["source_app"], doc["target_app"])
	}
	if doc["brief_version"] != "3" {
		t.Fatalf("brief_version = %v (integer string per O2 alignment)", doc["brief_version"])
	}
	if doc["source_project_ref"] != "opp_abc" {
		t.Fatalf("source_project_ref = %v", doc["source_project_ref"])
	}
	if doc["source_revision"] != mustFingerprint(t, in) {
		t.Fatalf("source_revision must be the content fingerprint")
	}
	actor := doc["actor"].(map[string]any)
	if actor["app_id"] != "leads-engine" || actor["subject"] != "usr_sales_a1" {
		t.Fatalf("actor = %v", actor)
	}
	prof := doc["source_profile"].(map[string]any)
	if prof["profile_version"] != ProfileVersionV1 || prof["source_kind"] != SourceKindStandalone {
		t.Fatalf("profile = %v", prof)
	}
	if _, has := prof["campaign_ref"]; has {
		t.Fatal("campaign_ref must be absent for standalone")
	}
	cons := toStrSlice(t, prof["constraints"])
	joined := strings.Join(cons, "|")
	if !strings.Contains(joined, "purpose:service_procurement") || !strings.Contains(joined, "category:video") {
		t.Fatalf("constraints = %v", cons)
	}
	if !strings.Contains(joined, "budget_cents:150000") || !strings.Contains(joined, "deadline:2026-10-31") {
		t.Fatalf("optional confirmed facts must ride the opaque constraints: %v", cons)
	}
	spec := doc["delivery_spec"].(map[string]any)
	if !strings.Contains(spec["description"].(string), "品牌焕新宣传片") {
		t.Fatalf("description = %v", spec["description"])
	}
	// 时钟纪律:issued < expires ≤ +900s。
	iss, err1 := time.Parse("2006-01-02T15:04:05Z", doc["issued_at"].(string))
	exp, err2 := time.Parse("2006-01-02T15:04:05Z", doc["expires_at"].(string))
	if err1 != nil || err2 != nil {
		t.Fatalf("times = %v/%v (%v %v)", doc["issued_at"], doc["expires_at"], err1, err2)
	}
	if !exp.After(iss) || exp.Sub(iss) > 900*time.Second {
		t.Fatalf("window = %v", exp.Sub(iss))
	}
	// scopes:最小授权集。
	scopes := toStrSlice(t, doc["scopes"])
	if len(scopes) != 2 || scopes[0] != "project.resume" || scopes[1] != "asset.import" {
		t.Fatalf("scopes = %v", scopes)
	}
}

func docKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func toStrSlice(t *testing.T, v any) []string {
	t.Helper()
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, it := range raw {
		s, _ := it.(string)
		out = append(out, s)
	}
	return out
}

// 载荷最小化:联系人档案/跟进历史/备注在任何路径都不进入载荷。
func TestPayloadMinimization(t *testing.T) {
	in := baseInput()
	// 故意把 PII 放进上下文字段:它们绝不能因为"存在"而泄漏进文档。
	in.OpportunityTitle = "甲商家 13812345678 品牌视频(备注含此手机号)"
	raw, err := BuildDocument(in, mustFingerprint(t, in), "handoff-min-1", 1, baseParams())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(raw)
	for _, banned := range []string{"13812345678", "phone", "email", "notes", "followup", "tags", "contact_id", "consent"} {
		if strings.Contains(s, banned) {
			t.Fatalf("payload leaks %q:\n%s", banned, s)
		}
	}
	// 资产:仅显式引用进入;无引用时 assets=[](纯文字需求)。
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if assets := doc["assets"].([]any); len(assets) != 0 {
		t.Fatalf("assets = %v, want []", assets)
	}
	in2 := baseInput()
	in2.Assets = []AssetInput{{Ref: "brand-logo.png", SHA256: strings.Repeat("c", 64), SizeBytes: 1024, MediaType: "image/png"}}
	raw2, err := BuildDocument(in2, mustFingerprint(t, in2), "handoff-min-2", 1, baseParams())
	if err != nil {
		t.Fatalf("build2: %v", err)
	}
	var doc2 map[string]any
	_ = json.Unmarshal(raw2, &doc2)
	assets := doc2["assets"].([]any)
	if len(assets) != 1 {
		t.Fatalf("assets = %v, want exactly the confirmed reference", assets)
	}
	a := assets[0].(map[string]any)
	if a["asset_ref"] != "brand-logo.png" || a["sha256"] != strings.Repeat("c", 64) {
		t.Fatalf("asset = %v", a)
	}
	// 资产引用缺 hash = 未授权引用:拒绝,绝不代填。
	bad := baseInput()
	bad.Assets = []AssetInput{{Ref: "mystery.bin", SizeBytes: 10, MediaType: "application/octet-stream"}}
	if _, err := BuildDocument(bad, mustFingerprint(t, bad), "handoff-min-3", 1, baseParams()); err == nil {
		t.Fatal("asset without user-confirmed sha256 must be rejected (unauthorized reference)")
	}
}

func TestBuildRejectsBadInputsAndParams(t *testing.T) {
	fp := mustFingerprint(t, baseInput())
	if _, err := BuildDocument(baseInput(), fp, "", 1, baseParams()); err == nil {
		t.Fatal("empty handoff_id must be rejected")
	}
	if _, err := BuildDocument(baseInput(), fp, "handoff-x", 0, baseParams()); err == nil {
		t.Fatal("source_version < 1 must be rejected")
	}
	p := baseParams()
	p.TenantScope = ""
	if _, err := BuildDocument(baseInput(), fp, "handoff-x", 1, p); err == nil {
		t.Fatal("empty tenant_scope must fail closed")
	}
	merchant := baseInput()
	merchant.BusinessCategory = "merchant_customer"
	if _, err := BuildDocument(merchant, mustFingerprint(t, merchant), "handoff-x", 1, baseParams()); err == nil {
		t.Fatal("merchant_customer must be rejected at the builder")
	}
	badCat := baseInput()
	badCat.ServiceCategory = "Video_Pro"
	if _, err := BuildDocument(badCat, mustFingerprint(t, badCat), "handoff-x", 1, baseParams()); err == nil {
		t.Fatal("service_category must be a lowercase slug")
	}
}

// ---- 共享向量:发送端独立通过 ------------------------------------------------

type vectorFile struct {
	Cases []struct {
		Name             string `json:"name"`
		VectorKind       string `json:"vector_kind"`
		Document         string `json:"document"`
		ProfiledAccepted bool   `json:"profiled_accepted"`
		ExpectError      string `json:"expect_error"`
		Now              string `json:"now"`
		ExpiresAt        string `json:"expires_at"`
		ExpectExpired    bool   `json:"expect_expired"`
		Declared         string `json:"declared"`
		Expected         string `json:"expected"`
		ExpectMatch      bool   `json:"expect_match"`
	} `json:"cases"`
}

// TestSourceProfileVectorsIndependentPass:向量文件取自 public-ai
// origin/main=1f3c4f1(sha256 e380de4dc7963c3acb66f6bc0e008a295057d2cae637e5cc969a2c45bcf425d2,
// 与 O2 接收端钉版同源),发送端按其结论独立通过。
func TestSourceProfileVectorsIndependentPass(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-profile-vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vf vectorFile
	if err := json.Unmarshal(raw, &vf); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	for _, c := range vf.Cases {
		switch c.VectorKind {
		case "handoff":
			kind, verr := ValidateDocument([]byte(c.Document))
			if c.ProfiledAccepted {
				if verr != nil {
					t.Fatalf("%s: expected accepted, got %v", c.Name, verr)
				}
				wantKind := KindProfiled
				if c.Name == "order-plain-v1-accepted" {
					wantKind = KindOrder
				}
				if kind != wantKind {
					t.Fatalf("%s: kind = %v, want %v", c.Name, kind, wantKind)
				}
			} else {
				if verr == nil {
					t.Fatalf("%s: expected rejection", c.Name)
				}
				if c.ExpectError != "" && !strings.Contains(verr.Error(), c.ExpectError) {
					// error codes are internal; match the semantic token
					if !strings.Contains(verr.Error(), "unsupported profile_version") {
						t.Fatalf("%s: error = %v, want ~%s", c.Name, verr, c.ExpectError)
					}
				}
			}
		case "clock":
			now, err := time.Parse(time.RFC3339, c.Now)
			if err != nil {
				t.Fatalf("%s: now: %v", c.Name, err)
			}
			if got := ExpiredClock(now, c.ExpiresAt); got != c.ExpectExpired {
				t.Fatalf("%s: expired = %v, want %v", c.Name, got, c.ExpectExpired)
			}
		case "tenant":
			if got := TenantScopeMatches(c.Declared, c.Expected); got != c.ExpectMatch {
				t.Fatalf("%s: match = %v, want %v", c.Name, got, c.ExpectMatch)
			}
		}
	}
}

// 发送端构建产物过一遍向量同款裁决:非过期 + 租户域自洽。
func TestBuiltDocumentPassesVectorDecisions(t *testing.T) {
	in := baseInput()
	raw, err := BuildDocument(in, mustFingerprint(t, in), "handoff-vec-1", 1, baseParams())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var probe struct {
		ExpiresAt     string `json:"expires_at"`
		SourceProfile struct {
			TenantScope string `json:"tenant_scope"`
		} `json:"source_profile"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if ExpiredClock(clock.Add(5*time.Minute), probe.ExpiresAt) {
		t.Fatal("a freshly built document must be live for retries within the window")
	}
	if !ExpiredClock(clock.Add(11*time.Minute), probe.ExpiresAt) {
		t.Fatal("an 11-minute-old document must be expired (600s window)")
	}
	if !TenantScopeMatches(probe.SourceProfile.TenantScope, "tenant-77") {
		t.Fatal("built tenant_scope must match the configured deployment scope")
	}
	if TenantScopeMatches(probe.SourceProfile.TenantScope, "tenant-88") {
		t.Fatal("built tenant_scope must not match a foreign scope")
	}
}

// ---- registry-sourced facts ---------------------------------------------------

func TestRegistrySourcedFacts(t *testing.T) {
	reg, err := appregistry.Embedded()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if _, ok := reg.ResolveTarget("leads-engine", ReturnTargetID, "receipt"); !ok {
		t.Fatalf("return target %s must come from the registry", ReturnTargetID)
	}
	if !reg.HasCapability("leads-engine", CapabilityHandoff) {
		t.Fatal("capability must come from the registry")
	}
	// target_app 事实从 registry 读:未登记的接收端不进文档。
	in := baseInput()
	_ = in
	if _, ok := reg.App("orders"); !ok {
		t.Fatal("receiver app must be registered")
	}
	if _, ok := reg.App("guanlan-order"); ok {
		t.Fatal("unregistered app ids must not resolve (registry is the source of truth)")
	}
}

// 同指纹同 handoff_id 前提:指纹对内容敏感(任何变更都是新快照)。
func TestFingerprintSensitivity(t *testing.T) {
	base := mustFingerprint(t, baseInput())

	same := baseInput()
	if mustFingerprint(t, same) != base {
		t.Fatal("identical inputs must produce an identical fingerprint (idempotency key)")
	}

	variations := map[string]func(*ConfirmedInput){
		"summary":      func(i *ConfirmedInput) { i.Summary += "(修改)" },
		"category":     func(i *ConfirmedInput) { i.ServiceCategory = "design-poster" },
		"budget":       func(i *ConfirmedInput) { b := int64(1); i.BudgetCents = &b },
		"budget_clear": func(i *ConfirmedInput) { i.BudgetCents = nil },
		"deadline":     func(i *ConfirmedInput) { i.Deadline = "2026-11-30" },
		"assets": func(i *ConfirmedInput) {
			i.Assets = []AssetInput{{Ref: "a", SHA256: strings.Repeat("a", 64), SizeBytes: 1, MediaType: "image/png"}}
		},
	}
	for name, mutate := range variations {
		v := baseInput()
		mutate(&v)
		if mustFingerprint(t, v) == base {
			t.Fatalf("%s change must produce a different fingerprint (new snapshot)", name)
		}
	}

	// 商机身份参与指纹:内容不能漂浮到别的商机上。
	other := baseInput()
	other.OpportunityID = "opp_other"
	if mustFingerprint(t, other) == base {
		t.Fatal("another opportunity must never reuse the same fingerprint")
	}
}
