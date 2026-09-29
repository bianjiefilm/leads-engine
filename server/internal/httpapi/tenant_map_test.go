package httpapi

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/tenantmap"
)

func legacyTenantBinding(external, target string) tenantmap.Binding {
	return tenantmap.Binding{
		SourceApp: tenantmap.Wildcard, SourceNS: tenantmap.Wildcard,
		ExternalTenant: external, TargetTenant: target,
		Version: 1, Basis: tenantmap.BasisOperatorBinding,
	}
}

func scopedTenantBinding(app, external, target string, version int) tenantmap.Binding {
	return tenantmap.Binding{
		SourceApp: app, SourceNS: tenantmap.NamespaceNotify,
		ExternalTenant: external, TargetTenant: target,
		Version: version, Basis: tenantmap.BasisOperatorBinding,
	}
}

func ingestHarness(t *testing.T) (*harness, *recordSource, string) {
	t.Helper()
	secret := "test-ingest-secret"
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{
		featureNotifyIngest: true,
		ingestFetchBase:     src.url,
		ingestSecret:        secret,
		ingestFetchToken:    "test-fetch-token",
	})
	return h, src, secret
}

func insertTenant(t *testing.T, h *harness, id, name string) {
	t.Helper()
	_, err := h.api.St.DB.Exec(`INSERT INTO tenants(id,name,created_at) VALUES(?,?,?)`,
		id, name, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
}

// HUI-2316 反例：绑定 S→A 且本地还没有 S 时，事件写入 A。
// 之后插入同 ID 的本地租户，配置不变。重放、跟进摘要和更高版本撤销必须仍在 A。
func TestTenantBindingStaysAfterLocalTenantAppears(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, tenantB, _ := h.seed()
	const sourceS = "tnt_ext_s"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{scopedTenantBinding("touch-engine", sourceS, tenantA, 2)}

	ref := "sub_map_stay"
	body := ingestEvent(sourceS, "evt-map-stay", ref, 1, "lead.authorized_submitted", "nfc", false, "稳定", "13900008821", "")
	src.put(ref, recordJSON(sourceS, ref, "稳定", "13900008821", "", "", "granted", "lead_submission", true, true, false))
	res := postIngest(t, h, body, signBody(secret, body))
	if res.status != 200 {
		t.Fatalf("ingest=%d %s", res.status, res.raw)
	}
	leadID, _ := res.json["lead_id"].(string)
	contactID, _ := res.json["contact_id"].(string)
	if leadID == "" || contactID == "" {
		t.Fatalf("receipt=%v", res.json)
	}
	if got := leadTenant(t, h, leadID); got != tenantA {
		t.Fatalf("lead tenant=%s want %s", got, tenantA)
	}
	assertMapRow(t, h, "evt-map-stay", sourceS, tenantA, 2, tenantmap.BasisOperatorBinding)
	for _, got := range src.fetchedTenants() {
		if got != sourceS {
			t.Fatalf("fetch tenant=%s want source %s", got, sourceS)
		}
	}
	if pendingFollowUp(t, h, sourceS, "camp-1") != 1 {
		t.Fatal("pending before local tenant")
	}

	insertTenant(t, h, sourceS, "Local S")

	if pending := pendingFollowUp(t, h, sourceS, "camp-1"); pending != 1 {
		t.Fatalf("pending after local tenant=%d want 1", pending)
	}
	replay := postIngest(t, h, body, signBody(secret, body))
	if replay.status != 200 || replay.json["lead_id"] != leadID {
		t.Fatalf("replay=%d %v", replay.status, replay.json)
	}
	if n := countLeads(t, h, sourceS); n != 0 {
		t.Fatalf("replay created %d leads in the new local tenant", n)
	}
	if n := countLeads(t, h, tenantA); n != 1 {
		t.Fatalf("tenant A leads=%d want 1", n)
	}

	revokeBody := ingestEvent(sourceS, "evt-map-revoke", ref, 2, "lead.consent_revoked", "nfc", true, "稳定", "13900008821", "")
	src.put(ref, recordJSON(sourceS, ref, "稳定", "13900008821", "", "", "revoked", "lead_submission", false, false, false))
	rev := postIngest(t, h, revokeBody, signBody(secret, revokeBody))
	if rev.status != 200 {
		t.Fatalf("revoke=%d %s", rev.status, rev.raw)
	}
	if marketingAllowed(t, h, tenantA, contactID, "marketing_phone") {
		t.Fatal("revocation left A")
	}
	if n := countLeads(t, h, sourceS); n != 0 {
		t.Fatalf("revoke created %d leads in the new local tenant", n)
	}

	ref2 := "sub_map_stay_2"
	body2 := ingestEvent(sourceS, "evt-map-stay-2", ref2, 1, "lead.authorized_submitted", "nfc", false, "仍在A", "13900008822", "")
	src.put(ref2, recordJSON(sourceS, ref2, "仍在A", "13900008822", "", "", "granted", "lead_submission", true, false, false))
	second := postIngest(t, h, body2, signBody(secret, body2))
	if second.status != 200 {
		t.Fatalf("second=%d %s", second.status, second.raw)
	}
	if got := leadTenant(t, h, second.json["lead_id"].(string)); got != tenantA {
		t.Fatalf("second lead tenant=%s", got)
	}
	if countLeads(t, h, sourceS) != 0 || countLeads(t, h, tenantA) != 2 {
		t.Fatalf("leads S=%d A=%d", countLeads(t, h, sourceS), countLeads(t, h, tenantA))
	}

	h.api.Cfg.TenantBindings = []tenantmap.Binding{scopedTenantBinding("touch-engine", sourceS, tenantB, 9)}
	driftReplay := postIngest(t, h, body, signBody(secret, body))
	if driftReplay.status != 200 || driftReplay.json["lead_id"] != leadID {
		t.Fatalf("drift replay=%d %v", driftReplay.status, driftReplay.json)
	}
	if pendingFollowUp(t, h, sourceS, "camp-1") != 2 {
		t.Fatal("summary followed the new binding")
	}
	ref3 := "sub_map_drift"
	body3 := ingestEvent(sourceS, "evt-map-drift", ref3, 1, "lead.authorized_submitted", "nfc", false, "漂移", "13900008823", "")
	src.put(ref3, recordJSON(sourceS, ref3, "漂移", "13900008823", "", "", "granted", "lead_submission", true, false, false))
	drift := postIngest(t, h, body3, signBody(secret, body3))
	if drift.status != 409 || drift.json["error"] != "tenant_map_conflict" || !strings.Contains(hintOf(drift.json), "will not") {
		t.Fatalf("drift=%d %s", drift.status, drift.raw)
	}
	if countLeads(t, h, tenantB) != 0 || countLeads(t, h, sourceS) != 0 {
		t.Fatal("drift wrote a lead")
	}
}

func TestExistingLocalTenantRejectsConflictingBinding(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, _, _ := h.seed()
	const sourceS = "tnt_ext_local"
	insertTenant(t, h, sourceS, "Already local")

	ref := "sub_local_first"
	body := ingestEvent(sourceS, "evt-local-first", ref, 1, "lead.authorized_submitted", "nfc", false, "本地", "13900008831", "")
	src.put(ref, recordJSON(sourceS, ref, "本地", "13900008831", "", "", "granted", "lead_submission", true, true, false))
	first := postIngest(t, h, body, signBody(secret, body))
	if first.status != 200 {
		t.Fatalf("passthrough=%d %s", first.status, first.raw)
	}
	leadID := first.json["lead_id"].(string)
	contactID := first.json["contact_id"].(string)
	if leadTenant(t, h, leadID) != sourceS {
		t.Fatal("first event was not stored on the local tenant")
	}
	assertMapRow(t, h, "evt-local-first", sourceS, sourceS, 1, tenantmap.BasisLeadsPrimaryKey)

	h.api.Cfg.TenantBindings = []tenantmap.Binding{scopedTenantBinding("touch-engine", sourceS, tenantA, 1)}
	replay := postIngest(t, h, body, signBody(secret, body))
	if replay.status != 200 || replay.json["lead_id"] != leadID {
		t.Fatalf("replay=%d %v", replay.status, replay.json)
	}
	if pendingFollowUp(t, h, sourceS, "camp-1") != 1 {
		t.Fatal("summary left the stored tenant")
	}
	refNew := "sub_local_new"
	bodyNew := ingestEvent(sourceS, "evt-local-new", refNew, 1, "lead.authorized_submitted", "nfc", false, "新的", "13900008832", "")
	src.put(refNew, recordJSON(sourceS, refNew, "新的", "13900008832", "", "", "granted", "lead_submission", true, false, false))
	rejected := postIngest(t, h, bodyNew, signBody(secret, bodyNew))
	if rejected.status != 409 || !strings.Contains(hintOf(rejected.json), "LEADS_TENANT_BINDINGS") || !strings.Contains(hintOf(rejected.json), "will not") {
		t.Fatalf("new ref=%d %s", rejected.status, rejected.raw)
	}
	if countLeads(t, h, tenantA) != 0 || countLeads(t, h, sourceS) != 1 {
		t.Fatalf("leads moved A=%d S=%d", countLeads(t, h, tenantA), countLeads(t, h, sourceS))
	}

	revokeBody := ingestEvent(sourceS, "evt-local-revoke", ref, 2, "lead.consent_revoked", "nfc", true, "本地", "13900008831", "")
	src.put(ref, recordJSON(sourceS, ref, "本地", "13900008831", "", "", "revoked", "lead_submission", false, false, false))
	rev := postIngest(t, h, revokeBody, signBody(secret, revokeBody))
	if rev.status != 200 {
		t.Fatalf("revoke=%d %s", rev.status, rev.raw)
	}
	if !consentRevoked(t, h, sourceS, contactID, "marketing_phone") {
		t.Fatal("revocation did not stay on the stored tenant")
	}
	if countLeads(t, h, tenantA) != 0 {
		t.Fatal("revocation created a lead in A")
	}
}

func TestBindingTargetMissingOrDeleted(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, _, _ := h.seed()
	const sourceS = "tnt_ext_miss"
	const ghost = "tnt_ghost_target"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{legacyTenantBinding(sourceS, ghost)}
	body := ingestEvent(sourceS, "evt-ghost", "sub_ghost", 1, "lead.authorized_submitted", "nfc", false, "缺目标", "13900008841", "")
	src.put("sub_ghost", recordJSON(sourceS, "sub_ghost", "缺目标", "13900008841", "", "", "granted", "lead_submission", true, false, false))
	res := postIngest(t, h, body, signBody(secret, body))
	if res.status != 404 || res.json["error"] != "unknown_tenant" || !strings.Contains(hintOf(res.json), "Do not create a local tenant") {
		t.Fatalf("missing=%d %s", res.status, res.raw)
	}
	if countLeads(t, h, tenantA) != 0 {
		t.Fatal("missing target still wrote a lead")
	}

	insertTenant(t, h, ghost, "Temp")
	if _, err := h.api.St.DB.Exec(`DELETE FROM tenants WHERE id=?`, ghost); err != nil {
		t.Fatal(err)
	}
	res = postIngest(t, h, body, signBody(secret, body))
	if res.status != 404 {
		t.Fatalf("deleted target=%d %s", res.status, res.raw)
	}

	h.api.Cfg.TenantBindings = []tenantmap.Binding{legacyTenantBinding(sourceS, tenantA)}
	okBody := ingestEvent(sourceS, "evt-keep", "sub_keep", 1, "lead.authorized_submitted", "nfc", false, "留下", "13900008842", "")
	src.put("sub_keep", recordJSON(sourceS, "sub_keep", "留下", "13900008842", "", "", "granted", "lead_submission", true, false, false))
	ok := postIngest(t, h, okBody, signBody(secret, okBody))
	if ok.status != 200 {
		t.Fatalf("keep=%d %s", ok.status, ok.raw)
	}
	if _, err := h.api.St.DB.Exec(`DELETE FROM tenants WHERE id=?`, tenantA); err == nil {
		t.Fatal("deleting a tenant that owns a lead must fail")
	}
	replay := postIngest(t, h, okBody, signBody(secret, okBody))
	if replay.status != 200 || replay.json["lead_id"] != ok.json["lead_id"] {
		t.Fatalf("replay after failed delete=%d %v", replay.status, replay.json)
	}
}

func TestTenantLookupErrorIsNotAbsence(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, _, _ := h.seed()
	const sourceS = "tnt_ext_disk"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{legacyTenantBinding(sourceS, tenantA)}
	var calls []string
	h.api.mapLookupFn = func(id string) (bool, error) {
		calls = append(calls, id)
		if id == sourceS {
			return false, errors.New("disk")
		}
		if id == tenantA {
			return true, nil
		}
		return false, nil
	}
	body := ingestEvent(sourceS, "evt-disk", "sub_disk", 1, "lead.authorized_submitted", "nfc", false, "读失败", "13900008851", "")
	src.put("sub_disk", recordJSON(sourceS, "sub_disk", "读失败", "13900008851", "", "", "granted", "lead_submission", true, false, false))
	res := postIngest(t, h, body, signBody(secret, body))
	if res.status != 500 || res.json["error"] != "tenant_lookup_failed" {
		t.Fatalf("status=%d %s", res.status, res.raw)
	}
	if strings.Contains(res.raw, "unknown_tenant") {
		t.Fatalf("lookup error was treated as absence: %s", res.raw)
	}
	for _, id := range calls {
		if id == tenantA {
			t.Fatal("target was consulted after the source read failed")
		}
	}
	var n int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(1) FROM notify_inbox WHERE profile_event_id=?`, "evt-disk").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 || countLeads(t, h, tenantA) != 0 {
		t.Fatalf("lookup error wrote inbox=%d leads=%d", n, countLeads(t, h, tenantA))
	}

	h.api.mapLookupFn = func(string) (bool, error) { return false, errors.New("disk") }
	code, parsed, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-1", "", sourceS, "")
	if code != 500 || parsed["error"] == "unknown_tenant" {
		t.Fatalf("summary=%d %v", code, parsed)
	}
}

func TestInvalidBindingsAreNotPartlyApplied(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, _, _ := h.seed()
	h.api.Cfg.TenantBindings = []tenantmap.Binding{legacyTenantBinding("tnt_ext_bad", tenantA)}
	h.api.Cfg.TenantBindingProblems = []string{"duplicate binding for */*/tnt_ext_bad"}
	body := ingestEvent("tnt_ext_bad", "evt-bad-cfg", "sub_bad_cfg", 1, "lead.authorized_submitted", "nfc", false, "坏配置", "13900008861", "")
	src.put("sub_bad_cfg", recordJSON("tnt_ext_bad", "sub_bad_cfg", "坏配置", "13900008861", "", "", "granted", "lead_submission", true, false, false))
	res := postIngest(t, h, body, signBody(secret, body))
	if res.status != 503 || res.json["error"] != "config_gate_tenant_map" || !strings.Contains(hintOf(res.json), "whole set") {
		t.Fatalf("config=%d %s", res.status, res.raw)
	}
	if countLeads(t, h, tenantA) != 0 {
		t.Fatal("invalid config still routed a lead")
	}
}

func TestTwoSourcesSameExternalTenantStayApart(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, tenantB, _ := h.seed()
	h.api.Cfg.IngestSources = append(h.api.Cfg.IngestSources, config.IngestSource{
		AppID: "partner-app", Secret: "partner-secret", FetchBase: src.url, FetchToken: "test-fetch-token",
	})
	const sourceS = "tnt_ext_shared"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{
		scopedTenantBinding("touch-engine", sourceS, tenantA, 1),
		scopedTenantBinding("partner-app", sourceS, tenantB, 1),
	}
	refA, refB := "sub_src_a", "sub_src_b"
	bodyA := ingestEvent(sourceS, "evt-src-a", refA, 1, "lead.authorized_submitted", "nfc", false, "甲源", "13900008871", "")
	bodyB := ingestEventApp("partner-app", sourceS, "evt-src-b", refB, 1, "lead.authorized_submitted", "nfc", false, "乙源", "13900008871", "")
	src.put(refA, recordJSON(sourceS, refA, "甲源", "13900008871", "", "", "granted", "lead_submission", true, false, false))
	src.put(refB, recordJSON(sourceS, refB, "乙源", "13900008871", "", "", "granted", "lead_submission", true, false, false))
	a := postIngest(t, h, bodyA, signBody(secret, bodyA))
	b := postIngest(t, h, bodyB, signBody("partner-secret", bodyB))
	if a.status != 200 || b.status != 200 {
		t.Fatalf("a=%d %s b=%d %s", a.status, a.raw, b.status, b.raw)
	}
	if a.json["contact_id"] == b.json["contact_id"] {
		t.Fatal("two sources merged a contact across targets")
	}
	if leadTenant(t, h, a.json["lead_id"].(string)) != tenantA || leadTenant(t, h, b.json["lead_id"].(string)) != tenantB {
		t.Fatal("lead landed in the wrong target")
	}
	code, parsed, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-1", "", sourceS, "")
	raw := mustJSON(parsed)
	if code != 409 || strings.Contains(raw, a.json["lead_id"].(string)) || strings.Contains(raw, b.json["lead_id"].(string)) {
		t.Fatalf("unscoped summary=%d %s", code, raw)
	}
	code, parsed, _ = h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-1&source_app=touch-engine", "", sourceS, "")
	if code != 200 || pendingCount(parsed) != 1 || !summaryHasLead(parsed, a.json["lead_id"].(string)) || summaryHasLead(parsed, b.json["lead_id"].(string)) {
		t.Fatalf("touch summary=%d %v", code, parsed)
	}
}

func TestTwoSourcesSameTargetDedupInsideThatTenant(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, tenantB, _ := h.seed()
	h.api.Cfg.IngestSources = append(h.api.Cfg.IngestSources, config.IngestSource{
		AppID: "partner-app", Secret: "partner-secret", FetchBase: src.url, FetchToken: "test-fetch-token",
	})
	h.api.Cfg.TenantBindings = []tenantmap.Binding{
		scopedTenantBinding("touch-engine", "tnt_ext_one", tenantA, 1),
		scopedTenantBinding("partner-app", "tnt_ext_two", tenantA, 1),
	}
	bodyA := ingestEvent("tnt_ext_one", "evt-same-a", "sub_same_a", 1, "lead.authorized_submitted", "nfc", false, "同目标", "13900008881", "")
	bodyB := ingestEventApp("partner-app", "tnt_ext_two", "evt-same-b", "sub_same_b", 1, "lead.authorized_submitted", "nfc", false, "同目标", "13900008881", "")
	src.put("sub_same_a", recordJSON("tnt_ext_one", "sub_same_a", "同目标", "13900008881", "", "", "granted", "lead_submission", true, false, false))
	src.put("sub_same_b", recordJSON("tnt_ext_two", "sub_same_b", "同目标", "13900008881", "", "", "granted", "lead_submission", true, false, false))
	a := postIngest(t, h, bodyA, signBody(secret, bodyA))
	b := postIngest(t, h, bodyB, signBody("partner-secret", bodyB))
	if a.status != 200 || b.status != 200 {
		t.Fatalf("a=%d %s b=%d %s", a.status, a.raw, b.status, b.raw)
	}
	if a.json["contact_id"] != b.json["contact_id"] {
		t.Fatal("same target did not dedup the phone")
	}
	if leadTenant(t, h, a.json["lead_id"].(string)) != tenantA || leadTenant(t, h, b.json["lead_id"].(string)) != tenantA {
		t.Fatal("dedup left the target tenant")
	}
	if countLeads(t, h, tenantA) != 2 || countLeads(t, h, tenantB) != 0 {
		t.Fatalf("A=%d B=%d", countLeads(t, h, tenantA), countLeads(t, h, tenantB))
	}
	var apps string
	if err := h.api.St.DB.QueryRow(`SELECT GROUP_CONCAT(source_app) FROM source_refs WHERE tenant_id=?`, tenantA).Scan(&apps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(apps, "touch-engine") || !strings.Contains(apps, "partner-app") {
		t.Fatalf("source apps=%s", apps)
	}
}

func TestConcurrentNotifyDeliveryIsIdempotent(t *testing.T) {
	h, src, secret := ingestHarness(t)
	tenantA, _, _ := h.seed()
	const sourceS = "tnt_ext_race"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{scopedTenantBinding("touch-engine", sourceS, tenantA, 1)}
	ref := "sub_race"
	body := ingestEvent(sourceS, "evt-race", ref, 1, "lead.authorized_submitted", "nfc", false, "并发", "13900008891", "")
	src.put(ref, recordJSON(sourceS, ref, "并发", "13900008891", "", "", "granted", "lead_submission", true, false, false))
	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	ids := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			res := postIngest(t, h, body, signBody(secret, body))
			codes[i] = res.status
			ids[i], _ = res.json["lead_id"].(string)
		}(i)
	}
	wg.Wait()
	leadID := ""
	for i := 0; i < n; i++ {
		if codes[i] != 200 || ids[i] == "" {
			t.Fatalf("delivery %d status=%d id=%q", i, codes[i], ids[i])
		}
		if leadID == "" {
			leadID = ids[i]
		}
		if ids[i] != leadID {
			t.Fatalf("lead ids diverged: %v", ids)
		}
	}
	if countLeads(t, h, tenantA) != 1 || countLeads(t, h, sourceS) != 0 {
		t.Fatalf("A=%d S=%d", countLeads(t, h, tenantA), countLeads(t, h, sourceS))
	}
	if pendingFollowUp(t, h, sourceS, "camp-1") != 1 {
		t.Fatal("concurrent delivery changed the follow-up count")
	}
}

func assertMapRow(t *testing.T, h *harness, eventID, source, target string, version int, basis string) {
	t.Helper()
	var gotSource, gotTarget, gotBasis, snapshot string
	var gotVersion int
	err := h.api.St.DB.QueryRow(`
		SELECT i.source_tenant_id, i.map_target_tenant_id, i.map_version, i.map_basis, COALESCE(sr.auth_scope_snapshot,'')
		FROM notify_inbox i
		LEFT JOIN leads l ON l.id=i.lead_id
		LEFT JOIN source_refs sr ON sr.id=l.source_ref_id
		WHERE i.profile_event_id=?`, eventID).Scan(&gotSource, &gotTarget, &gotVersion, &gotBasis, &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if gotSource != source || gotTarget != target || gotVersion != version || gotBasis != basis {
		t.Fatalf("map row source=%s target=%s version=%d basis=%s", gotSource, gotTarget, gotVersion, gotBasis)
	}
	if !strings.Contains(snapshot, `"source_tenant_id":"`+source+`"`) || !strings.Contains(snapshot, `"map_basis":"`+basis+`"`) {
		t.Fatalf("snapshot=%s", snapshot)
	}
}

func consentRevoked(t *testing.T, h *harness, tenant, contactID, channel string) bool {
	t.Helper()
	var allowed int
	var revoked *string
	err := h.api.St.DB.QueryRow(`SELECT marketing_allowed, revoked_at FROM contact_consents WHERE tenant_id=? AND contact_id=? AND source_channel=?`,
		tenant, contactID, channel).Scan(&allowed, &revoked)
	if err != nil {
		t.Fatal(err)
	}
	return revoked != nil && *revoked != ""
}

func hintOf(parsed map[string]any) string {
	hint, _ := parsed["hint"].(string)
	return hint
}

func pendingCount(parsed map[string]any) int {
	camps, _ := parsed["campaigns"].([]any)
	if len(camps) != 1 {
		return -1
	}
	n, _ := camps[0].(map[string]any)["pending_follow_up"].(float64)
	return int(n)
}

func summaryHasLead(parsed map[string]any, leadID string) bool {
	camps, _ := parsed["campaigns"].([]any)
	if len(camps) != 1 {
		return false
	}
	ids, _ := camps[0].(map[string]any)["lead_ids"].([]any)
	for _, id := range ids {
		if id == leadID {
			return true
		}
	}
	return false
}

func leadTenant(t *testing.T, h *harness, leadID string) string {
	t.Helper()
	var tenant string
	if err := h.api.St.DB.QueryRow(`SELECT tenant_id FROM leads WHERE id=?`, leadID).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	return tenant
}

func countLeads(t *testing.T, h *harness, tenant string) int {
	t.Helper()
	var n int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(1) FROM leads WHERE tenant_id=?`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pendingFollowUp(t *testing.T, h *harness, tenant, campaign string) int {
	t.Helper()
	code, parsed, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref="+campaign, "", tenant, "")
	if code != 200 {
		t.Fatalf("summary status=%d body=%v", code, parsed)
	}
	if pendingCount(parsed) < 0 {
		t.Fatalf("campaigns=%v", parsed)
	}
	return pendingCount(parsed)
}
