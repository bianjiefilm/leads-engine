// HUI-1685 / FEAT-0186 线索自动分配池配置端点(FEATURE_LEADS_ASSIGN 闸控,
// 默认 off -> 路由不注册 -> 404 不可见;on 时全部挂 requireSession 既有鉴权
// 中间件,owner 专属 —— 池配置直接决定线索流向,属租户级销售运营决策):
//   - GET/POST /api/v1/leads/assign-pool、PATCH/DELETE /api/v1/leads/assign-pool/{id};
//   - 校验全部在服务端单点判定(成员须本租户、在册、在职、sales 语义;
//     weight 1..1000;标签 ≤64 runes),BFF 零业务判断;
//   - 池条目是配置;有效池在分配时刻 JOIN members 现算(停用/改角色动态退出)。
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// assignPoolError maps store domain errors to HTTP statuses: duplicates 409,
// not-found 404, validation ("assign pool: …") 400 with the message as-is.
func assignPoolError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrAssignPoolDuplicate):
		fail(w, http.StatusConflict, "assign_pool_exists",
			"this member already has a pool entry; patch it instead")
	case errors.Is(err, store.ErrAssignPoolNotFound):
		fail(w, http.StatusNotFound, "not_found", "pool entry not found")
	default:
		if msg, ok := assignPoolValidation(err); ok {
			fail(w, http.StatusBadRequest, "bad_request", msg)
		} else {
			fail(w, http.StatusInternalServerError, "internal", "assign pool write failed")
		}
	}
	return true
}

// assignPoolValidation recognizes the store layer's own validation errors.
func assignPoolValidation(err error) (string, bool) {
	const prefix = "assign pool: "
	if msg := err.Error(); strings.HasPrefix(msg, prefix) {
		return msg[len(prefix):], true
	}
	return "", false
}

func (s *Server) handleAssignPoolCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAssignPool, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		MemberID string  `json:"member_id"`
		Weight   *int    `json:"weight"`
		Region   *string `json:"region"`
		Industry *string `json:"industry"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.MemberID == "" {
		fail(w, http.StatusBadRequest, "bad_request", "member_id is required")
		return
	}
	weight := store.AssignWeightMin // 缺省(nil)= 默认 1;显式越界值由域校验 400 拒绝
	if in.Weight != nil {
		weight = *in.Weight
	}
	region, industry := "", ""
	if in.Region != nil {
		region = *in.Region
	}
	if in.Industry != nil {
		industry = *in.Industry
	}
	e, err := s.St.CreateAssignPoolEntry(c.Member.TenantID, in.MemberID, weight, region, industry, c.Member.ID)
	if assignPoolError(w, err) {
		return
	}
	s.Log.Printf("assign pool entry created tenant=%s pool=%s member=%s weight=%d by=%s",
		c.Member.TenantID, e.ID, e.MemberID, e.Weight, c.Member.ID)
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) handleAssignPoolList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAssignPool, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	items, err := s.St.ListAssignPoolEntries(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "assign pool list failed")
		return
	}
	if items == nil {
		items = []store.AssignPoolEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAssignPoolPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAssignPool, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Weight   *int    `json:"weight"`
		Region   *string `json:"region"`
		Industry *string `json:"industry"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	e, err := s.St.UpdateAssignPoolEntry(r.PathValue("id"), c.Member.TenantID, in.Weight, in.Region, in.Industry)
	if assignPoolError(w, err) {
		return
	}
	s.Log.Printf("assign pool entry patched tenant=%s pool=%s member=%s weight=%d by=%s",
		c.Member.TenantID, e.ID, e.MemberID, e.Weight, c.Member.ID)
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleAssignPoolDelete(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAssignPool, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	id := r.PathValue("id")
	if err := s.St.DeleteAssignPoolEntry(id, c.Member.TenantID); assignPoolError(w, err) {
		return
	}
	s.Log.Printf("assign pool entry deleted tenant=%s pool=%s by=%s", c.Member.TenantID, id, c.Member.ID)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}
