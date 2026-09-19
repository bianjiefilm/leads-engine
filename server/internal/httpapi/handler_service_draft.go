// handler_service_draft.go — HUI-1749 「创建服务需求草稿」HTTP surface.
//
// 纪律(票面红线):
//   - 只有 creative_service 商机可见/可调用(API+UI 双侧断言的 API 侧);
//     merchant_customer 一律 422 category_not_applicable,动作不存在;
//   - 无 AI 评分、无 stage=won 自动触发:一切都由用户从详情页显式发起;
//   - confirm:false = 预览(零持久化,缺的字段标缺失,绝不从 CRM 备注猜);
//     confirm:true = 显式确认(幂等快照 + 投递);
//   - 投递成功 ≠ 成交/已收款:所有响应只携带受限状态投影与守卫文案;
//   - 撤销只在接单侧未接受前允许;已接受 → 409 引导走接单侧变更流程。
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/handoffsender"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// deliveryNoteDelivered is the standing guard copy: the receiver created a
// draft — that is a handoff fact, never a deal fact (投递成功 ≠ 成交).
const deliveryNoteDelivered = "接单侧已建立服务需求草稿;这只是交接事实投影,不代表成交/已收款,商机状态以 CRM 为准。"

// ---- shared guards ----------------------------------------------------------

// serviceDraftPrelude runs the shared guards for every service-draft
// endpoint, in a fixed order: record visibility/authz (404 masking) → the
// creative_service category gate (422: the action does not exist for other
// categories, regardless of configuration) → the eco config gate (503
// config_gate_eco, fail-closed).
func (s *Server) serviceDraftPrelude(w http.ResponseWriter, r *http.Request, action authz.Action) (*caller, store.Opportunity, bool) {
	c := callerFrom(r)
	rec, ok := s.oppRecord(w, r, c, action)
	if !ok {
		return nil, store.Opportunity{}, false
	}
	if rec.BusinessCategory != "creative_service" {
		fail(w, http.StatusUnprocessableEntity, "category_not_applicable",
			"只有创意服务(creative_service)商机提供「创建服务需求草稿」动作")
		return nil, store.Opportunity{}, false
	}
	if s.Eco == nil || len(s.Cfg.EcoGate()) > 0 {
		s.failEcoGate(w)
		return nil, store.Opportunity{}, false
	}
	return c, rec, true
}

func (s *Server) failEcoGate(w http.ResponseWriter) {
	problems := s.Cfg.EcoGate()
	if s.Eco == nil {
		problems = append(problems, "app registry unavailable")
	}
	fail(w, http.StatusServiceUnavailable, "config_gate_eco",
		"eco handoff 未配置(功能开但部署事实缺失,拒绝服务): "+strings.Join(problems, "; "))
}

// failHandoffErr maps handoffsender typed errors to the contract codes.
// Returns true when the error was handled.
func failHandoffErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var re *handoffsender.ReceiverError
	switch {
	case errors.Is(err, handoffsender.ErrNotConfigured):
		fail(w, http.StatusServiceUnavailable, "config_gate_eco",
			"eco handoff 未配置(拒绝服务,不伪造投递)")
	case errors.Is(err, handoffsender.ErrSuperseded):
		// 旧快照显式拒绝:内容变更有新版本,绝不静默重发旧内容。
		fail(w, http.StatusConflict, "superseded_version",
			"该 handoff 不是最新快照:内容已变更为新版本,旧版本不可静默重发")
	case errors.Is(err, handoffsender.ErrAlreadyRevoked):
		fail(w, http.StatusConflict, "handoff_revoked",
			"该交接已撤销:请重新确认(将生成新版本新引用)")
	case errors.Is(err, handoffsender.ErrAcceptedByTarget):
		fail(w, http.StatusConflict, "accepted_change_via_target",
			"接单侧已接受该需求:后续变更请走接单应用的需求变更流程,此处不覆盖已确认内容")
	case errors.Is(err, handoffsender.ErrCannotConfirmTargetState):
		fail(w, http.StatusServiceUnavailable, "target_state_unconfirmed",
			"接单侧状态不可确认(fail-closed):不执行盲撤,请稍后重试")
	case errors.Is(err, handoffsender.ErrHandoffNotFound):
		fail(w, http.StatusNotFound, "not_found", "no handoff snapshot for this opportunity")
	case errors.Is(err, handoffsender.ErrReceiverConflict):
		fail(w, http.StatusConflict, "handoff_id_conflict",
			"接单侧报告同键异内容冲突:本地快照未被覆盖,已记录待人工处置")
	case errors.As(err, &re):
		fail(w, http.StatusUnprocessableEntity, "receiver_rejected",
			"接单侧拒绝该交接: "+re.Code+" "+re.Reason)
	default:
		fail(w, http.StatusInternalServerError, "internal", "handoff operation failed")
	}
	return true
}

