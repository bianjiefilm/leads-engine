package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// HUI-1678：企业资料筛选不等于个人营销名单。
// 样本由测试请求带入（客户声明有权导入），不预置公开企业库，也不写 Notify 收件箱冒充生产接收。

func TestEnterpriseDirectoryOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()
	h.mustDo("GET", "/api/v1/enterprise-directory/capability", sessionOwnerA, tenantA, "", http.StatusNotFound)
}

func TestEnterpriseDirectoryScreenPreviewConfirm(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureEnterpriseDir: true})
	tenantA, tenantB, _ := h.seed()

	t.Run("capability records that no official directory is connected", func(t *testing.T) {
		out := h.mustDo("GET", "/api/v1/enterprise-directory/capability", sessionOwnerA, tenantA, "", http.StatusOK)
		if out["official_directory"] != "unavailable" {
			t.Fatalf("official_directory = %v", out["official_directory"])
		}
		reason, _ := out["reason"].(string)
		if reason == "" {
			t.Fatal("capability must explain the missing official source")
		}
		if out["accepted_source"] != "customer_authorized_import" || out["marketing_implied"] != false || out["outbound_linked"] != false {
			t.Fatalf("capability = %v", out)
		}
	})

	t.Run("agent without a grant cannot screen", func(t *testing.T) {
		h2 := newHarnessOpts(t, harnessOpts{featureEnterpriseDir: true})
		ta, _, _ := h2.seed()
		h2.mustDo("GET", "/api/v1/enterprise-directory/capability", sessionAgentA, ta, "", http.StatusForbidden)
	})

	freshAt := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	expiredAt := time.Now().UTC().Add(-400 * 24 * time.Hour).Format(time.RFC3339)
	wholesale := entRecord("91310000MA1FL2XW3X", "示例商贸", "批发", "上海", "小微", "", "", "")
	missing := entRecord("91110000MA00ABCD1X", "缺行业企业", "", "北京", "中型", "张三", "13900001111", "")
	created := h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-export-1", "客户自有导出", freshAt, 30, []map[string]any{wholesale, missing}),
		http.StatusCreated)
	records, _ := created["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("import records = %d, want 2 (%v)", len(records), created)
	}
	if h.leadCount(sessionOwnerA, tenantA) != 0 {
		t.Fatal("import must not create leads before confirm")
	}

	again := h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-export-1", "客户自有导出", freshAt, 30, []map[string]any{wholesale, missing}),
		http.StatusOK)
	againRecords, _ := again["records"].([]any)
	if len(againRecords) != 2 {
		t.Fatalf("replay import grew records: %v", again)
	}

	listed := h.mustDo("GET", "/api/v1/enterprise-directory/records?industry=批发&region=上海&scale=小微",
		sessionOwnerA, tenantA, "", http.StatusOK)
	items, _ := listed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("screened items = %d, want 1 (%v)", len(items), listed)
	}
	row := items[0].(map[string]any)
	if row["freshness"] != "fresh" || row["marketing_consent"] != false {
		t.Fatalf("screen row = %v", row)
	}
	uses, _ := row["allowed_uses"].([]any)
	if len(uses) != 1 || uses[0] != "enterprise_profile" {
		t.Fatalf("allowed_uses = %v", uses)
	}
	person, _ := row["person"].(map[string]any)
	if person["phone"] != "" {
		t.Fatalf("enterprise screen must not surface a person phone as the company row phone: %v", person)
	}

	h.mustDo("GET", "/api/v1/enterprise-directory/records?person_phone=13900001111",
		sessionOwnerA, tenantA, "", http.StatusBadRequest)

	all := h.mustDo("GET", "/api/v1/enterprise-directory/records", sessionOwnerA, tenantA, "", http.StatusOK)
	allItems, _ := all["items"].([]any)
	var missingID, wholesaleID string
	for _, it := range allItems {
		m := it.(map[string]any)
		switch m["enterprise_id"] {
		case "91110000MA00ABCD1X":
			missingID, _ = m["id"].(string)
			miss, _ := m["missing_fields"].([]any)
			if !containsAny(miss, "industry") {
				t.Fatalf("missing industry not marked: %v", m)
			}
			p, _ := m["person"].(map[string]any)
			if p["name"] != "张三" {
				t.Fatalf("person name must stay beside the enterprise, got %v", p)
			}
		case "91310000MA1FL2XW3X":
			wholesaleID, _ = m["id"].(string)
		}
	}
	if missingID == "" || wholesaleID == "" {
		t.Fatalf("record ids missing in %v", all)
	}

	preview := h.mustDo("GET", "/api/v1/enterprise-directory/records/"+wholesaleID+"/preview",
		sessionOwnerA, tenantA, "", http.StatusOK)
	if preview["will_enter_pool"] != false || preview["marketing_consent"] != false || preview["outbound"] != "none" {
		t.Fatalf("preview = %v", preview)
	}

	confirmed := h.mustDo("POST", "/api/v1/enterprise-directory/records/"+wholesaleID+"/confirm",
		sessionOwnerA, tenantA, "", http.StatusCreated)
	leadID, _ := confirmed["lead_id"].(string)
	contactID, _ := confirmed["contact_id"].(string)
	if leadID == "" || contactID == "" || confirmed["duplicate"] != false || confirmed["marketing_consent"] != false {
		t.Fatalf("confirm = %v", confirmed)
	}
	if confirmed["source_app"] != "customer_authorized_import" {
		t.Fatalf("source_app = %v", confirmed["source_app"])
	}
	if h.leadCount(sessionOwnerA, tenantA) != 1 {
		t.Fatal("confirm must create exactly one lead")
	}
	contact := h.mustDo("GET", "/api/v1/contacts/"+contactID, sessionOwnerA, tenantA, "", http.StatusOK)
	if contact["phone"] != "" || contact["email"] != "" {
		t.Fatalf("person contact facts must not be copied onto the enterprise candidate: %v", contact)
	}
	if contact["name"] != "示例商贸" || contact["consent_status"] != "pending" {
		t.Fatalf("contact = %v", contact)
	}
	consents := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
	summary, _ := consents["summary"].(map[string]any)
	if summary["marketing_active"] != float64(0) {
		t.Fatalf("import confirm granted marketing: %v", consents)
	}
	lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
	if lead["source_ref_id"] == "" {
		t.Fatalf("lead missing provenance: %v", lead)
	}
	var inbox int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(1) FROM notify_inbox`).Scan(&inbox); err != nil {
		t.Fatal(err)
	}
	if inbox != 0 {
		t.Fatalf("confirm wrote %d notify inbox rows; that would pretend a production receive", inbox)
	}

	replay := h.mustDo("POST", "/api/v1/enterprise-directory/records/"+wholesaleID+"/confirm",
		sessionOwnerA, tenantA, "", http.StatusOK)
	if replay["lead_id"] != leadID || replay["duplicate"] != true {
		t.Fatalf("replay = %v", replay)
	}
	if h.leadCount(sessionOwnerA, tenantA) != 1 {
		t.Fatal("replay confirm created another lead")
	}

	h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-export-1", "客户自有导出", freshAt, 30, []map[string]any{
			entRecord("91310000MA1FL2XW3X", "示例商贸", "零售", "上海", "小微", "", "", ""),
		}), http.StatusOK)
	conflicted := h.mustDo("POST", "/api/v1/enterprise-directory/records/"+wholesaleID+"/confirm",
		sessionOwnerA, tenantA, "", http.StatusConflict)
	if conflicted["error"] != "event_content_conflict" {
		t.Fatalf("changed refresh = %v", conflicted)
	}
	if h.leadCount(sessionOwnerA, tenantA) != 1 {
		t.Fatal("conflicting refresh created another lead")
	}
	marked := h.mustDo("GET", "/api/v1/enterprise-directory/records?industry=零售", sessionOwnerA, tenantA, "", http.StatusOK)
	markedItems, _ := marked["items"].([]any)
	if len(markedItems) != 1 || markedItems[0].(map[string]any)["conflict"] != true {
		t.Fatalf("conflict not marked: %v", marked)
	}

	expired := h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-old", "过期导出", expiredAt, 30, []map[string]any{
			entRecord("91440300MA5DXXXX1A", "过期企业", "制造", "深圳", "小型", "", "", ""),
		}), http.StatusCreated)
	expRows, _ := expired["records"].([]any)
	expID, _ := expRows[0].(map[string]any)["id"].(string)
	expPreview := h.mustDo("GET", "/api/v1/enterprise-directory/records/"+expID+"/preview", sessionOwnerA, tenantA, "", http.StatusOK)
	if expPreview["freshness"] != "expired" {
		t.Fatalf("stale sample not marked expired: %v", expPreview)
	}

	staleCo := h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-mixed", "混合导出", expiredAt, 30, []map[string]any{
			entRecord("91440300MA5DSTALE1", "同来源过期企业", "制造", "深圳", "小型", "", "", ""),
		}), http.StatusCreated)
	h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-mixed", "混合导出", freshAt, 30, []map[string]any{
			entRecord("91440300MA5DFRESH1", "同来源新企业", "制造", "深圳", "小型", "", "", ""),
		}), http.StatusCreated)
	_ = staleCo
	mixed := h.mustDo("GET", "/api/v1/enterprise-directory/records?industry=制造&region=深圳&scale=小型",
		sessionOwnerA, tenantA, "", http.StatusOK)
	var staleFreshness, freshFreshness string
	for _, it := range mixed["items"].([]any) {
		m := it.(map[string]any)
		switch m["enterprise_id"] {
		case "91440300MA5DSTALE1":
			staleFreshness, _ = m["freshness"].(string)
		case "91440300MA5DFRESH1":
			freshFreshness, _ = m["freshness"].(string)
		}
	}
	if staleFreshness != "expired" || freshFreshness != "fresh" {
		t.Fatalf("later import rewrote sibling freshness: stale=%q fresh=%q body=%v", staleFreshness, freshFreshness, mixed)
	}

	h.mustDo("POST", "/api/v1/enterprise-directory/records/"+missingID+"/refuse", sessionOwnerA, tenantA, "", http.StatusOK)
	refused := h.mustDo("POST", "/api/v1/enterprise-directory/records/"+missingID+"/confirm", sessionOwnerA, tenantA, "", http.StatusConflict)
	if refused["error"] != "suppression_held" {
		t.Fatalf("refused confirm = %v", refused)
	}
	h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-export-1", "客户自有导出", freshAt, 30, []map[string]any{missing}),
		http.StatusOK)
	refusedAgain := h.mustDo("POST", "/api/v1/enterprise-directory/records/"+missingID+"/confirm", sessionOwnerA, tenantA, "", http.StatusConflict)
	if refusedAgain["error"] != "suppression_held" {
		t.Fatalf("refresh after refuse = %v", refusedAgain)
	}

	h.mustDo("POST", "/api/v1/enterprise-directory/records/"+wholesaleID+"/delete", sessionOwnerA, tenantA, "", http.StatusOK)
	deleted := h.mustDo("POST", "/api/v1/enterprise-directory/records/"+wholesaleID+"/confirm", sessionOwnerA, tenantA, "", http.StatusConflict)
	if deleted["error"] != "suppression_held" {
		t.Fatalf("deleted confirm = %v", deleted)
	}
	h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerA, tenantA,
		enterpriseImportBody("cust-export-1", "客户自有导出", freshAt, 30, []map[string]any{wholesale}),
		http.StatusOK)
	stillHeld := h.mustDo("POST", "/api/v1/enterprise-directory/records/"+wholesaleID+"/confirm", sessionOwnerA, tenantA, "", http.StatusConflict)
	if stillHeld["error"] != "suppression_held" || h.leadCount(sessionOwnerA, tenantA) != 1 {
		t.Fatalf("refresh after delete reactivated marketing pool: leads=%d body=%v", h.leadCount(sessionOwnerA, tenantA), stillHeld)
	}
	consents = h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
	summary, _ = consents["summary"].(map[string]any)
	if summary["marketing_active"] != float64(0) {
		t.Fatalf("delete/refresh reactivated marketing: %v", consents)
	}

	h.mustDo("GET", "/api/v1/enterprise-directory/records", sessionOwnerB, tenantA, "", http.StatusForbidden)
	emptyB := h.mustDo("GET", "/api/v1/enterprise-directory/records", sessionOwnerB, tenantB, "", http.StatusOK)
	bItems, _ := emptyB["items"].([]any)
	if len(bItems) != 0 {
		t.Fatalf("tenant B saw tenant A records: %v", emptyB)
	}
	h.mustDo("GET", "/api/v1/enterprise-directory/records/"+wholesaleID+"/preview", sessionOwnerB, tenantB, "", http.StatusNotFound)
	bCreated := h.mustDo("POST", "/api/v1/enterprise-directory/imports", sessionOwnerB, tenantB,
		enterpriseImportBody("cust-export-1", "客户自有导出", freshAt, 30, []map[string]any{wholesale}),
		http.StatusCreated)
	bRows, _ := bCreated["records"].([]any)
	bID, _ := bRows[0].(map[string]any)["id"].(string)
	if bID == wholesaleID {
		t.Fatal("cross-tenant import reused tenant A record id")
	}
	h.mustDo("POST", "/api/v1/enterprise-directory/records/"+bID+"/confirm", sessionOwnerB, tenantB, "", http.StatusCreated)
	if h.leadCount(sessionOwnerA, tenantA) != 1 || h.leadCount(sessionOwnerB, tenantB) != 1 {
		t.Fatal("cross-tenant confirm shared a lead pool")
	}
}

func (h *harness) leadCount(session, tenant string) int {
	h.t.Helper()
	out := h.mustDo("GET", "/api/v1/leads", session, tenant, "", http.StatusOK)
	items, _ := out["items"].([]any)
	return len(items)
}

func enterpriseImportBody(sourceKey, sourceName, collectedAt string, cycle int, records []map[string]any) string {
	body := map[string]any{
		"source_key":        sourceKey,
		"source_name":       sourceName,
		"collected_at":      collectedAt,
		"update_cycle_days": cycle,
		"license":           "客户声明对本份导出拥有再使用权，仅限本租户筛选",
		"correction":        "客户可在本页删除；删除或拒绝后，同一企业资料刷新不得恢复营销",
		"records":           records,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func entRecord(id, name, industry, region, scale, personName, personPhone, personEmail string) map[string]any {
	return map[string]any{
		"enterprise_id":   id,
		"enterprise_name": name,
		"industry":        industry,
		"region":          region,
		"scale":           scale,
		"person_name":     personName,
		"person_phone":    personPhone,
		"person_email":    personEmail,
	}
}

func containsAny(items []any, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}
