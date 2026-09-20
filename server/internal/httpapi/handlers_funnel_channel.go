// HUI-1695 / FEAT-0196 渠道效果分析端点(FEATURE_CHANNEL_ANALYTICS 闸控,默认
// off -> 路由不注册 -> 404 不可见;on 时挂 requireSession 既有鉴权中间件):
//   - GET /api/v1/funnel/channels?window_start=&window_end=
//     渠道效果 = 按渠道(contacts.source_type 既有域)分组的线索质量漏斗对比:
//     阶段定义/窗口语义/作用域推导全部复用 FEAT-0195,零迁移零状态,同窗口
//     重算恒一致;窗口为 [start, end) 半开区间,RFC3339 必填(非法/缺省一律
//     400,绝不静默夹逼);
//   - 渠道 group-by 的互斥参数显式 400:granularity(渠道对比只出总数)与
//     source_type(group-by 本身就是来源维度),绝不静默忽略造成「以为生效了」;
//   - low_sample 机器标注:样本量(该渠道窗口内建档身份基数)低于常量阈值 10
//     时为 true,阈值与规则随响应披露;
//   - ROI 诚实降级:广告花费等费用事实不在本仓(费用归因属 HUI-1696),
//     available=false + 中文 reason,spend/roi/cac 恒 JSON null —— 绝不推算、
//     绝不置 0、无演示数据;
//   - 权限与 /api/v1/funnel 同款:租户级聚合走 ActionReadList;owner 租户全量,
//     sales/agent 只见自己被指派人群(复用既有记录级作用域推导,不发明新模型);
//     跨租户无成员行一律 403(中间件 fail-closed);
//   - PII 纪律:响应只含聚合计数与定义披露,零联系方式、零档案原文,可进日志。
package httpapi

import (
	"net/http"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// handleFunnelChannels answers the read-only channel-grouped funnel for the
// caller's tenant and role scope.
func (s *Server) handleFunnelChannels(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	query := r.URL.Query()

	// 窗口必填且合法:与 FEAT-0195 同款规范化与 [start,end) 校验(RFC3339
	// 解析失败/缺省即 400,规范化为 UTC 秒精度,end 必须晚于 start)。
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

	// 渠道 group-by 的互斥参数:显式拒绝,绝不静默忽略。
	if query.Get("granularity") != "" {
		fail(w, http.StatusBadRequest, "bad_request", "granularity is not supported on the channel group-by (totals only)")
		return
	}
	if query.Get("source_type") != "" {
		fail(w, http.StatusBadRequest, "bad_request", "source_type drill-down is not applicable to the channel group-by (the group-by IS the source dimension)")
		return
	}

	q := store.FunnelQuery{
		TenantID:    c.Member.TenantID,
		WindowStart: startT,
		WindowEnd:   endT,
	}
	// 记录级作用域复用(与 /api/v1/funnel 同款先例):非 owner 只见自己人群。
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		q.AssigneeMemberID = c.Member.ID
	}
	rep, err := s.St.ChannelFunnelAnalysis(q)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "channel funnel analysis failed")
		return
	}
	// 日志只有租户/窗口/作用域,零业务计数细节、零 PII。
	s.Log.Printf("channel_funnel tenant=%s window=[%s,%s) scope=%s by=%s",
		c.Member.TenantID, rep.WindowStart, rep.WindowEnd, rep.Scope, c.Member.ID)
	writeJSON(w, http.StatusOK, rep)
}