// ---- request / projection shapes ---------------------------------------------

type serviceDraftIntentIn struct {
	Confirm         bool                       `json:"confirm"`
	Summary         string                     `json:"summary"`
	ServiceCategory string                     `json:"service_category"`
	BudgetCents     *int64                     `json:"budget_cents"`
	Deadline        string                     `json:"deadline"`
	Assets          []handoffsender.AssetInput `json:"assets"`
}

func (in serviceDraftIntentIn) confirmedInput(rec store.Opportunity, tenantID string) handoffsender.ConfirmedInput {
	return handoffsender.ConfirmedInput{
		TenantID:         tenantID,
		OpportunityID:    rec.ID,
		OpportunityTitle: rec.Title,
		BusinessCategory: rec.BusinessCategory,
		Summary:          in.Summary,
		ServiceCategory:  in.ServiceCategory,
		BudgetCents:      in.BudgetCents,
		Deadline:         in.Deadline,
		Assets:           in.Assets,
	}
}

// handoffProjection is the RESTRICTED status projection: identities and
// states only — never doc_json/doc_sha256 internals, never contact data.
func handoffProjection(h *store.OpportunityHandoff) map[string]any {
	return map[string]any{
		"handoff_id":     h.HandoffID,
		"source_version": h.SourceVersion,
		"local_status":   h.LocalStatus,
		"draft_ref":      h.DraftRef,
		"target_app":     h.TargetApp,
		"target_status":  h.TargetStatus,
		"target_dirty":   h.TargetDirty,
		"target_revoked": h.TargetRevoked,
		"confirmed_at":   h.ConfirmedAt,
		"delivered_at":   h.DeliveredAt,
		"revoked_at":     h.RevokedAt,
		"last_error":     h.LastError,
		"note":           handoffNote(h),
	}
}

func handoffNote(h *store.OpportunityHandoff) string {
	switch h.LocalStatus {
	case handoffsender.StatusDelivered:
		return deliveryNoteDelivered
	case handoffsender.StatusDeliveryFailed:
		return "交接快照已保存;投递暂未完成,可重试(同一 handoff 引用),确认事实不丢失。"
	case handoffsender.StatusRevoked:
		return "该交接已撤销;如需继续请重新确认(生成新版本新引用)。"
	default:
		return "交接快照已确认,待投递。"
	}
}

// ---- handlers ----------------------------------------------------------------

