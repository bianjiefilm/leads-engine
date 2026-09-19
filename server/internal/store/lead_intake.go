// HUI-1683 / FEAT-0184 线索 intake 去重领域函数。
//
// 这是 HUI-1680(inbox)与本服务之间的唯一接缝:渠道侧把事件与线索写入放进
// 同一个事务,调用 IntakeLeadInTx 完成去重三分类,不在渠道各写一套规则。
//
// 三分类(去重判定结果,记入 lead_intake_events.class):
//   - exact_duplicate 重复事件:同幂等键同内容 —— 幂等吞,返回原 lead/contact
//     引用,零写入(账本行只记首投判定,重放不改写);
//   - repeat_consult 同一联系人再次咨询:新建 lead 关联既有 contact,
//     新 lead 保留自己的来源/活动/授权记录,真实咨询次数不丢;
//   - ambiguous 疑似同人:本租户内多命中同一手机号 —— 新建独立 contact+lead
//     (不丢真实咨询),写 merge_candidates 候选,绝不静默合并;
//   - new 零命中的普通新线索(非去重结果的记账值)。
//
// 同键不同内容(content_sha256)必须显式冲突(ErrEventContentConflict → HTTP
// 409),绝不静默覆盖。查重空间严格限定在接收租户内:跨租户同号码互不可见、
// 不合并、不绑定任何平台账号。
//
// 手机号纪律:事件账本只存 HMAC-SHA256(pepper, 归一化手机号) 指纹(phone_fpr),
// 明文手机号不入 intake 索引;日志侧由 HTTP 层只记指纹前缀。
package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrEventContentConflict is returned when the same idempotency key arrives
// with a different content_sha256. HTTP 层必须显式 409,绝不静默覆盖。
var ErrEventContentConflict = errors.New("lead intake: same event key with different content")

// Intake class values (also the CHECK domain of lead_intake_events.class).
const (
	IntakeClassNew            = "new"
	IntakeClassExactDuplicate = "exact_duplicate"
	IntakeClassRepeatConsult  = "repeat_consult"
	IntakeClassAmbiguous      = "ambiguous"
)

// IntakeInput is one arriving lead event. Content is the raw payload bytes as
// received; its SHA-256 is the content identity of the idempotency key.
type IntakeInput struct {
	TenantID string
	// Idempotency key: (tenant, source_app, source_ns, event_id).
	SourceApp string
	SourceNS  string
	EventID   string
	// Content is hashed (never stored verbatim) into content_sha256.
	Content []byte

	// Contact facts from the event. Phone is optional; without it the event
	// can only ever classify as new (no dedup signal, and none is invented).
	ContactName      string
	Phone            string
	Email            string
	BusinessCategory string
	// SourceType feeds contacts.source_type (manual|form|touch_campaign);
	// empty defaults to "form".
	SourceType string
	// SourceRefID is an optional provenance row (source_refs) for campaign
	// attribution; it must belong to the same tenant (verified).
	SourceRefID string
	// Consent, when non-nil, is recorded on the resolved contact inside the
	// SAME transaction, under the ordinary consent rules (replay on a revoked
	// key stays replay_unchanged; revocation is never cleared).
	Consent *ConsentUpsert
	// Pepper is the deployment-injected HMAC pepper for the phone fingerprint.
	Pepper string
}

// IntakeResult reports the dedup classification and the lead/contact the event
// resolved to. For exact_duplicate these are the ORIGINAL references.
type IntakeResult struct {
	Class     string `json:"class"`
	LeadID    string `json:"lead_id"`
	ContactID string `json:"contact_id"`
	// Duplicate is true iff the event key already existed (idempotent replay).
	Duplicate bool `json:"duplicate"`
}

// intakeEventCols mirrors lead_intake_events (HUI-1683 migration).
const intakeEventCols = `id,tenant_id,source_app,source_ns,event_id,content_sha256,phone_fpr,class,lead_id,contact_id,created_at`

// NormalizePhone canonicalizes a phone for dedup comparison: trims space,
// drops separators, and strips a +86/86 country prefix from an 11-digit CN
// mobile. It never invents digits; an empty result means "no usable phone".
func NormalizePhone(phone string) string {
	p := strings.TrimSpace(phone)
	if p == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range p {
		switch r {
		case ' ', '-', '(', ')', '.', '　', '－', '（', '）':
			continue
		}
		b.WriteRune(r)
	}
	p = b.String()
	p = strings.TrimPrefix(p, "+")
	if len(p) == 13 && strings.HasPrefix(p, "86") {
		p = p[2:]
	}
	return p
}

// PhoneFingerprint returns hex(HMAC-SHA256(pepper, normalized phone)), or ""
// when the phone is empty. 明文手机号绝不进入 intake 索引,只进指纹。
func PhoneFingerprint(pepper, phone string) string {
	norm := NormalizePhone(phone)
	if norm == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(norm))
	return hex.EncodeToString(mac.Sum(nil))
}

