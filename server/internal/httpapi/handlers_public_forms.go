// HUI-1679 / FEAT-0180 版本化留资表单:公共(无商家登录)端点。
//
//   - GET  /public/forms/{id}            公共渲染描述符(落地页用;仅营销面字段,无租户 id)
//   - POST /public/forms/{id}/submissions   公共提交(校验/限流/幂等/原子落库)
//
// 红线:
//   - 接收租户恒为表单归属租户;X-Tenant-ID 头与 body 内任何租户字段结构性无效;
//   - 无自由字段:payload 键必须 ⊆ 表单 fields 白名单 ∪ 控制字段;
//   - 联系方式零 URL/零日志明文(手机号只记指纹前缀);
//   - marketing_allowed 独立勾选、缺省 false;没有同意不进营销池;
//   - 限流:每 (IP, 表单) 30 次/分钟,进程内固定窗口;第 31 次 429;
//   - 幂等:同 (tenant, form, source, source_ref) 返回原 submission 零写入;
//     同 contact+form 短窗内重复提交幂等提示不新建(窗外咨询归 intake 三分类)。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const (
	// formRatePerMinute is the default per-(IP, form) submission quota.
	formRatePerMinute = 30
	// formMaxBodyBytes is the public submission body hard cap.
	formMaxBodyBytes = 1 << 14 // 16 KiB
	// formResubmitWindowDefault is the same contact+form suppression window.
	formResubmitWindowDefault = 10 * time.Minute
	// formRevokeHint is shown on the submission success page (撤销渠道文案).
	formRevokeHint = "如需停止接收营销信息,可联系商家,或在商家 CRM 客户档案页对该来源授权执行「撤销/停止营销」。撤销不可恢复。"
)

// FormRatePerMinute / FormResubmitWindow are Server knobs (tests inject).
// The rate limiter is in-process by design: single-writer sqlite, SMB scale.
func (s *Server) formLimiter() *formRateLimiter {
	s.formLimitOnce.Do(func() {
		limit := s.FormRatePerMinute
		if limit <= 0 {
			limit = formRatePerMinute
		}
		s.formLimit = newFormRateLimiter(limit)
	})
	return s.formLimit
}

func (s *Server) formSubmitLimit() int {
	if s.FormRatePerMinute > 0 {
		return s.FormRatePerMinute
	}
	return formRatePerMinute
}

func (s *Server) formWindowSeconds() int {
	if s.FormResubmitWindow > 0 {
		return int(s.FormResubmitWindow.Seconds())
	}
	return int(formResubmitWindowDefault.Seconds())
}

// ---- in-process fixed-window rate limiter ---------------------------------------

type formRateLimiter struct {
	mu    sync.Mutex
	limit int
	// windows maps "ip|form" -> (minute epoch, count).
	windows map[string]*formWindow
}

type formWindow struct {
	epoch int64
	count int
}

func newFormRateLimiter(limit int) *formRateLimiter {
	return &formRateLimiter{limit: limit, windows: map[string]*formWindow{}}
}

// allow answers whether one more request from key fits the current minute.
func (rl *formRateLimiter) allow(key string, now time.Time) bool {
	epoch := now.Unix() / 60
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if len(rl.windows) > 8192 {
		// bounded memory: drop stale windows in one sweep
		for k, w := range rl.windows {
			if w.epoch != epoch {
				delete(rl.windows, k)
			}
		}
	}
	w, ok := rl.windows[key]
	if !ok || w.epoch != epoch {
		rl.windows[key] = &formWindow{epoch: epoch, count: 1}
		return rl.limit >= 1
	}
	if w.count >= rl.limit {
		return false
	}
	w.count++
	return true
}

// clientIP prefers the left-most X-Forwarded-For hop (set by the BFF layer);
// behind proxies without the header this degrades to the socket address,
// which only ever errs on the strict side (limits still apply, just broader).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if first != "" && len(first) <= 64 {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- public render descriptor ----------------------------------------------------

// handlePublicFormGet answers the landing page's render descriptor: the fixed
// field set, notice version, purpose and the marketing prompt. It carries NO
// tenant id and NO contact data — only the marketing surface of the form.
func (s *Server) handlePublicFormGet(w http.ResponseWriter, r *http.Request) {
	f, ok := s.publicFormOrFail(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, publicFormDescriptor(f, formRevokeHint))
}