// handleServiceDraftIntent: confirm:false → 200 preview (zero persistence,
// missing marks); confirm:true → 201 new snapshot delivered / 200 idempotent
// recovery of the same reference (double click, timeout retry, restart).
func (s *Server) handleServiceDraftIntent(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.serviceDraftPrelude(w, r, authz.ActionUpdate)
	if !ok {
		return
	}
	var in serviceDraftIntentIn
	if !decodeBody(w, r, &in) {
		return
	}
	input := in.confirmedInput(rec, c.Member.TenantID)

	if !in.Confirm {
		// 预览:只校验**已提供**字段的形状;缺失字段进 missing 列表,绝不代填。
		if err := input.ValidateProvided(); err != nil {
			fail(w, http.StatusBadRequest, "invalid_confirm_input", err.Error())
			return
		}
		missing := input.MissingFields()
		if missing == nil {
			missing = []string{} // 空列表以 [] 表达,而非 null
		}
		assets := in.Assets
		if assets == nil {
			assets = []handoffsender.AssetInput{}
		}
		receiver := map[string]any{
			"target_app":       s.Eco.Cfg.TargetAppID,
			"return_target_id": handoffsender.ReturnTargetID,
			"scopes":           handoffsender.DeliveryScopes,
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"preview": map[string]any{
				"opportunity_id":    rec.ID,
				"opportunity_title": rec.Title,
				"business_category": rec.BusinessCategory,
				"summary":           in.Summary,
				"service_category":  in.ServiceCategory,
				"budget_cents":      in.BudgetCents,
				"deadline":          in.Deadline,
				"assets":            assets,
				"missing":           missing,
				"receiver":          receiver,
				"note":              "确认后将向接单应用创建服务需求草稿并登记交接引用;投递成功不代表成交/已收款。缺失字段按缺失交接,不由 CRM 备注猜测。",
			},
		})
		return
	}

	if err := input.Validate(); err != nil {
		fail(w, http.StatusBadRequest, "invalid_confirm_input", err.Error())
		return
	}
	res, err := s.Eco.Confirm(r.Context(), input, c.Principal.ID, s.Cfg.IdentityBaseURL, c.Member.ID)
	if err != nil {
		failHandoffErr(w, err)
		return
	}
	code := http.StatusOK
	if res.Delivered && !res.Duplicate {
		code = http.StatusCreated // 新确认新快照已投递
	}
	writeJSON(w, code, map[string]any{
		"handoff":   handoffProjection(res.Handoff),
		"duplicate": res.Duplicate,
		"delivered": res.Delivered,
		"note":      res.Note,
	})
}

// handleServiceDraftGet: the stored restricted projection (404 until the
// first confirmation exists).
func (s *Server) handleServiceDraftGet(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.serviceDraftPrelude(w, r, authz.ActionReadRecord)
	if !ok {
		return
	}
	h, err := s.Eco.St.LatestOpportunityHandoff(rec.ID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	if h == nil {
		fail(w, http.StatusNotFound, "not_found", "no handoff snapshot yet (尚未确认过服务需求草稿)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": handoffProjection(h)})
}

// handleServiceDraftRefresh pulls the receiver's live projection and updates
// the locally stored one (restricted CRM-side status mirror).
func (s *Server) handleServiceDraftRefresh(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.serviceDraftPrelude(w, r, authz.ActionUpdate)
	if !ok {
		return
	}
	h, err := s.Eco.Refresh(r.Context(), rec.ID, c.Member.TenantID)
	if err != nil {
		if errors.Is(err, handoffsender.ErrHandoffNotFound) {
			fail(w, http.StatusNotFound, "not_found", "no handoff snapshot yet (尚未确认过服务需求草稿)")
			return
		}
		if errors.Is(err, handoffsender.ErrTransport) {
			fail(w, http.StatusServiceUnavailable, "receiver_unreachable",
				"接单侧暂不可达:本地投影保持原样,可稍后刷新")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "refresh failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": handoffProjection(h)})
}

// handleServiceDraftRetry re-delivers the LATEST snapshot's exact stored
// bytes. A specific older handoff_id is answered with 409 superseded_version.
func (s *Server) handleServiceDraftRetry(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.serviceDraftPrelude(w, r, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		HandoffID string `json:"handoff_id"`
	}
	// body 可选(裸重试 = 重发最新快照)。
	if r.Body != nil {
		raw, rerr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if rerr == nil && len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
				return
			}
		}
	}
	res, err := s.Eco.Retry(r.Context(), rec.ID, c.Member.TenantID, in.HandoffID)
	if err != nil {
		failHandoffErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff":   handoffProjection(res.Handoff),
		"delivered": res.Delivered,
		"note":      res.Note,
	})
}

// handleServiceDraftRevoke: only before the target accepts. After
// acceptance → 409 accepted_change_via_target (change via the target flow).
func (s *Server) handleServiceDraftRevoke(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.serviceDraftPrelude(w, r, authz.ActionUpdate)
	if !ok {
		return
	}
	h, err := s.Eco.Revoke(r.Context(), rec.ID, c.Member.TenantID)
	if err != nil {
		failHandoffErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": handoffProjection(h)})
}
