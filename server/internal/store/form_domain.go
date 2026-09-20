// HUI-1679 / FEAT-0180 版本化留资表单域存储层。
//
// 首版收敛:固定字段留资表单(name/phone/wechat 固定枚举)+ 版本化 schema。
// 语义红线:
//   - 无自由字段:fields JSON 只接受固定枚举白名单;payload 键必须 ⊆ 白名单;
//   - 版本不可变:发布后表单配置不可改,改配置 = 同 (tenant, store, form_key) 族
//     新版本行(version 递增);
//   - 接收租户恒为表单归属租户:submission 的 tenant_id 永远取自 form 行,
//     不存在任何来自请求的租户输入;
//   - 没有同意不进营销池:marketing_allowed 独立勾选、缺省 false;consent 行
//     恒写(false = 仅建档,不进任何营销触达范围;触达语义归 HUI-1689);
//   - 幂等:同 (tenant, form, source, source_ref) 重试返回原 submission 零写入;
//     同 contact+form 短窗内重复提交幂等提示不新建(真实咨询次数语义归
//     HUI-1683 intake:短窗外再次咨询走 IntakeLeadInTx 三分类,repeat_consult
//     新 lead 关联既有 contact,咨询次数不丢);
//   - payload 原文不入库,只留 sha256;联系方式只落 contacts/consents 域表。
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Form lifecycle statuses (CHECK domain of forms.status).
const (
	FormStatusDraft     = "draft"
	FormStatusPublished = "published"
	FormStatusDisabled  = "disabled"
	FormStatusExpired   = "expired"
)

// Domain errors surfaced to the HTTP layer for explicit status mapping.
var (
	ErrFormNotFound       = errors.New("form: not found")
	ErrFormNotDraft       = errors.New("form: only drafts can be edited")
	ErrFormNotPublishable = errors.New("form: only drafts can be published")
	ErrFormDisabled       = errors.New("form: disabled")
	ErrFormExpired        = errors.New("form: expired")
	ErrFormIncomplete     = errors.New("form: notice_version and purpose are required to publish")
	ErrSourceInvalid      = errors.New("form: source/source_ref must be ASCII identifiers")
)

// FieldError is one explicit per-item validation failure (逐项明确).
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ErrPayloadValidation carries the accumulated field errors.
type ErrPayloadValidation struct{ Details []FieldError }

func (e *ErrPayloadValidation) Error() string {
	return "form: payload validation failed"
}

// Fixed field enum (首版固定枚举,不开放自由字段).
const (
	FormFieldName   = "name"
	FormFieldPhone  = "phone"
	FormFieldWechat = "wechat"
)

// Value caps (runes unless stated).
const (
	FormNameMaxRunes    = 100
	FormWechatMaxRunes  = 64
	FormSourceMaxRunes  = 64
	FormSourceRefMax    = 128
	FormNoticeMaxRunes  = 64
	FormPurposeMaxRunes = 64
	FormPromptMaxRunes  = 500
)

// FormFieldsDef is the versioned fields JSON contract (schema_version=1).
type FormFieldsDef struct {
	SchemaVersion   int            `json:"schema_version"`
	Fields          []FormFieldDef `json:"fields"`
	ConsentRequired bool           `json:"consent_required"`
}

// FormFieldDef is one fixed field's configuration. Name is from the fixed enum.
type FormFieldDef struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// FormFieldType is the render/validation type exposed in the schema contract.
func FormFieldType(name string) string {
	if name == FormFieldPhone {
		return "phone_cn"
	}
	return "text"
}

