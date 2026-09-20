// HUI-1683 / FEAT-0184 线索 intake 去重与联系人合并的 HTTP 面:
//   - POST /leads/intake:事件幂等键 + 三分类(去重规则唯一实现,HUI-1680 的
//     inbox 与本端点共用 store.IntakeLeadInTx,不在渠道各写一套);
//   - POST /contacts/{id}/merge/{otherId}:owner 专属显式合并(字段级 provenance、
//     consent 取最严格、per-source 行重指不归并);
//   - POST /merges/{id}/undo:仅未发生后续写时纠错,undo 本身写入审计;
//   - GET/POST merge-candidates 与 GET /dedup/stats:候选池与重复统计(owner);
//
// 日志红线:手机号明文绝不入日志 —— intake 只记 phone 指纹前缀;merge/undo 只记
// id 与计数。所有查询面不收 phone 参数。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const intakeMaxBodyBytes = 1 << 16 // 64 KiB: 单条线索事件的硬上限

// assignTagCapRunes caps the intake-side region/industry matching dimensions
// (HUI-1685); the pool config carries the same cap server-side.
const assignTagCapRunes = 64

// handleLeadIntake is THE dedup seam for every intake channel (HUI-1680 calls
// store.IntakeLeadInTx on its inbox tx; this endpoint is the HTTP form).
func (s *Server) handleLeadIntake(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	if strings.TrimSpace(s.Cfg.DedupPepper) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config_gate_dedup",
			"message": "LEADS_DEDUP_PEPPER is required for lead intake (fail-closed)",
			"detail":  []string{"LEADS_DEDUP_PEPPER is required (HMAC pepper for the intake phone fingerprint)"},
		})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, intakeMaxBodyBytes))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable (max 64 KiB)")
		return
	}
	var in struct {
		SourceApp        string              `json:"source_app"`
		SourceNS         string              `json:"source_ns"`
		EventID          string              `json:"event_id"`
		Contact          contactFacts        `json:"contact"`
		BusinessCategory string              `json:"business_category"`
		SourceType       string              `json:"source_type"`
		Source           *sourceInput        `json:"source"`
		Consent          *intakeConsentInput `json:"consent"`
		// Region/Industry feed the auto-assignment pool matching (HUI-1685).
		// Only validated/forwarded when FEATURE_LEADS_ASSIGN=on; with the flag
		// off they are ignored exactly like any other unknown field was before
		// (byte-identical legacy behavior).
		Region   string `json:"region"`
		Industry string `json:"industry"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if in.BusinessCategory == "" {
		in.BusinessCategory = "merchant_customer"
	}
	if !validCategories[in.BusinessCategory] {
		fail(w, http.StatusBadRequest, "bad_request", "business_category must be merchant_customer or creative_service")
		return
	}
	if in.SourceType == "" {
		in.SourceType = "form"
	}
	if !validSourceTypes[in.SourceType] {
		fail(w, http.StatusBadRequest, "bad_request", "source_type must be manual, form or touch_campaign")
		return
	}
	if strings.TrimSpace(in.Contact.Name) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "contact.name is required")
		return
	}
	var consent *store.ConsentUpsert
	if in.Consent != nil {
		if strings.TrimSpace(in.Consent.SourceSubmissionRef) == "" {
			fail(w, http.StatusBadRequest, "bad_request",
				"consent.source_submission_ref is required (consent is kept per source submission/channel)")
			return
		}
		if !capped(in.Consent.SourceSubmissionRef, 128) || !capped(in.Consent.SourceChannel, 64) ||
			!capped(in.Consent.NoticeVersion, 64) {
			fail(w, http.StatusBadRequest, "bad_request", "consent fields exceed their length caps (128/64/64)")
			return
		}
		consent = &store.ConsentUpsert{
			SourceSubmissionRef: in.Consent.SourceSubmissionRef,
			SourceChannel:       in.Consent.SourceChannel,
			NoticeVersion:       in.Consent.NoticeVersion,
			Purpose:             "marketing",
			MarketingAllowed:    in.Consent.MarketingAllowed,
		}
	}
	// campaign/活动 provenance is optional; when present it must be complete.
	var sourceRefID string
	if in.Source != nil && (in.Source.SourceApp != "" || in.Source.SourceRef != "" || in.Source.AuthScopeSnapshot != "") {
		if in.Source.SourceApp == "" || in.Source.SourceRef == "" {
			fail(w, http.StatusBadRequest, "bad_source", "source_app and source_ref are both required")
			return
		}
		// 幂等复用:重复投递不得累积重复的活动 provenance 行。
		if existing, ok := s.St.FindSourceRef(c.Member.TenantID, in.Source.SourceApp, in.Source.SourceRef); ok {
			sourceRefID = existing.ID
		} else {
			sr, err := s.St.CreateSourceRef(c.Member.TenantID, in.Source.SourceApp, in.Source.SourceRef,
				in.Source.AuthScopeSnapshot, c.Member.ID)
			if err != nil {
				fail(w, http.StatusInternalServerError, "internal", "source ref create failed")
				return
			}
			sourceRefID = sr.ID
		}
	}

	// 分配匹配维度(HUI-1685):仅开关开时校验并转发;off 时零参与(off =
	// 与既往逐字节一致 —— 开关关时这两个字段与任何未知字段一样被忽略)。
	region, industry := "", ""
	if s.Cfg.FeatureLeadsAssign {
		region, industry = strings.TrimSpace(in.Region), strings.TrimSpace(in.Industry)
		if runeLen(region) > assignTagCapRunes || runeLen(industry) > assignTagCapRunes {
			fail(w, http.StatusBadRequest, "bad_request", "region/industry must be at most 64 characters")
			return
		}
	}

	tx, err := s.St.DB.Begin()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "intake begin failed")
		return
	}
	defer tx.Rollback()
	res, err := store.IntakeLeadInTx(tx, store.IntakeInput{
		TenantID:         c.Member.TenantID,
		SourceApp:        in.SourceApp,
		SourceNS:         in.SourceNS,
		EventID:          in.EventID,
		Content:          body,
		ContactName:      in.Contact.Name,
		Phone:            in.Contact.Phone,
		Email:            in.Contact.Email,
		BusinessCategory: in.BusinessCategory,
		SourceType:       in.SourceType,
		SourceRefID:      sourceRefID,
		Consent:          consent,
		Pepper:           s.Cfg.DedupPepper,
		FilterEnabled:    s.Cfg.FeatureLeadsFilter,
		AssignEnabled:    s.Cfg.FeatureLeadsAssign,
		Region:           region,
		Industry:         industry,
	})
	if errors.Is(err, store.ErrEventContentConflict) {
		// 同键不同内容:显式冲突,绝不静默覆盖。
		fail(w, http.StatusConflict, "event_content_conflict",
			"the same event key was delivered with different content; resolve manually instead of overwriting")
		return
	}
	if err != nil {
		if msg, ok := intakeValidationError(err); ok {
			fail(w, http.StatusBadRequest, "bad_request", msg)
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "intake failed")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "intake commit failed")
		return
	}
	// 日志只记分类/幂等键/引用与指纹前缀;手机号明文零出现。过滤开着时追加
	// 机器原因码(HUI-1686),off 时日志格式与既往逐字一致。自动分配开着且本投
	// 命中时追加 assigned=<member id>;空后缀不改变既往字节。
	assignSuffix := ""
	if res.AssignedTo != "" {
		assignSuffix = " assigned=" + res.AssignedTo
	}
	if res.FilterReason != "" {
		s.Log.Printf("lead intake tenant=%s event=%s/%s/%s class=%s lead=%s contact=%s phone_fpr=%s dup=%t filter=%s%s",
			c.Member.TenantID, in.SourceApp, in.SourceNS, in.EventID, res.Class,
			res.LeadID, res.ContactID, shortFPR(s.Cfg.DedupPepper, in.Contact.Phone), res.Duplicate, res.FilterReason, assignSuffix)
	} else {
		s.Log.Printf("lead intake tenant=%s event=%s/%s/%s class=%s lead=%s contact=%s phone_fpr=%s dup=%t%s",
			c.Member.TenantID, in.SourceApp, in.SourceNS, in.EventID, res.Class,
			res.LeadID, res.ContactID, shortFPR(s.Cfg.DedupPepper, in.Contact.Phone), res.Duplicate, assignSuffix)
	}
	status := http.StatusCreated
	if res.Duplicate {
		status = http.StatusOK
	}
	out := map[string]any{
		"class":      res.Class,
		"lead_id":    res.LeadID,
		"contact_id": res.ContactID,
		"duplicate":  res.Duplicate,
	}
	// filter_reason 只在首投判定命中时出现(机器码,零联系方式原文);
	// off 模式下响应键与既往完全一致。assigned_member_id 同理:仅自动分配
	// 开且本投真的指派了成员时出现(重放/池空都不出现该键)。
	if res.FilterReason != "" {
		out["filter_reason"] = res.FilterReason
	}
	if res.AssignedTo != "" {
		out["assigned_member_id"] = res.AssignedTo
	}
	writeJSON(w, status, out)
}

type contactFacts struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Email string `json:"email"`
}

type intakeConsentInput struct {
	SourceSubmissionRef string `json:"source_submission_ref"`
	SourceChannel       string `json:"source_channel"`
	NoticeVersion       string `json:"notice_version"`
	MarketingAllowed    bool   `json:"marketing_allowed"`
}

// intakeValidationError recognizes the domain function's own validation
// errors ("intake: …"); anything else is an internal failure and must not
// leak its text to the client.
func intakeValidationError(err error) (string, bool) {
	msg := err.Error()
	const prefix = "intake: "
	if strings.HasPrefix(msg, prefix) {
		return msg[len(prefix):], true
	}
	return "", false
}

// shortFPR renders a log-safe correlation prefix of the phone fingerprint.
func shortFPR(pepper, phone string) string {
	fpr := store.PhoneFingerprint(pepper, phone)
	if fpr == "" {
		return "none"
	}
	return fpr[:16]
}

// ---- merge (owner only) ------------------------------------------------------

// handleContactMerge merges otherId INTO id within the caller's tenant.
// Owner-only; cross-category merges are refused; the source profile becomes a
// masked tombstone and the full audit record (field provenance + snapshots +
// repoint counts) is returned.
func (s *Server) handleContactMerge(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionMerge, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	targetID, sourceID := r.PathValue("id"), r.PathValue("otherId")
	m, err := s.St.MergeContacts(c.Member.TenantID, targetID, sourceID, c.Member.ID)
	switch {
	case errors.Is(err, store.ErrMergeSameContact):
		fail(w, http.StatusBadRequest, "bad_request", "cannot merge a contact into itself")
		return
	case errors.Is(err, store.ErrMergeCategoryMismatch):
		fail(w, http.StatusConflict, "category_mismatch",
			"merchant_customer and creative_service contacts are never merged")
		return
	case errors.Is(err, store.ErrMergeContactNotFound):
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "contact merge failed")
		return
	}
	s.Log.Printf("contacts merged tenant=%s target=%s source=%s leads=%d opps=%d consents=%d followups=%d by=%s",
		c.Member.TenantID, m.TargetContact, m.SourceContact,
		m.LeadRepointCount, m.OppRepointCount, m.ConsentRepointCount, m.FollowupRepointCount, c.Member.ID)
	writeJSON(w, http.StatusOK, m)
}

// handleMergeUndo reverts one merge (owner-only). Refusals are explicit 409s —
// never a silent partial undo.
func (s *Server) handleMergeUndo(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionMerge, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	m, err := s.St.UndoContactMerge(c.Member.TenantID, r.PathValue("id"), c.Member.ID)
	switch {
	case errors.Is(err, store.ErrMergeNotFound):
		fail(w, http.StatusNotFound, "not_found", "merge not found")
		return
	case errors.Is(err, store.ErrMergeAlreadyUndone):
		fail(w, http.StatusConflict, "merge_already_undone", "this merge has already been undone")
		return
	case errors.Is(err, store.ErrMergeHasSubsequentWrites):
		fail(w, http.StatusConflict, "merge_has_subsequent_writes",
			"records were written after this merge; undo would lose them — resolve manually")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "merge undo failed")
		return
	}
	s.Log.Printf("merge undone tenant=%s merge=%s target=%s source=%s by=%s",
		c.Member.TenantID, m.ID, m.TargetContact, m.SourceContact, c.Member.ID)
	writeJSON(w, http.StatusOK, m)
}

// handleMergeList answers the merge audit trail (owner-only).
func (s *Server) handleMergeList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionMerge, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	items, err := s.St.ListContactMerges(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "merge list failed")
		return
	}
	if items == nil {
		items = []store.ContactMerge{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- candidate pool + stats (owner only) --------------------------------------

func (s *Server) handleMergeCandidateList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionMerge, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "merged" && status != "dismissed" {
		fail(w, http.StatusBadRequest, "bad_request", "status must be pending, merged or dismissed")
		return
	}
	items, err := s.St.ListMergeCandidates(c.Member.TenantID, status)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "candidate list failed")
		return
	}
	if items == nil {
		items = []store.MergeCandidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleMergeCandidateDismiss(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionMerge, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	cand, err := s.St.DismissMergeCandidate(c.Member.TenantID, r.PathValue("id"), c.Member.ID)
	if errors.Is(err, store.ErrCandidateNotFound) {
		fail(w, http.StatusNotFound, "not_found", "pending candidate not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "candidate dismiss failed")
		return
	}
	s.Log.Printf("merge candidate dismissed tenant=%s candidate=%s pair=%s/%s by=%s",
		c.Member.TenantID, cand.ID, cand.ContactA, cand.ContactB, c.Member.ID)
	writeJSON(w, http.StatusOK, cand)
}

func (s *Server) handleDedupStats(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionMerge, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	st, err := s.St.DedupStats(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "dedup stats failed")
		return
	}
	writeJSON(w, http.StatusOK, st)
}