// publicFormOrFail resolves the form for the public surface and enforces the
// same visibility discipline as submissions: drafts answer 404 (never publicly
// visible), disabled/expired answer 410.
func (s *Server) publicFormOrFail(w http.ResponseWriter, r *http.Request) (store.Form, bool) {
	f, err := s.St.GetFormAnyTenant(r.PathValue("id"))
	if errors.Is(err, store.ErrFormNotFound) {
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return store.Form{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "form lookup failed")
		return store.Form{}, false
	}
	if f.Status == store.FormStatusDisabled {
		fail(w, http.StatusGone, "form_disabled", "this form is no longer accepting submissions")
		return store.Form{}, false
	}
	if f.Status != store.FormStatusPublished {
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return store.Form{}, false
	}
	if store.FormExpiredAt(f, time.Now()) {
		fail(w, http.StatusGone, "form_expired", "this form has expired and no longer accepts submissions")
		return store.Form{}, false
	}
	return f, true
}

func publicFormDescriptor(f store.Form, revokeHint string) map[string]any {
	def, _, _ := store.ValidateFormFieldsJSON(string(f.Fields))
	fields := make([]map[string]any, 0, len(def.Fields))
	for _, fl := range def.Fields {
		fields = append(fields, map[string]any{
			"name":     fl.Name,
			"type":     store.FormFieldType(fl.Name),
			"required": fl.Required,
		})
	}
	return map[string]any{
		"form_id": f.ID,
		"version": f.Version,
		"status":  f.Status,
		"schema": map[string]any{
			"schema_version":   def.SchemaVersion,
			"consent_required": def.ConsentRequired,
			"fields":           fields,
		},
		"notice_version":    f.NoticeVersion,
		"purpose":           f.Purpose,
		"marketing_prompt":  f.MarketingPrompt,
		"marketing_default": false, // 缺省不勾:没有同意不进营销池
		"revoke_hint":       revokeHint,
	}
}

// formSchemaContract is the owner-facing contract export (T1 对齐锚).
func formSchemaContract(f store.Form, ratePerMinute, windowSeconds int) map[string]any {
	out := publicFormDescriptor(f, formRevokeHint)
	out["form_key"] = f.FormKey
	out["submit"] = map[string]any{
		"method":          "POST",
		"path":            "/api/v1/public/forms/" + f.ID + "/submissions",
		"idempotency_key": []string{"source", "source_ref"},
	}
	out["limits"] = map[string]any{
		"per_ip_per_form_per_minute": ratePerMinute,
		"resubmit_window_seconds":    windowSeconds,
	}
	return out
}

// ---- public submission ------------------------------------------------------------

