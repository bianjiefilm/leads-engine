// HUI-1686 / FEAT-0187 无效线索过滤 HTTP E2E(登记制开关 FEATURE_LEADS_FILTER):
//   - off(默认):行为与既往一致 —— 响应无 filter_reason 键,线索照常 new;
//   - on:无效联系方式 → 201 台账留痕(三分类不变),响应与 GET lead 均带
//     status=filtered + 机器原因码;同号重投仍走 repeat_consult 去重;
//     日志只出现原因码与指纹前缀,零联系方式原文(PII 纪律)。
package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestLeadIntakeFilterFlagOff(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx", "evt-f0", "丙商家", "12345678901", ""), http.StatusCreated)
	if out["class"] != "new" {
		t.Fatalf("class = %v, want new", out["class"])
	}
	if _, present := out["filter_reason"]; present {
		t.Fatalf("filter_reason must be absent with the flag off, got %v", out["filter_reason"])
	}
	leadID, _ := out["lead_id"].(string)
	lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
	if lead["status"] != "new" {
		t.Fatalf("lead status = %v, want new (off = 完全沿用现行为)", lead["status"])
	}
	if _, present := lead["filter_reason"]; present {
		t.Fatalf("filter_reason must be absent with the flag off, got %v", lead["filter_reason"])
	}
}

func TestLeadIntakeFilterFlagOn(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLeadsFilter: true, captureLog: true})
	tenantA, _, _ := h.seed()

	var garbageContact string
	t.Run("invalid phone is ledgered as filtered with a machine reason", func(t *testing.T) {
		out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx", "evt-f1", "丙商家", "12345678901", ""), http.StatusCreated)
		if out["class"] != "new" || out["filter_reason"] != "invalid_phone" {
			t.Fatalf("intake = %v, want class new + filter_reason invalid_phone (三分类不变)", out)
		}
		leadID, _ := out["lead_id"].(string)
		garbageContact, _ = out["contact_id"].(string)
		if leadID == "" || garbageContact == "" {
			t.Fatalf("intake response missing ids: %v", out)
		}
		lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
		if lead["status"] != "filtered" || lead["filter_reason"] != "invalid_phone" {
			t.Fatalf("lead = %v, want filtered/invalid_phone", lead)
		}
	})

	t.Run("valid +86 formatted number passes untouched", func(t *testing.T) {
		body := `{"source_app":"touch","source_ns":"wx","event_id":"evt-f2","contact":{"name":"丁商家","phone":"+86 139-0000-0009","email":""},"business_category":"merchant_customer","source_type":"form"}`
		out := h.intake(sessionOwnerA, tenantA, body, http.StatusCreated)
		if _, present := out["filter_reason"]; present {
			t.Fatalf("valid lead must carry no filter_reason, got %v", out["filter_reason"])
		}
		leadID, _ := out["lead_id"].(string)
		lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
		if lead["status"] != "new" {
			t.Fatalf("valid lead status = %v, want new", lead["status"])
		}
	})

	t.Run("invalid email reason code", func(t *testing.T) {
		out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx", "evt-f3", "戊商家", "13900000008", "broken-at"), http.StatusCreated)
		if out["filter_reason"] != "invalid_email" {
			t.Fatalf("filter_reason = %v, want invalid_email", out["filter_reason"])
		}
		leadID, _ := out["lead_id"].(string)
		lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
		if lead["status"] != "filtered" || lead["filter_reason"] != "invalid_email" {
			t.Fatalf("lead = %v, want filtered/invalid_email", lead)
		}
	})

	t.Run("repeated invalid number still dedups (repeat_consult, same contact)", func(t *testing.T) {
		out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "landing", "evt-f4", "丙商家", "123 4567 8901", ""), http.StatusCreated)
		if out["class"] != "repeat_consult" || out["contact_id"] != garbageContact {
			t.Fatalf("repeat = %v, want repeat_consult on contact %s (无效号码参与去重)", out, garbageContact)
		}
		if out["filter_reason"] != "invalid_phone" {
			t.Fatalf("filter_reason = %v, want invalid_phone", out["filter_reason"])
		}
	})

	t.Run("logs carry reason codes and fingerprints only", func(t *testing.T) {
		logs := h.logs.String()
		if !strings.Contains(logs, "invalid_phone") {
			t.Fatal("filtered intake must be visible in the log by machine reason")
		}
		if strings.Contains(logs, "12345678901") {
			t.Fatal("raw phone must never reach the log")
		}
	})
}