// validKeyToken reports whether an idempotency-key component is URL/log-safe:
// conservative ASCII identifier charset only.
func validKeyToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':', r == '@':
		default:
			return false
		}
	}
	return true
}

// IntakeLeadInTx runs the full intake classification inside the CALLER's
// transaction (HUI-1680 writes its inbox row on the same tx). It commits
// nothing: the caller owns Begin/Commit, so event + lead + contact + consent +
// candidate rows are atomic with whatever else the channel writes.
//
// Concurrency: a concurrent insert on the same key surfaces as a UNIQUE
// violation on this tx's INSERT; the function re-reads the key and answers
// idempotently (same sha → original reference; different sha → conflict).
func IntakeLeadInTx(tx *sql.Tx, in IntakeInput) (IntakeResult, error) {
	if in.TenantID == "" {
		return IntakeResult{}, errors.New("intake: tenant required")
	}
	if !validKeyToken(in.SourceApp, 64) || !validKeyToken(in.SourceNS, 64) || !validKeyToken(in.EventID, 128) {
		return IntakeResult{}, errors.New("intake: source_app/source_ns/event_id must be non-empty ASCII identifiers (caps 64/64/128)")
	}
	if in.BusinessCategory == "" {
		in.BusinessCategory = "merchant_customer"
	}
	if in.BusinessCategory != "merchant_customer" && in.BusinessCategory != "creative_service" {
		return IntakeResult{}, errors.New("intake: business_category must be merchant_customer or creative_service")
	}
	if in.SourceType == "" {
		in.SourceType = "form"
	}
	if in.SourceType != "manual" && in.SourceType != "form" && in.SourceType != "touch_campaign" {
		return IntakeResult{}, errors.New("intake: source_type must be manual, form or touch_campaign")
	}
	if strings.TrimSpace(in.ContactName) == "" {
		return IntakeResult{}, errors.New("intake: contact name is required")
	}
	if runeLen64(in.ContactName) > 100 || runeLen64(in.Email) > 200 {
		return IntakeResult{}, errors.New("intake: name/email exceed length caps (100/200)")
	}
	if norm := NormalizePhone(in.Phone); norm != "" && runeLen64(norm) > 32 {
		return IntakeResult{}, errors.New("intake: phone too long")
	}
	if in.SourceRefID != "" {
		if _, err := sGetSourceRef(tx, in.TenantID, in.SourceRefID); err != nil {
			return IntakeResult{}, fmt.Errorf("intake: source ref %s: %w", in.SourceRefID, err)
		}
	}

	sum := sha256.Sum256(in.Content)
	contentSHA := hex.EncodeToString(sum[:])

	// ---- 1. idempotency: same key must never create twice ----------------
	res, err := intakeLookupByKey(tx, in, contentSHA)
	if err == nil {
		return res, nil
	}
	if !errors.Is(err, errIntakeKeyUnknown) {
		return IntakeResult{}, err
	}

	// ---- 2. classify by phone within THIS tenant --------------------------
	fpr := PhoneFingerprint(in.Pepper, in.Phone)
	norm := NormalizePhone(in.Phone)
	candidates, err := tenantContactsByPhone(tx, in.TenantID, norm)
	if err != nil {
		return IntakeResult{}, err
	}

	class := IntakeClassNew
	var contactID string
	switch {
	case len(candidates) == 1:
		contactID = candidates[0].ID
		class = IntakeClassRepeatConsult
	case len(candidates) > 1:
		// 疑似同人:新建独立 contact+lead,写候选池,绝不静默合并。
		class = IntakeClassAmbiguous
	default:
		class = IntakeClassNew
	}
	if contactID == "" {
		nc, err := intakeNewContact(tx, in)
		if err != nil {
			return IntakeResult{}, err
		}
		contactID = nc.ID
		if len(candidates) > 1 {
			// pair the new contact with every hit, and the hits pairwise.
			ids := append([]string{nc.ID}, candidateIDs(candidates)...)
			for i := 0; i < len(ids); i++ {
				for j := i + 1; j < len(ids); j++ {
					if err := upsertMergeCandidateTx(tx, in.TenantID, ids[i], ids[j], "shared_phone"); err != nil {
						return IntakeResult{}, err
					}
				}
			}
		}
	}

	lead := Lead{
		TenantID:    in.TenantID,
		ContactID:   contactID,
		SourceRefID: in.SourceRefID,
		Status:      "new",
	}
	lead.ID = newID("lead_")
	lead.CreatedBy = "intake:" + in.SourceApp
	lead.CreatedAt, lead.UpdatedAt = now(), now()
	if err := insertLeadTx(tx, lead); err != nil {
		return IntakeResult{}, err
	}

	evID := newID("iev_")
	_, err = tx.Exec(
		`INSERT INTO lead_intake_events(`+intakeEventCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		evID, in.TenantID, in.SourceApp, in.SourceNS, in.EventID, contentSHA, fpr, class,
		lead.ID, contactID, lead.CreatedAt)
	if err != nil {
		// 同键并发插入:UNIQUE 冲突 → 重读返回既有(幂等),绝不重复建线索。
		// 若键仍不可见(并发事务未提交),返回原始冲突错误 —— 不造出第二行。
		if isUniqueViolation(err) {
			res, lerr := intakeLookupByKey(tx, in, contentSHA)
			if lerr == nil {
				return res, nil
			}
			return IntakeResult{}, err
		}
		return IntakeResult{}, err
	}

	// ---- 3. the event's own consent record (same tx, ordinary rules) ------
	if in.Consent != nil {
		if _, _, err := upsertConsentTx(tx, in.TenantID, contactID, *in.Consent); err != nil {
			return IntakeResult{}, err
		}
	}

	return IntakeResult{Class: class, LeadID: lead.ID, ContactID: contactID}, nil
}

