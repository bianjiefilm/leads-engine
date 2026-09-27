// HUI-1680 Notify 接收端。
//
// 只接受部署登记过的来源。验签沿用 platform-notify 的
// X-Notify-Signature: sha256=<hex(hmac(secret, body))>。
// 联系方式不在事件里：按 payload_ref 向登记过的源拉取最小 submission，
// 校验 sha256 后与 inbox 行、线索、consent 同一事务落库。
// 落库前的失败一律不返回成功回执。浏览/企微点击不建联系人。
// 不触发外呼、短信、SOP，也不创建派诺订单。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/notifyingest"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const notifyIngestMaxBody = 64 << 10

func (s *Server) handleNotifyIngest(w http.ResponseWriter, r *http.Request) {
	if problems := s.Cfg.IngestGate(); len(problems) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "config_gate_ingest", "message": "notify ingest is not configured (fail-closed)", "detail": problems,
		})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, notifyIngestMaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable (max 64 KiB)")
		return
	}
	src, ok := matchIngestSignature(s.Cfg.IngestSources, r.Header.Get("X-Notify-Signature"), body)
	if !ok {
		fail(w, http.StatusUnauthorized, "bad_signature", "signature does not match a registered source")
		return
	}
	delivery, err := notifyingest.ParseDelivery(body)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_event", "directed event rejected")
		return
	}
	if delivery.Profile.SourceApp != src.AppID {
		fail(w, http.StatusUnauthorized, "bad_signature", "signature source does not match event_profile")
		return
	}
	if delivery.Profile.TargetApp != s.Cfg.AppID {
		fail(w, http.StatusForbidden, "wrong_target", "event target_app is not this application")
		return
	}
	now := time.Now().UTC()
	if delivery.Profile.ExpiresAt != nil && now.After(delivery.Profile.ExpiresAt.UTC()) {
		fail(w, http.StatusBadRequest, "event_expired", "directed event is expired")
		return
	}
	if !delivery.OccurredAt.IsZero() && delivery.OccurredAt.After(now.Add(10*time.Minute)) {
		fail(w, http.StatusBadRequest, "event_skew", "occurred_at is too far in the future")
		return
	}
	if _, err := s.St.GetTenant(delivery.TenantID); err != nil {
		fail(w, http.StatusNotFound, "unknown_tenant", "target tenant is not provisioned")
		return
	}
	bodySHA := notifyingest.BodySHA256(body)
	if existing, err := s.St.GetNotifyInboxByEvent(delivery.TenantID, delivery.Profile.SourceApp, delivery.Profile.EventID); err == nil {
		if existing.BodySHA256 != bodySHA {
			fail(w, http.StatusConflict, "event_content_conflict", "the same event key was delivered with different content")
			return
		}
		writeReceipt(w, existing.ReceiptJSON)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "inbox lookup failed")
		return
	}

	if prior, err := s.St.GetNotifyInboxByFact(delivery.TenantID, delivery.Profile.SourceApp, delivery.Profile.EventType, delivery.Profile.SourceRef); err == nil && delivery.Profile.SourceVersion < prior.SourceVersion {
		writeReceipt(w, prior.ReceiptJSON)
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "inbox lookup failed")
		return
	}

	rec, err := notifyingest.FetchRecord(r.Context(), src.FetchBase, src.FetchToken, delivery.TenantID, delivery.Profile.SourceRef)
	if err != nil {
		s.Log.Printf("notify ingest fetch failed tenant=%s source=%s event=%s", delivery.TenantID, src.AppID, delivery.Profile.EventID)
		fail(w, http.StatusServiceUnavailable, "source_unavailable", "authorized submission could not be fetched; not accepted")
		return
	}
	if notifyingest.RecordSHA256(rec.Name, rec.Phone, rec.Wechat) != delivery.Profile.PayloadRef.SHA256 {
		fail(w, http.StatusConflict, "payload_hash_mismatch", "fetched submission does not match payload_ref.sha256")
		return
	}
	if notifyingest.NonLeadChannel(delivery.Payload.Channel, rec.Purpose) {
		fail(w, http.StatusUnprocessableEntity, "not_a_submission", "browse or click events do not create contacts")
		return
	}

	revoke := delivery.Profile.EventType == notifyingest.EventLeadConsentRevoked || delivery.Payload.Revoked || rec.Authorization == "revoked"
	if !revoke && rec.Authorization != "granted" {
		fail(w, http.StatusUnprocessableEntity, "authorization_required", "submission is not authorized")
		return
	}

	tx, err := s.St.DB.Begin()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "ingest begin failed")
		return
	}
	defer tx.Rollback()

	if existing, err := getInboxEvent(tx, delivery); err == nil {
		if existing.BodySHA256 != bodySHA {
			fail(w, http.StatusConflict, "event_content_conflict", "the same event key was delivered with different content")
			return
		}
		if err := tx.Commit(); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
			return
		}
		writeReceipt(w, existing.ReceiptJSON)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "inbox lookup failed")
		return
	}

	// 事实键上 source_version 单调。更旧的另一个 event_id 只确认、不改数据，
	// 避免 Notify 把 500 重试到死信。同版本异正文仍是 409。
	prior, factErr := store.GetNotifyInboxByFactTx(tx, delivery.TenantID, delivery.Profile.SourceApp, delivery.Profile.EventType, delivery.Profile.SourceRef)
	advance := false
	if factErr == nil {
		switch {
		case delivery.Profile.SourceVersion < prior.SourceVersion,
			delivery.Profile.SourceVersion == prior.SourceVersion && prior.BodySHA256 == bodySHA:
			if err := tx.Commit(); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
				return
			}
			writeReceipt(w, prior.ReceiptJSON)
			return
		case delivery.Profile.SourceVersion == prior.SourceVersion:
			fail(w, http.StatusConflict, "event_content_conflict", "the same fact was delivered with different content")
			return
		default:
			advance = true
		}
	} else if !errors.Is(factErr, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "inbox lookup failed")
		return
	}

	revVersion, revokedBefore, err := store.NotifyRevocationVersion(tx, delivery.TenantID, delivery.Profile.SourceApp, delivery.Profile.SourceRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	}
	marketingBlocked := revoke || revokedBefore

	var leadID, contactID, class string
	duplicate := false
	if revoke {
		leadID, contactID, _, err = findLead(tx, delivery)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
			return
		}
		if contactID != "" {
			if err := store.RevokeMarketingTx(tx, delivery.TenantID, contactID, "notify_revoke"); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "revoke failed")
				return
			}
			if err := store.RevokeConsentChannelsTx(tx, delivery.TenantID, contactID, []string{"marketing_phone", "marketing_sms"}, "notify_revoke"); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "revoke failed")
				return
			}
		}
		if err := storeUpsertRevocation(tx, delivery, maxInt(delivery.Profile.SourceVersion, revVersion)); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "revocation write failed")
			return
		}
		class = "revocation"
	} else {
		name := strings.TrimSpace(rec.Name)
		if name == "" && strings.TrimSpace(rec.ChannelSubjectRef) != "" {
			name = "未留姓名"
		}
		if name == "" {
			fail(w, http.StatusUnprocessableEntity, "insufficient_contact", "authorized submission has no name or channel subject")
			return
		}
		snapshot := provenanceSnapshot(delivery)
		sourceRefID, err := ensureSource(tx, delivery, snapshot)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "source ref failed")
			return
		}
		var found bool
		leadID, contactID, found, err = findLead(tx, delivery)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
			return
		}
		if !found {
			res, err := store.IntakeLeadInTx(tx, store.IntakeInput{
				TenantID: delivery.TenantID, SourceApp: delivery.Profile.SourceApp, SourceNS: "notify",
				EventID: delivery.Profile.EventID, Content: body,
				ContactName: name, Phone: rec.Phone, Email: rec.Email,
				BusinessCategory: "merchant_customer", SourceType: sourceTypeFor(delivery.Profile.SourceApp),
				SourceRefID: sourceRefID, Pepper: s.Cfg.DedupPepper,
				FilterEnabled: s.Cfg.FeatureLeadsFilter, AssignEnabled: s.Cfg.FeatureLeadsAssign,
			})
			if errors.Is(err, store.ErrEventContentConflict) {
				fail(w, http.StatusConflict, "event_content_conflict", "the same event key was delivered with different content")
				return
			}
			if err != nil {
				fail(w, http.StatusInternalServerError, "internal", "intake failed")
				return
			}
			leadID, contactID, class, duplicate = res.LeadID, res.ContactID, res.Class, res.Duplicate
		} else {
			class = "existing"
		}
		notice := firstNonEmptyStr(rec.Consents.NoticeVersion, delivery.Payload.ConsentVersion)
		phoneAllowed := rec.Consents.MarketingPhone && !marketingBlocked
		smsAllowed := rec.Consents.MarketingSMS && !marketingBlocked
		if err := store.UpsertConsentsTx(tx, delivery.TenantID, contactID, []store.ConsentUpsert{
			{SourceSubmissionRef: delivery.Profile.SourceRef, SourceChannel: "form_authorization", NoticeVersion: notice, Purpose: "form_authorization", MarketingAllowed: rec.Consents.FormAuthorized},
			{SourceSubmissionRef: delivery.Profile.SourceRef, SourceChannel: "channel_reply", NoticeVersion: notice, Purpose: "channel_reply", MarketingAllowed: rec.Consents.ChannelReply},
			{SourceSubmissionRef: delivery.Profile.SourceRef, SourceChannel: "marketing_phone", NoticeVersion: notice, Purpose: "marketing_phone", MarketingAllowed: phoneAllowed},
			{SourceSubmissionRef: delivery.Profile.SourceRef, SourceChannel: "marketing_sms", NoticeVersion: notice, Purpose: "marketing_sms", MarketingAllowed: smsAllowed},
		}); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "consent write failed")
			return
		}
		if marketingBlocked {
			if err := store.RevokeConsentChannelsTx(tx, delivery.TenantID, contactID, []string{"marketing_phone", "marketing_sms"}, "prior_revocation"); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "revoke failed")
				return
			}
		}
		if err := setNotes(tx, delivery.TenantID, contactID, rec); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "contact note failed")
			return
		}
	}

	receipt := map[string]any{
		"ingestion":      ingestionLabel(revoke, contactID),
		"event_id":       delivery.Profile.EventID,
		"source_ref":     delivery.Profile.SourceRef,
		"source_version": delivery.Profile.SourceVersion,
		"duplicate":      duplicate,
		"class":          class,
	}
	if leadID != "" {
		receipt["lead_id"] = leadID
		receipt["contact_id"] = contactID
	}
	raw, _ := json.Marshal(receipt)
	occurred := ""
	if !delivery.OccurredAt.IsZero() {
		occurred = delivery.OccurredAt.UTC().Format(time.RFC3339)
	}
	inboxRow := store.NotifyInbox{
		TenantID: delivery.TenantID, SourceApp: delivery.Profile.SourceApp, EventType: delivery.Profile.EventType,
		SourceRef: delivery.Profile.SourceRef, SourceVersion: delivery.Profile.SourceVersion,
		ProfileEventID: delivery.Profile.EventID, NotifyEventID: delivery.NotifyEventID,
		DeliveryID: r.Header.Get("X-Notify-Delivery-Id"), BodySHA256: bodySHA,
		LeadID: leadID, ContactID: contactID, ReceiptJSON: string(raw), OccurredAt: occurred,
	}
	var writeErr error
	if advance {
		writeErr = store.UpdateNotifyInboxFactTx(tx, prior.ID, inboxRow)
	} else {
		writeErr = store.InsertNotifyInboxTx(tx, inboxRow)
	}
	if writeErr != nil {
		fail(w, http.StatusInternalServerError, "internal", "inbox write failed")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
		return
	}
	s.Log.Printf("notify ingest tenant=%s source=%s event=%s class=%s lead=%s phone_fpr=%s dup=%t",
		delivery.TenantID, delivery.Profile.SourceApp, delivery.Profile.EventID, class, leadID,
		shortFPR(s.Cfg.DedupPepper, rec.Phone), duplicate)
	writeReceipt(w, string(raw))
}