// ValidateFormFieldsJSON parses and canonically re-encodes a fields JSON value.
// 白名单纪律:字段名必须在固定枚举内、不得重复;name/phone 恒为必填(首版拍板);
// schema_version 固定为 1。返回 canonical JSON for storage.
func ValidateFormFieldsJSON(raw string) (FormFieldsDef, string, error) {
	def := FormFieldsDef{}
	if strings.TrimSpace(raw) == "" {
		def = defaultFormFields()
	} else if err := json.Unmarshal([]byte(raw), &def); err != nil {
		return FormFieldsDef{}, "", errors.New("form: fields must be valid JSON")
	}
	if def.SchemaVersion == 0 {
		def.SchemaVersion = 1
	}
	if def.SchemaVersion != 1 {
		return FormFieldsDef{}, "", errors.New("form: fields.schema_version must be 1")
	}
	if len(def.Fields) == 0 {
		def = defaultFormFields()
	}
	seen := map[string]bool{}
	for _, f := range def.Fields {
		if f.Name != FormFieldName && f.Name != FormFieldPhone && f.Name != FormFieldWechat {
			return FormFieldsDef{}, "", errors.New("form: fields names are limited to the fixed enum name/phone/wechat")
		}
		if seen[f.Name] {
			return FormFieldsDef{}, "", errors.New("form: duplicate field " + f.Name)
		}
		seen[f.Name] = true
	}
	// name/phone 恒必填:缺失即补齐为 required,配置里试图放开会被规范化回来。
	for i := range def.Fields {
		if def.Fields[i].Name == FormFieldName || def.Fields[i].Name == FormFieldPhone {
			def.Fields[i].Required = true
		}
	}
	for _, req := range []string{FormFieldName, FormFieldPhone} {
		if !seen[req] {
			def.Fields = append(def.Fields, FormFieldDef{Name: req, Required: true})
		}
	}
	// canonical order: keep a stable order for the contract (name, phone, wechat).
	order := map[string]int{FormFieldName: 0, FormFieldPhone: 1, FormFieldWechat: 2}
	fs := def.Fields
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && order[fs[j].Name] < order[fs[j-1].Name]; j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
	b, err := json.Marshal(def)
	if err != nil {
		return FormFieldsDef{}, "", err
	}
	return def, string(b), nil
}

func defaultFormFields() FormFieldsDef {
	return FormFieldsDef{SchemaVersion: 1, Fields: []FormFieldDef{
		{Name: FormFieldName, Required: true},
		{Name: FormFieldPhone, Required: true},
	}}
}

// ---- form rows ----------------------------------------------------------------