// errIntakeKeyUnknown marks "idempotency key not seen before".
var errIntakeKeyUnknown = errors.New("intake: event key unknown")

// intakeLookupByKey answers an existing event: same content → original
// reference + exact_duplicate; different content → explicit conflict.
func intakeLookupByKey(tx *sql.Tx, in IntakeInput, contentSHA string) (IntakeResult, error) {
	var evSHA, leadID, contactID string
	err := tx.QueryRow(
		`SELECT content_sha256,lead_id,contact_id FROM lead_intake_events
		 WHERE tenant_id=? AND source_app=? AND source_ns=? AND event_id=?`,
		in.TenantID, in.SourceApp, in.SourceNS, in.EventID).Scan(&evSHA, &leadID, &contactID)
	if errors.Is(err, sql.ErrNoRows) {
		return IntakeResult{}, errIntakeKeyUnknown
	}
	if err != nil {
		return IntakeResult{}, err
	}
	if evSHA != contentSHA {
		// 同键异内容:显式冲突,不静默覆盖。
		return IntakeResult{}, ErrEventContentConflict
	}
	return IntakeResult{Class: IntakeClassExactDuplicate, LeadID: leadID, ContactID: contactID, Duplicate: true}, nil
}

// tenantContactsByPhone returns the LIVE contacts of the tenant whose phone
// normalizes to norm (empty norm → no candidates, no signal is invented).
// Stored phones keep the operator's original formatting, so the comparison
// happens on the normalized form in Go — this query is tenant-scoped and the
// table is tenant-local by construction (single-writer sqlite, SMB scale).
func tenantContactsByPhone(tx *sql.Tx, tenantID, norm string) ([]Contact, error) {
	if norm == "" {
		return nil, nil
	}
	rows, err := tx.Query(
		`SELECT `+contactCols+` FROM contacts WHERE tenant_id=? AND deleted_at IS NULL`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		if NormalizePhone(c.Phone) == norm {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// intakeNewContact creates the intake-side contact row (assigned to nobody:
// assignment is a human decision, made later through the ordinary API).
func intakeNewContact(tx *sql.Tx, in IntakeInput) (Contact, error) {
	c := Contact{
		TenantID:         in.TenantID,
		Name:             strings.TrimSpace(in.ContactName),
		Phone:            strings.TrimSpace(in.Phone),
		Email:            strings.TrimSpace(in.Email),
		BusinessCategory: in.BusinessCategory,
		SourceType:       in.SourceType,
		ConsentStatus:    "pending", // per-source consent rows are the real record
	}
	c.ID = newID("con_")
	c.CreatedBy = "intake:" + in.SourceApp
	c.CreatedAt, c.UpdatedAt = now(), now()
	if err := insertContactTx(tx, c); err != nil {
		return Contact{}, err
	}
	return c, nil
}

func candidateIDs(cs []Contact) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: lead_intake_events")
}

// sGetSourceRef verifies a source_refs row within the tenant (tx-scoped).
func sGetSourceRef(tx *sql.Tx, tenantID, id string) (SourceRef, error) {
	var r SourceRef
	err := tx.QueryRow(
		`SELECT id,tenant_id,source_app,source_ref,auth_scope_snapshot,created_by,created_at
		 FROM source_refs WHERE id=? AND tenant_id=?`, id, tenantID).
		Scan(&r.ID, &r.TenantID, &r.SourceApp, &r.SourceRef, &r.AuthScopeSnapshot, &r.CreatedBy, &r.CreatedAt)
	return r, err
}

// runeLen64 counts runes for the intake caps (local helper to keep this file
// free of httpapi dependencies).
func runeLen64(s string) int { return len([]rune(s)) }
