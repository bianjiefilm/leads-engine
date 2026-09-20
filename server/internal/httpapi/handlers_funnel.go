// HUI-1694 / FEAT-0195 全漏斗分析端点(FEATURE_FUNNEL 闸控,默认 off ->
// 路由不注册 -> 404 不可见;on 时挂 requireSession 既有鉴权中间件):
//   - GET /api/v1/funnel?window_start=&window_end=[&granularity=][&source_type=]
//     纯只读计算,零状态:同窗口重算结果恒一致;窗口为 [start, end) 半开区间,
//     RFC3339 必填且 end 必须晚于 start(非法/缺省一律 400,绝不静默夹逼);
//   - 权限:租户级聚合走 ActionReadList(与商机统计同款先例);owner 租户全量,
//     sales/agent 只见自己被指派人群(复用既有记录级作用域推导,不发明新模型);
//     跨租户无成员行一律 403(中间件 fail-closed);
//   - UNKNOWN 诚实降级:曝光/留资触点在响应中 available=false + 中文 reason,
//     count 为 null —— 绝不推算、绝不置 0;
//   - PII 纪律:响应只含聚合计数与定义披露,零联系方式、零档案原文,可进日志。
package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// handleFunnel answers the read-only full-funnel analysis for the caller's
// tenant and role scope.
func (s *Server) handleFunnel(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	query := r.URL.Query()

	// 窗口必填且合法:RFC3339 解析失败/缺省即 400,规范化为 UTC 秒精度
	// (复用跟进时间戳的规范化纪律),end 必须晚于 start。
	start, ok := normalizeFollowUpWhen(query.Get("window_start"))
	if !ok {
		fail(w, http.StatusBadRequest, "bad_request", "window_start is required and must be an RFC3339 timestamp")
		return
	}
	endRaw, ok := normalizeFollowUpWhen(query.Get("window_end"))
	if !ok {
		fail(w, http.StatusBadRequest, "bad_request", "window_end is required and must be an RFC3339 timestamp")
		return
	}
	startT, _ := time.Parse(time.RFC3339, start)
	endT, _ := time.Parse(time.RFC3339, endRaw)
	if !endT.After(startT) {
		fail(w, http.StatusBadRequest, "bad_request", "window_end must be after window_start ([start, end) 半开窗口不接受空窗口)")
		return
	}

	// 时间粒度下钻:day(默认,产出日序列)/ none(只出总数)。
	granularity := query.Get("granularity")
	if granularity == "" {
		granularity = "day"
	}
	if granularity != "day" && granularity != "none" {
		fail(w, http.StatusBadRequest, "bad_request", "granularity must be day or none")
		return
	}

	// 来源下钻:仅接受既有 contacts.source_type 域;不发明新维度。
	sourceType := strings.TrimSpace(query.Get("source_type"))
	if sourceType != "" && !validSourceTypes[sourceType] {
		fail(w, http.StatusBadRequest, "bad_request", "source_type must be manual, form or touch_campaign")
		return
	}

	q := store.FunnelQuery{
		TenantID:    c.Member.TenantID,
		SourceType:  sourceType,
		WithSeries:  granularity == "day",
		WindowStart: startT,
		WindowEnd:   endT,
	}
	// 记录级作用域复用(与商机统计同款先例):非 owner 只见自己人群。
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		q.AssigneeMemberID = c.Member.ID
	}
	rep, err := s.St.FunnelAnalysis(q)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "funnel analysis failed")
		return
	}
	// 日志只有租户/窗口/粒度/作用域,零业务计数细节、零 PII。
	s.Log.Printf("funnel tenant=%s window=[%s,%s) granularity=%s scope=%s by=%s",
		c.Member.TenantID, rep.WindowStart, rep.WindowEnd, rep.Granularity, rep.Scope, c.Member.ID)
	writeJSON(w, http.StatusOK, rep)
}