// Form is one versioned form row (每行 = 一个版本).
type Form struct {
	ID              string          `json:"id"`
	TenantID        string          `json:"tenant_id"`
	StoreID         string          `json:"store_id"`
	FormKey         string          `json:"form_key"`
	Version         int             `json:"version"`
	NoticeVersion   string          `json:"notice_version"`
	Purpose         string          `json:"purpose"`
	MarketingPrompt string          `json:"marketing_prompt"`
	Fields          json.RawMessage `json:"fields"`
	Status          string          `json:"status"`
	ExpiresAt       string          `json:"expires_at,omitempty"`
	PublishedAt     string          `json:"published_at,omitempty"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

const formCols = `id,tenant_id,store_id,form_key,version,notice_version,purpose,marketing_prompt,fields,status,expires_at,published_at,created_by,created_at,updated_at`

func scanForm(sc interface{ Scan(...any) error }) (Form, error) {
	var f Form
	var fields, expires, published sql.NullString
	err := sc.Scan(&f.ID, &f.TenantID, &f.StoreID, &f.FormKey, &f.Version, &f.NoticeVersion,
		&f.Purpose, &f.MarketingPrompt, &fields, &f.Status, &expires, &published,
		&f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return Form{}, err
	}
	f.Fields = json.RawMessage(fields.String)
	f.ExpiresAt, f.PublishedAt = expires.String, published.String
	return f, nil
}

// FormDraftInput is the create/patch shape for drafts. Nil pointers mean "no
// change" on patch; on create zero values apply.
type FormDraftInput struct {
	FormKey         *string
	StoreID         *string
	NoticeVersion   *string
	Purpose         *string
	MarketingPrompt *string
	Fields          *string // raw JSON; validated before storage
	ExpiresAt       *string // RFC3339 or "" to clear
}

// DraftInputProblem returns "" or a human-readable 400 message for a draft
// create/patch payload (caps + charset + JSON whitelist + RFC3339).
func DraftInputProblem(in FormDraftInput) string {
	if in.FormKey != nil {
		k := strings.TrimSpace(*in.FormKey)
		if k == "" {
			k = "main"
		}
		if !validKeyToken(k, 64) {
			return "form_key must be an ASCII identifier of at most 64 characters"
		}
	}
	if in.StoreID != nil && runeLen64(*in.StoreID) > 64 {
		return "store_id must be at most 64 characters"
	}
	if in.NoticeVersion != nil && runeLen64(*in.NoticeVersion) > FormNoticeMaxRunes {
		return "notice_version must be at most 64 characters"
	}
	if in.Purpose != nil && runeLen64(*in.Purpose) > FormPurposeMaxRunes {
		return "purpose must be at most 64 characters"
	}
	if in.MarketingPrompt != nil && runeLen64(*in.MarketingPrompt) > FormPromptMaxRunes {
		return "marketing_prompt must be at most 500 characters"
	}
	if in.Fields != nil {
		if _, _, err := ValidateFormFieldsJSON(*in.Fields); err != nil {
			return err.Error()
		}
	}
	if in.ExpiresAt != nil && *in.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, *in.ExpiresAt); err != nil {
			return "expires_at must be an RFC3339 timestamp"
		}
	}
	return ""
}

// CreateFormDraft inserts a new draft as the next version of its form family.
func (s *Store) CreateFormDraft(tenantID string, createdBy string, in FormDraftInput) (Form, error) {
	if msg := DraftInputProblem(in); msg != "" {
		return Form{}, errors.New("form: " + msg)
	}
	formKey := "main"
	if in.FormKey != nil && strings.TrimSpace(*in.FormKey) != "" {
		formKey = strings.TrimSpace(*in.FormKey)
	}
	storeID := ""
	if in.StoreID != nil {
		storeID = strings.TrimSpace(*in.StoreID)
	}
	notice, purpose, prompt := "", "", ""
	if in.NoticeVersion != nil {
		notice = strings.TrimSpace(*in.NoticeVersion)
	}
	if in.Purpose != nil {
		purpose = strings.TrimSpace(*in.Purpose)
	}
	if in.MarketingPrompt != nil {
		prompt = strings.TrimSpace(*in.MarketingPrompt)
	}
	fieldsJSON := ""
	if in.Fields != nil {
		fieldsJSON = *in.Fields
	}
	_, canonical, err := ValidateFormFieldsJSON(fieldsJSON)
	if err != nil {
		return Form{}, err
	}
	expires := ""
	if in.ExpiresAt != nil {
		expires = strings.TrimSpace(*in.ExpiresAt)
	}

	var version int
	if err := s.DB.QueryRow(
		`SELECT COALESCE(MAX(version),0)+1 FROM forms WHERE tenant_id=? AND store_id=? AND form_key=?`,
		tenantID, storeID, formKey).Scan(&version); err != nil {
		return Form{}, err
	}
	f := Form{
		TenantID: tenantID, StoreID: storeID, FormKey: formKey, Version: version,
		NoticeVersion: notice, Purpose: purpose, MarketingPrompt: prompt,
		Fields: json.RawMessage(canonical), Status: FormStatusDraft,
		ExpiresAt: expires, CreatedBy: createdBy,
	}
	f.ID = newID("frm_")
	f.CreatedAt, f.UpdatedAt = now(), now()
	_, err = s.DB.Exec(
		`INSERT INTO forms(`+formCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.TenantID, f.StoreID, f.FormKey, f.Version, f.NoticeVersion, f.Purpose,
		f.MarketingPrompt, string(f.Fields), f.Status, nullable(f.ExpiresAt), nil,
		f.CreatedBy, f.CreatedAt, f.UpdatedAt)
	if err != nil {
		return Form{}, err
	}
	return f, nil
}

// GetForm fetches one form row within the tenant.
func (s *Store) GetForm(id, tenantID string) (Form, error) {
	row := s.DB.QueryRow(`SELECT `+formCols+` FROM forms WHERE id=? AND tenant_id=?`, id, tenantID)
	f, err := scanForm(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Form{}, ErrFormNotFound
	}
	return f, err
}

// GetFormAnyTenant is the public-side lookup: the FORM owns the receiving
// tenant, so the public endpoints resolve the form without a caller tenant.
// It never trusts any request-supplied tenant.
func (s *Store) GetFormAnyTenant(id string) (Form, error) {
	row := s.DB.QueryRow(`SELECT `+formCols+` FROM forms WHERE id=?`, id)
	f, err := scanForm(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Form{}, ErrFormNotFound
	}
	return f, err
}

func (s *Store) ListForms(tenantID string) ([]Form, error) {
	rows, err := s.DB.Query(
		`SELECT `+formCols+` FROM forms WHERE tenant_id=? ORDER BY form_key, version`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Form
	for rows.Next() {
		f, err := scanForm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UpdateFormDraft edits a draft. Published rows are immutable: 改配置 = 新版本行.
func (s *Store) UpdateFormDraft(id, tenantID string, in FormDraftInput) (Form, error) {
	cur, err := s.GetForm(id, tenantID)
	if err != nil {
		return Form{}, err
	}
	if cur.Status != FormStatusDraft {
		return Form{}, ErrFormNotDraft
	}
	if msg := DraftInputProblem(in); msg != "" {
		return Form{}, errors.New("form: " + msg)
	}
	if in.NoticeVersion != nil {
		cur.NoticeVersion = strings.TrimSpace(*in.NoticeVersion)
	}
	if in.Purpose != nil {
		cur.Purpose = strings.TrimSpace(*in.Purpose)
	}
	if in.MarketingPrompt != nil {
		cur.MarketingPrompt = strings.TrimSpace(*in.MarketingPrompt)
	}
	if in.Fields != nil {
		_, canonical, err := ValidateFormFieldsJSON(*in.Fields)
		if err != nil {
			return Form{}, err
		}
		cur.Fields = json.RawMessage(canonical)
	}
	if in.ExpiresAt != nil {
		cur.ExpiresAt = strings.TrimSpace(*in.ExpiresAt)
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE forms SET notice_version=?,purpose=?,marketing_prompt=?,fields=?,expires_at=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.NoticeVersion, cur.Purpose, cur.MarketingPrompt, string(cur.Fields), nullable(cur.ExpiresAt),
		cur.UpdatedAt, cur.ID, tenantID)
	return cur, err
}

// PublishForm moves draft -> published. 发布校验:notice_version/purpose 非空;
// 发布后行不可变(版本冻结)。
func (s *Store) PublishForm(id, tenantID string) (Form, error) {
	cur, err := s.GetForm(id, tenantID)
	if err != nil {
		return Form{}, err
	}
	if cur.Status != FormStatusDraft {
		return Form{}, ErrFormNotPublishable
	}
	if strings.TrimSpace(cur.NoticeVersion) == "" || strings.TrimSpace(cur.Purpose) == "" {
		return Form{}, ErrFormIncomplete
	}
	if _, _, err := ValidateFormFieldsJSON(string(cur.Fields)); err != nil {
		return Form{}, err
	}
	ts := now()
	_, err = s.DB.Exec(
		`UPDATE forms SET status='published', published_at=?, updated_at=? WHERE id=? AND tenant_id=? AND status='draft'`,
		ts, ts, cur.ID, tenantID)
	if err != nil {
		return Form{}, err
	}
	cur.Status, cur.PublishedAt, cur.UpdatedAt = FormStatusPublished, ts, ts
	return cur, nil
}

// DisableForm stops a draft or published form. 已停用重复停用幂等。
func (s *Store) DisableForm(id, tenantID string) (Form, error) {
	cur, err := s.GetForm(id, tenantID)
	if err != nil {
		return Form{}, err
	}
	if cur.Status == FormStatusDisabled {
		return cur, nil
	}
	if cur.Status != FormStatusDraft && cur.Status != FormStatusPublished {
		return Form{}, errors.New("form: expired forms cannot transition to disabled")
	}
	ts := now()
	_, err = s.DB.Exec(
		`UPDATE forms SET status='disabled', updated_at=? WHERE id=? AND tenant_id=?`,
		ts, cur.ID, tenantID)
	if err != nil {
		return Form{}, err
	}
	cur.Status, cur.UpdatedAt = FormStatusDisabled, ts
	return cur, nil
}

// FormExpiredAt reports functional expiry: a published form whose expires_at
// has passed answers 410 without rewriting the stored status (the 'expired'
// enum value stays available for later manual marking).
func FormExpiredAt(f Form, at time.Time) bool {
	if f.ExpiresAt == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, f.ExpiresAt)
	if err != nil {
		return false
	}
	return !at.Before(exp)
}

// ---- submissions ----------------------------------------------------------------

// SubmissionPayload is the public submission body (already JSON-decoded).
type SubmissionPayload struct {
	Name             string
	Phone            string
	Wechat           string
	MarketingAllowed bool
	Source           string
	SourceRef        string
}

// ValidateSubmissionPayload enforces the fixed whitelist against ONE form's
// fields config. 逐项明确:所有违规一次性返回,绝不只报第一个。
func ValidateSubmissionPayload(fieldsJSON string, in SubmissionPayload) []FieldError {
	def, _, err := ValidateFormFieldsJSON(fieldsJSON)
	if err != nil {
		return []FieldError{{Field: "fields", Message: err.Error()}}
	}
	var details []FieldError
	add := func(field, msg string) { details = append(details, FieldError{Field: field, Message: msg}) }

	allowed := map[string]bool{}
	for _, f := range def.Fields {
		allowed[f.Name] = true
	}
	// control fields are always accepted
	allowedControl := map[string]bool{"marketing_allowed": true, "source": true, "source_ref": true}

	for _, f := range def.Fields {
		switch f.Name {
		case FormFieldName:
			v := strings.TrimSpace(in.Name)
			if v == "" {
				add(FormFieldName, "name is required")
				continue
			}
			if runeLen64(v) > FormNameMaxRunes {
				add(FormFieldName, "name must be at most 100 characters")
			}
		case FormFieldPhone:
			v := strings.TrimSpace(in.Phone)
			if v == "" {
				add(FormFieldPhone, "phone is required")
				continue
			}
			if msg := phoneProblem(v); msg != "" {
				add(FormFieldPhone, msg)
			}
		case FormFieldWechat:
			v := strings.TrimSpace(in.Wechat)
			if v == "" && f.Required {
				add(FormFieldWechat, "wechat is required")
				continue
			}
			if runeLen64(v) > FormWechatMaxRunes {
				add(FormFieldWechat, "wechat must be at most 64 characters")
			}
		}
	}
	if !allowed[FormFieldWechat] && strings.TrimSpace(in.Wechat) != "" {
		add(FormFieldWechat, "this form does not collect wechat")
	}
	if strings.TrimSpace(in.Source) == "" {
		add("source", "source is required (campaign/channel provenance)")
	} else if !validKeyToken(in.Source, FormSourceMaxRunes) {
		add("source", "source must be an ASCII identifier of at most 64 characters")
	}
	if strings.TrimSpace(in.SourceRef) == "" {
		add("source_ref", "source_ref is required (idempotency reference)")
	} else if !validKeyToken(in.SourceRef, FormSourceRefMax) {
		add("source_ref", "source_ref must be an ASCII identifier of at most 128 characters")
	}
	_ = allowedControl
	return details
}

// phoneProblem validates the CN mobile shape on the normalized form.
func phoneProblem(phone string) string {
	norm := NormalizePhone(phone)
	if norm == "" {
		return "phone is required"
	}
	if len(norm) != 11 {
		return "phone must be an 11-digit CN mobile number"
	}
	for _, r := range norm {
		if r < '0' || r > '9' {
			return "phone must be an 11-digit CN mobile number"
		}
	}
	if norm[0] != '1' || norm[1] < '3' || norm[1] > '9' {
		return "phone must start with 1 followed by 3-9"
	}
	return ""
}

// SubmitFormInput is one public submission reaching the store layer. The
// receiving tenant ALWAYS comes from the form row; no input carries it.
type SubmitFormInput struct {
	FormID string
	// Payload is the already-validated submission body (ValidateSubmissionPayload).
	Payload SubmissionPayload
	// Content is the canonical payload bytes hashed into the intake ledger.
	Content []byte
	// Pepper is the deployment HMAC pepper (intake phone fingerprint).
	Pepper string
	// ResubmitWindow is the same contact+form duplicate-suppression window.
	ResubmitWindow time.Duration
	// FilterEnabled turns on deterministic invalid-lead filtering (HUI-1686,
	// FEATURE_LEADS_FILTER); it is passed straight through to the intake seam.
	FilterEnabled bool
	// AssignEnabled turns on deterministic lead auto-assignment (HUI-1685,
	// FEATURE_LEADS_ASSIGN); passed straight through to the intake seam.
	// 表单首版不采集地域/行业,匹配维度恒为空(走全池轮询)。
	AssignEnabled bool
	// Now overrides the clock in tests; zero = time.Now.
	Now func() time.Time
}

// SubmitFormResult reports what one submission resolved to.
type SubmitFormResult struct {
	SubmissionID     string `json:"submission_id"`
	ContactID        string `json:"contact_id"`
	LeadID           string `json:"lead_id"`
	ConsentID        string `json:"consent_id"`
	Class            string `json:"class"`
	MarketingAllowed bool   `json:"marketing_allowed"`
	// Duplicate is true when an existing submission answered (idempotent key or
	// recent-window suppression); nothing new was written in that case.
	Duplicate bool `json:"duplicate"`
	// FilterReason is the machine reason code when the deterministic intake
	// filter marked the created lead filtered (HUI-1686); "" otherwise.
	FilterReason string `json:"filter_reason,omitempty"`
	// DuplicateKind: "" (fresh), "idempotent" (same form+source+source_ref),
	// "recent_window" (same contact+form within the window, different ref).
	DuplicateKind string `json:"-"`
	FormVersion   int    `json:"-"`
	NoticeVersion string `json:"-"`
}

const submissionCols = `id,form_id,form_version,tenant_id,contact_id,lead_id,consent_id,marketing_allowed,source,source_ref,payload_sha256,created_at`

// submissionTimeLayout is a fixed-width RFC3339 variant: lexicographic string
// order == chronological order (UTC only), safe for SQL string comparisons.
const submissionTimeLayout = "2006-01-02T15:04:05.000Z07:00"

func scanSubmission(sc interface{ Scan(...any) error }) (formSubmission, error) {
	var sub formSubmission
	var mk int
	err := sc.Scan(&sub.ID, &sub.FormID, &sub.FormVersion, &sub.TenantID, &sub.ContactID,
		&sub.LeadID, &sub.ConsentID, &mk, &sub.Source, &sub.SourceRef, &sub.PayloadSHA256, &sub.CreatedAt)
	if err != nil {
		return formSubmission{}, err
	}
	sub.MarketingAllowed = mk == 1
	return sub, nil
}

// formSubmission mirrors form_submissions (internal scan shape).
type formSubmission struct {
	ID, FormID               string
	FormVersion              int
	TenantID, ContactID      string
	LeadID, ConsentID        string
	MarketingAllowed         bool
	Source, SourceRef        string
	PayloadSHA256, CreatedAt string
}

// SubmitFormInTx runs the whole public submission inside the CALLER's tx:
// idempotency -> recent-window suppression -> source provenance ->
// IntakeLeadInTx (dedup classification + contact/lead) -> consent row (keyed
// by the submission id) -> form_submissions row. Everything commits together.
func SubmitFormInTx(tx *sql.Tx, in SubmitFormInput) (SubmitFormResult, error) {
	nowFn := in.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	// 1. the form owns everything: tenant, version, notice, purpose, state.
	f, err := scanForm(tx.QueryRow(`SELECT `+formCols+` FROM forms WHERE id=?`, in.FormID))
	if errors.Is(err, sql.ErrNoRows) {
		return SubmitFormResult{}, ErrFormNotFound
	}
	if err != nil {
		return SubmitFormResult{}, err
	}
	if f.Status != FormStatusPublished {
		if f.Status == FormStatusDisabled {
			return SubmitFormResult{}, ErrFormDisabled
		}
		// drafts are never publicly visible: same answer as a missing form
		return SubmitFormResult{}, ErrFormNotFound
	}
	if FormExpiredAt(f, nowFn()) {
		return SubmitFormResult{}, ErrFormExpired
	}

	tenantID := f.TenantID // 接收租户恒为表单归属;无任何请求侧租户输入。

	// 2. idempotency: same (tenant, form, source, source_ref) -> original row.
	existing, err := lookupSubmissionByKey(tx, tenantID, f.ID, in.Payload.Source, in.Payload.SourceRef)
	if err == nil {
		return submissionToResult(existing, IntakeClassExactDuplicate, "idempotent"), nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SubmitFormResult{}, err
	}

	// 3. recent-window suppression: same contact+form within the window answers
	// the original submission without creating another lead. Only a unique live
	// phone match triggers it; ambiguity is left to the intake classification.
	norm := NormalizePhone(in.Payload.Phone)
	if norm != "" {
		candidates, err := tenantContactsByPhone(tx, tenantID, norm)
		if err != nil {
			return SubmitFormResult{}, err
		}
		if len(candidates) == 1 {
			// fixed-width UTC timestamps so string comparison is chronological;
			// strict > keeps a zero-width window from suppressing on equal clock
			// ticks (a genuine outside-window re-consult must proceed).
			windowStart := nowFn().Add(-in.ResubmitWindow).UTC().Format(submissionTimeLayout)
			row := tx.QueryRow(
				`SELECT `+submissionCols+` FROM form_submissions
				 WHERE tenant_id=? AND form_id=? AND contact_id=? AND created_at>?
				 ORDER BY created_at DESC LIMIT 1`,
				tenantID, f.ID, candidates[0].ID, windowStart)
			recent, err := scanSubmission(row)
			if err == nil {
				return submissionToResult(recent, IntakeClassExactDuplicate, "recent_window"), nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return SubmitFormResult{}, err
			}
		}
	}

	// 4. campaign provenance: (source, source_ref) resolves to a source_refs row
	// inside this tenant (idempotent reuse; created once, never duplicated).
	sourceRefID, err := resolveSourceRefTx(tx, tenantID, in.Payload.Source, in.Payload.SourceRef)
	if err != nil {
		return SubmitFormResult{}, err
	}

	// 5. intake classification: the single dedup seam (HUI-1683). 咨询次数不丢:
	// repeat_consult 新建 lead 关联既有 contact;new 建全新 contact+lead。
	submissionID := newID("fsub_")
	canonical := canonicalPayload(in.Payload)
	res, err := IntakeLeadInTx(tx, IntakeInput{
		TenantID:         tenantID,
		SourceApp:        "public_form",
		SourceNS:         f.FormKey,
		EventID:          in.Payload.SourceRef,
		Content:          canonical,
		ContactName:      strings.TrimSpace(in.Payload.Name),
		Phone:            strings.TrimSpace(in.Payload.Phone),
		Email:            "",
		BusinessCategory: "merchant_customer",
		SourceType:       "form",
		SourceRefID:      sourceRefID,
		Consent:          nil, // consent is written below under the submission id key
		Pepper:           in.Pepper,
		FilterEnabled:    in.FilterEnabled,
		AssignEnabled:    in.AssignEnabled,
	})
	if err != nil {
		return SubmitFormResult{}, err
	}

	// 6. consent row keyed by THIS submission (新提交=新键): notice_version 与
	// purpose 从表单带出;marketing_allowed 是独立勾选值,缺省 false ——
	// 没有同意不进营销池(仅建档)。
	consent, _, err := upsertConsentTx(tx, tenantID, res.ContactID, ConsentUpsert{
		SourceSubmissionRef: submissionID,
		SourceChannel:       "public_form",
		NoticeVersion:       f.NoticeVersion,
		Purpose:             consentPurpose(f.Purpose),
		MarketingAllowed:    in.Payload.MarketingAllowed,
	})
	if err != nil {
		return SubmitFormResult{}, err
	}

	// 7. the submission ledger row (payload 原文不入库,只留 sha256)。
	sum := sha256.Sum256(canonical)
	createdAt := nowFn().UTC().Format(submissionTimeLayout)
	_, err = tx.Exec(
		`INSERT INTO form_submissions(`+submissionCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		submissionID, f.ID, f.Version, tenantID, res.ContactID, res.LeadID, consent.ID,
		boolInt(in.Payload.MarketingAllowed), in.Payload.Source, in.Payload.SourceRef,
		hex.EncodeToString(sum[:]), createdAt)
	if err != nil {
		// 并发同键:UNIQUE 冲突 → 重读幂等返回原 submission,绝不造第二行。
		if isUniqueViolation(err) {
			existing, lerr := lookupSubmissionByKey(tx, tenantID, f.ID, in.Payload.Source, in.Payload.SourceRef)
			if lerr == nil {
				return submissionToResult(existing, IntakeClassExactDuplicate, "idempotent"), nil
			}
		}
		return SubmitFormResult{}, err
	}

	return SubmitFormResult{
		SubmissionID:     submissionID,
		ContactID:        res.ContactID,
		LeadID:           res.LeadID,
		ConsentID:        consent.ID,
		Class:            res.Class,
		FilterReason:     res.FilterReason,
		MarketingAllowed: in.Payload.MarketingAllowed,
		FormVersion:      f.Version,
		NoticeVersion:    f.NoticeVersion,
	}, nil
}

// consentPurpose maps the form purpose to the consent ledger's purpose value.
func consentPurpose(p string) string {
	if v := strings.TrimSpace(p); v != "" {
		return v
	}
	return "marketing"
}

// canonicalPayload produces stable bytes for hashing (fixed field order).
func canonicalPayload(in SubmissionPayload) []byte {
	b, _ := json.Marshal(struct {
		Name             string `json:"name"`
		Phone            string `json:"phone"`
		Wechat           string `json:"wechat,omitempty"`
		MarketingAllowed bool   `json:"marketing_allowed"`
		Source           string `json:"source"`
		SourceRef        string `json:"source_ref"`
	}{
		Name:             strings.TrimSpace(in.Name),
		Phone:            NormalizePhone(in.Phone),
		Wechat:           strings.TrimSpace(in.Wechat),
		MarketingAllowed: in.MarketingAllowed,
	})
	return b
}

func lookupSubmissionByKey(tx *sql.Tx, tenantID, formID, source, sourceRef string) (formSubmission, error) {
	return scanSubmission(tx.QueryRow(
		`SELECT `+submissionCols+` FROM form_submissions
		 WHERE tenant_id=? AND form_id=? AND source=? AND source_ref=?`,
		tenantID, formID, source, sourceRef))
}

func resolveSourceRefTx(tx *sql.Tx, tenantID, sourceApp, sourceRef string) (string, error) {
	var id string
	err := tx.QueryRow(
		`SELECT id FROM source_refs WHERE tenant_id=? AND source_app=? AND source_ref=?
		 ORDER BY created_at LIMIT 1`, tenantID, sourceApp, sourceRef).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID("src_")
	_, err = tx.Exec(
		`INSERT INTO source_refs(id,tenant_id,source_app,source_ref,auth_scope_snapshot,created_by,created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		id, tenantID, sourceApp, sourceRef, "", "public_form", now())
	if err != nil {
		// concurrent insert: reuse whichever row won
		return resolveSourceRefTx(tx, tenantID, sourceApp, sourceRef)
	}
	return id, nil
}

func submissionToResult(sub formSubmission, class, kind string) SubmitFormResult {
	return SubmitFormResult{
		SubmissionID:     sub.ID,
		ContactID:        sub.ContactID,
		LeadID:           sub.LeadID,
		ConsentID:        sub.ConsentID,
		Class:            class,
		MarketingAllowed: sub.MarketingAllowed,
		Duplicate:        true,
		DuplicateKind:    kind,
		FormVersion:      sub.FormVersion,
	}
}