func (s *Server) handleNotifyReceipt(w http.ResponseWriter, r *http.Request) {
	if problems := s.Cfg.IngestGate(); len(problems) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "config_gate_ingest", "message": "notify ingest is not configured (fail-closed)", "detail": problems,
		})
		return
	}
	appID := strings.TrimSpace(r.Header.Get("X-Notify-App-ID"))
	src, ok := sourceByApp(s.Cfg.IngestSources, appID)
	if !ok {
		fail(w, http.StatusUnauthorized, "bad_signature", "source is not registered")
		return
	}
	macBody := "GET\n" + r.URL.Path
	if !notifyingest.VerifySignature(src.Secret, r.Header.Get("X-Notify-Signature"), []byte(macBody)) {
		fail(w, http.StatusUnauthorized, "bad_signature", "signature does not match a registered source")
		return
	}
	eventID := r.PathValue("eventID")
	rows, err := s.St.ListNotifyInboxByEvent(appID, eventID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "receipt lookup failed")
		return
	}
	if len(rows) == 0 {
		fail(w, http.StatusNotFound, "not_found", "no receipt for this event")
		return
	}
	if len(rows) > 1 {
		fail(w, http.StatusConflict, "ambiguous_receipt", "more than one receipt for this event")
		return
	}
	writeReceipt(w, rows[0].ReceiptJSON)
}