// handlePublicFormSubmit is the ONLY write path for unauthenticated visitors.
// The receiving tenant always comes from the form row.
func (s *Server) handlePublicFormSubmit(w http.ResponseWriter, r *http.Request) {
	formID := r.PathValue("id")

	// 1. rate limit first: junk floods must not even reach the DB.
	if !s.formLimiter().allow(clientIP(r)+"|"+formID, time.Now()) {
		fail(w, http.StatusTooManyRequests, "rate_limited",
			"too many submissions from this address for this form; retry in a minute")
		return
	}

	// 2. strict body decode + unknown-key rejection (无自由字段;伪造租户参数
	// 不在白名单内,结构性无效)。
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, formMaxBodyBytes))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable (max 16 KiB)")
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	var payloadIn struct {
		Name             string `json:"name"`
		Phone            string `json:"phone"`
		Wechat           string `json:"wechat"`
		MarketingAllowed bool   `json:"marketing_allowed"`
		Source           string `json:"source"`
		SourceRef        string `json:"source_ref"`
	}
	if err := json.Unmarshal(body, &payloadIn); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	allowedKeys := map[string]bool{
		"name": true, "phone": true, "wechat": true,
		"marketing_allowed": true, "source": true, "source_ref": true,
	}
	var unknown []string
	for k := range raw {
		if !allowedKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		fail(w, http.StatusBadRequest, "unknown_fields",
			"payload keys are limited to the form's fixed fields (name/phone/wechat) plus marketing_allowed/source/source_ref; unexpected: "+
				strings.Join(unknown, ", "))
		return
	}
	payload := store.SubmissionPayload{
		Name:             payloadIn.Name,
		Phone:            payloadIn.Phone,
		Wechat:           payloadIn.Wechat,
		MarketingAllowed: payloadIn.MarketingAllowed,
		Source:           payloadIn.Source,
		SourceRef:        payloadIn.SourceRef,
	}

	// 3. the form decides everything (state, tenant, fields whitelist).
	f, err := s.St.GetFormAnyTenant(formID)
	if errors.Is(err, store.ErrFormNotFound) {
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "form lookup failed")
		return
	}
	if f.Status == store.FormStatusDisabled {
		fail(w, http.StatusGone, "form_disabled", "this form is no longer accepting submissions")
		return
	}
	if f.Status != store.FormStatusPublished {
		// drafts are never publicly visible: same answer as a missing form
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return
	}
	if store.FormExpiredAt(f, time.Now()) {
		fail(w, http.StatusGone, "form_expired", "this form has expired and no longer accepts submissions")
		return
	}

	// 4. per-item validation against THIS form's fields whitelist.
	if details := store.ValidateSubmissionPayload(string(f.Fields), payload); len(details) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "validation_failed",
			"message": "one or more fields are invalid",
			"details": details,
		})
		return
	}

	// 5. dedup pepper gate (intake fingerprints): fail closed.
	if strings.TrimSpace(s.Cfg.DedupPepper) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config_gate_dedup",
			"message": "LEADS_DEDUP_PEPPER is required for form submissions (fail-closed)",
			"detail":  []string{"LEADS_DEDUP_PEPPER is required (HMAC pepper for the intake phone fingerprint)"},
		})
		return
	}

	// 6. atomic: intake classification + consent + submission in one tx.
	window := s.FormResubmitWindow
	if window <= 0 {
		window = formResubmitWindowDefault
	}
	tx, err := s.St.DB.Begin()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "submission begin failed")
		return
	}
	defer tx.Rollback()
	res, err := store.SubmitFormInTx(tx, store.SubmitFormInput{
		FormID:         f.ID,
		Payload:        payload,
		Content:        canonicalRequestBytes(payload),
		Pepper:         s.Cfg.DedupPepper,
		ResubmitWindow: window,
	})
	if errors.Is(err, store.ErrEventContentConflict) {
		fail(w, http.StatusConflict, "event_content_conflict",
			"the same submission reference was delivered with different content; use a new source_ref")
		return
	}
	if err != nil {
		if msg, ok := intakeValidationError(err); ok {
			fail(w, http.StatusBadRequest, "bad_request", msg)
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "submission failed")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "submission commit failed")
		return
	}

	// 日志:无手机号/姓名明文可免则免 —— 姓名走 redact.Person,手机号只记指纹前缀。
	s.Log.Printf("form submit tenant=%s form=%s v=%d source=%s ref=%s submission=%s contact=%s lead=%s class=%s dup=%s marketing=%t phone_fpr=%s",
		f.TenantID, f.ID, f.Version, payload.Source, payload.SourceRef,
		res.SubmissionID, res.ContactID, res.LeadID, res.Class, duplicateKind(res),
		res.MarketingAllowed, shortFPR(s.Cfg.DedupPepper, payload.Phone))

	status := http.StatusCreated
	if res.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"submission_id":     res.SubmissionID,
		"contact_id":        res.ContactID,
		"lead_id":           res.LeadID,
		"consent_id":        res.ConsentID,
		"class":             res.Class,
		"duplicate":         res.Duplicate,
		"duplicate_kind":    duplicateKind(res),
		"marketing_allowed": res.MarketingAllowed,
		"notice_version":    f.NoticeVersion,
		"revoke_hint":       formRevokeHint,
	})
}

func duplicateKind(res store.SubmitFormResult) string {
	if !res.Duplicate {
		return "none"
	}
	if res.DuplicateKind == "" {
		return "idempotent"
	}
	return res.DuplicateKind
}

// canonicalRequestBytes is the hashed content of one submission (fixed order,
// normalized phone) — the ledger stores only its sha256, never the payload.
func canonicalRequestBytes(p store.SubmissionPayload) []byte {
	type wire struct {
		Name             string `json:"name"`
		Phone            string `json:"phone"`
		Wechat           string `json:"wechat,omitempty"`
		MarketingAllowed bool   `json:"marketing_allowed"`
		Source           string `json:"source"`
		SourceRef        string `json:"source_ref"`
	}
	b, _ := json.Marshal(wire{
		Name:             strings.TrimSpace(p.Name),
		Phone:            store.NormalizePhone(p.Phone),
		Wechat:           strings.TrimSpace(p.Wechat),
		MarketingAllowed: p.MarketingAllowed,
		Source:           p.Source,
		SourceRef:        p.SourceRef,
	})
	return b
}
