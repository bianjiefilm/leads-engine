package httpapi

// HUI-1693 / FEAT-0194 商机域端点:阶段转换(审计+幂等)、阶段历史、
// 按类别隔离的统计、以及 FEATURE_SERVICE_DRAFT 挂载点(L1,归 HUI-1749/1751)。

import (
	"net/http"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// handleOppStage is the ONLY way to change an opportunity's stage. It reuses
// the L0 update permission (owner: any tenant record; sales/agent: own records
// only; non-assignee masked 404; cross-tenant/disabled 403). Repeating the
// current stage is idempotent: 200 with the unchanged record and no new
// history row. won = 人工标记成交,绝不表示已支付/已收款。
func (s *Server) handleOppStage(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.oppRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		ToStage string `json:"to_stage"`
		Note    string `json:"note"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !validOppStage[in.ToStage] {
		fail(w, http.StatusBadRequest, "bad_request",
			"to_stage must be open, qualified, proposal, negotiation, won or closed_lost")
		return
	}
	updated, _, err := s.St.TransitionOpportunityStage(rec.ID, rec.TenantID, in.ToStage, c.Member.ID, in.Note)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stage transition failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleOppStageHistory returns the audited transition chain, oldest first.
func (s *Server) handleOppStageHistory(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.oppRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	items, err := s.St.ListOpportunityStageEvents(rec.ID, rec.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stage history lookup failed")
		return
	}
	if items == nil {
		items = []store.OpportunityStageEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleOppStats answers the funnel + amount summary for exactly ONE business
// category. 无跨类别合计:category 必填,缺省/非法一律 400 —— 统计口径按
// FEAT-0194 分域补充第 2 条隔离,商家经营销售与创意服务成交额/漏斗不得混算。
// NULL 金额进 unknown_count(「未知」桶),绝不当 0。sales/agent 只见自己的漏斗。
func (s *Server) handleOppStats(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	category := r.URL.Query().Get("category")
	if !validCategories[category] {
		fail(w, http.StatusBadRequest, "bad_request",
			"category query parameter is required and must be merchant_customer or creative_service (no cross-category totals)")
		return
	}
	filter := ""
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		filter = c.Member.ID
	}
	st, err := s.St.OpportunityStats(c.Member.TenantID, category, filter)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stats failed")
		return
	}
	writeJSON(w, http.StatusOK, st)
}