func matchIngestSignature(sources []config.IngestSource, header string, body []byte) (config.IngestSource, bool) {
	var matched config.IngestSource
	n := 0
	for _, src := range sources {
		if notifyingest.VerifySignature(src.Secret, header, body) {
			matched = src
			n++
		}
	}
	return matched, n == 1
}

func sourceByApp(sources []config.IngestSource, appID string) (config.IngestSource, bool) {
	for _, src := range sources {
		if src.AppID == appID {
			return src, true
		}
	}
	return config.IngestSource{}, false
}

func writeReceipt(w http.ResponseWriter, raw string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(raw))
	if !strings.HasSuffix(raw, "\n") {
		_, _ = w.Write([]byte("\n"))
	}
}

func ingestionLabel(revoke bool, contactID string) string {
	if !revoke {
		return "accepted"
	}
	if contactID == "" {
		return "revocation_accepted"
	}
	return "revoked"
}

func sourceTypeFor(app string) string {
	if app == "touch-engine" {
		return "touch_campaign"
	}
	return "form"
}

func provenanceSnapshot(d notifyingest.Delivery) string {
	b, _ := json.Marshal(map[string]any{
		"campaign_ref": d.Payload.CampaignRef, "store_ref": d.Payload.StoreRef,
		"channel": d.Payload.Channel, "tag": d.Payload.Tag, "asset_ref": d.Payload.AssetRef,
		"source_version": d.Profile.SourceVersion, "occurred_at": d.OccurredAt.UTC().Format(time.RFC3339),
		"consent_version": d.Payload.ConsentVersion,
	})
	return string(b)
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func contactExtraNotes(rec notifyingest.Record) string {
	extra := map[string]string{}
	if v := strings.TrimSpace(rec.Wechat); v != "" {
		extra["wechat"] = v
	}
	if v := strings.TrimSpace(rec.ChannelSubjectRef); v != "" {
		extra["channel_subject_ref"] = v
	}
	if len(extra) == 0 {
		return ""
	}
	b, _ := json.Marshal(extra)
	return string(b)
}

// wrappers keep the handler from reaching into unexported store helpers.
func getInboxEvent(tx *sql.Tx, d notifyingest.Delivery) (store.NotifyInbox, error) {
	return store.GetNotifyInboxByEventTx(tx, d.TenantID, d.Profile.SourceApp, d.Profile.EventID)
}

func findLead(tx *sql.Tx, d notifyingest.Delivery) (string, string, bool, error) {
	return store.FindLeadBySourceTx(tx, d.TenantID, d.Profile.SourceApp, d.Profile.SourceRef)
}

func ensureSource(tx *sql.Tx, d notifyingest.Delivery, snapshot string) (string, error) {
	return store.EnsureSourceRefTx(tx, d.TenantID, d.Profile.SourceApp, d.Profile.SourceRef, snapshot)
}

func storeUpsertRevocation(tx *sql.Tx, d notifyingest.Delivery, version int) error {
	return store.UpsertNotifyRevocationTx(tx, d.TenantID, d.Profile.SourceApp, d.Profile.SourceRef, version)
}

func setNotes(tx *sql.Tx, tenantID, contactID string, rec notifyingest.Record) error {
	return store.SetContactNotesIfEmptyTx(tx, tenantID, contactID, contactExtraNotes(rec))
}
