// HUI-1690 / FEAT-0191 客户画像标签端点(FEATURE_CONTACT_TAGS 闸控,默认 off ->
// 路由不注册 -> 404 不可见;on 时全部挂 requireSession 既有鉴权中间件):
//   - 人工标签定义 /api/v1/contact-tags CRUD:owner 专属(authz manage_contact_tags,
//     名称租户内唯一、颜色可空 #RRGGBB、描述可空;在用拒删);
//   - 打标/去标 /api/v1/contacts/{id}/tags:既有记录级作用域(update 动作;
//     非 assignee 404 掩码),幂等 + 首戳留痕可回查(谁/何时/哪标签);
//   - 派生标签 /api/v1/contact-tags/derived 与 /api/v1/contacts/{id}/derived-tags:
//     纯只读按需重算不落库,定义披露(事实来源+判定规则+阈值,FEAT-0195 范式);
//     AI 画像评分不在接口面(外部事实源 deferred,如实不出现评分字段);
//   - 分群 /api/v1/contact-tags/segment:标签组合筛选(组间 AND、组内 OR,
//     combine_rule 随响应披露),结果只回裸 contact_id 引用;非 owner 限定自己
//     人群(记录级作用域复用);零 PII——标签值与结果不外泄联系方式/姓名原文。
//
// PII 纪律:日志只记 id/计数(标签名是租户自撰文本,不入日志);分群与派生
// 响应体不含手机号/邮箱/姓名原文(contact_tags_test.go 逐字节断言)。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const (
	tagDefNameMaxRunes = 50  // 名称上限:运营口径短标签
	tagDefDescMaxRunes = 200 // 描述上限
)

// tagDefColorOK guards the optional color as #RRGGBB(空串=未设置).
var tagDefColorOK = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// validateTagDefInput trims and validates manual tag definition fields;
// ok=false means the caller must answer 400.
func validateTagDefInput(name, color, description string) (string, string, string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || runeLen(name) > tagDefNameMaxRunes {
		return "", "", "", false
	}
	if color != "" && !tagDefColorOK.MatchString(color) {
		return "", "", "", false
	}
	if runeLen(description) > tagDefDescMaxRunes {
		return "", "", "", false
	}
	return name, color, description, true
}

// writeTagDefItems writes the list envelope with a non-nil items array.
func writeTagDefItems(w http.ResponseWriter, defs []store.ContactTagDef) {
	if defs == nil {
		defs = []store.ContactTagDef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": defs})
}

// ---- manual tag definitions (owner only) -----------------------------------

// handleContactTagList answers the tenant's manual tag definitions
// (GET /api/v1/contact-tags;业务角色可读,定义本身零 PII)。
func (s *Server) handleContactTagList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	defs, err := s.St.ListContactTagDefs(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag definition list failed")
		return
	}
	writeTagDefItems(w, defs)
}

// handleContactTagCreate creates a manual tag definition
// (POST /api/v1/contact-tags;owner 专属;重名 409 tag_name_conflict)。
func (s *Server) handleContactTagCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageContactTags, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	name, color, description, ok := validateTagDefInput(in.Name, in.Color, in.Description)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_request",
			"name 必填且不超过 50 字;color 可空、合法时必须为 #RRGGBB;description 不超过 200 字")
		return
	}
	def, err := s.St.CreateContactTagDef(c.Member.TenantID, name, color, description, c.Member.ID)
	if errors.Is(err, store.ErrTagDefDuplicate) {
		fail(w, http.StatusConflict, "tag_name_conflict", "同名标签定义已存在(租户内唯一)")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag definition create failed")
		return
	}
	// 日志只记 id(标签名是租户自撰文本,不入日志)。
	s.Log.Printf("contact tag def created id=%s tenant=%s by=%s", def.ID, c.Member.TenantID, c.Member.ID)
	writeJSON(w, http.StatusCreated, def)
}

// handleContactTagPatch updates name/color/description of a definition
// (PATCH /api/v1/contact-tags/{id};owner 专属;nil 字段保持不变)。
func (s *Server) handleContactTagPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageContactTags, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Name        *string `json:"name"`
		Color       *string `json:"color"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	patch := store.ContactTagDefPatch{}
	if in.Name != nil {
		name, _, _, ok := validateTagDefInput(*in.Name, "", "")
		if !ok {
			fail(w, http.StatusBadRequest, "bad_request", "name 必填且不超过 50 字")
			return
		}
		patch.Name = &name
	}
	if in.Color != nil {
		if *in.Color != "" && !tagDefColorOK.MatchString(*in.Color) {
			fail(w, http.StatusBadRequest, "bad_request", "color 可空、合法时必须为 #RRGGBB")
			return
		}
		patch.Color = in.Color
	}
	if in.Description != nil {
		if runeLen(*in.Description) > tagDefDescMaxRunes {
			fail(w, http.StatusBadRequest, "bad_request", "description 不超过 200 字")
			return
		}
		patch.Description = in.Description
	}
	def, err := s.St.UpdateContactTagDef(r.PathValue("id"), c.Member.TenantID, patch)
	if errors.Is(err, store.ErrTagDefDuplicate) {
		fail(w, http.StatusConflict, "tag_name_conflict", "同名标签定义已存在(租户内唯一)")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "tag definition not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag definition update failed")
		return
	}
	s.Log.Printf("contact tag def updated id=%s tenant=%s by=%s", def.ID, c.Member.TenantID, c.Member.ID)
	writeJSON(w, http.StatusOK, def)
}

// handleContactTagDelete deletes an unused definition
// (DELETE /api/v1/contact-tags/{id};owner 专属;在用 409 tag_in_use)。
func (s *Server) handleContactTagDelete(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageContactTags, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	id := r.PathValue("id")
	err := s.St.DeleteContactTagDef(id, c.Member.TenantID)
	if errors.Is(err, store.ErrTagDefInUse) {
		fail(w, http.StatusConflict, "tag_in_use", "标签仍有联系人在用,先去标再删除")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "tag definition not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag definition delete failed")
		return
	}
	s.Log.Printf("contact tag def deleted id=%s tenant=%s by=%s", id, c.Member.TenantID, c.Member.ID)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// ---- derived tags (read-only, recomputed on demand) --------------------------

// handleDerivedTagCatalog answers the machine-readable derived tag catalog
// (GET /api/v1/contact-tags/derived;定义披露沿 FEAT-0195 范式)。
func (s *Server) handleDerivedTagCatalog(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"as_of":                time.Now().UTC().Format(time.RFC3339),
		"activity_recent_days": store.ActivityRecentDays,
		"tags":                 store.DerivedTagCatalog(),
	})
}

// handleContactDerivedTags recomputes one contact's derived keys on demand
// (GET /api/v1/contacts/{id}/derived-tags;既有记录级作用域 read_record)。
func (s *Server) handleContactDerivedTags(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	now := time.Now().UTC()
	m, err := s.St.ComputeDerivedTags(store.DerivedTagsQuery{
		TenantID:   rec.TenantID,
		ContactIDs: []string{rec.ID},
		AsOf:       now,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "derived tags compute failed")
		return
	}
	keys := m[rec.ID]
	if keys == nil {
		keys = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"contact_id":           rec.ID,
		"as_of":                now.Format(time.RFC3339),
		"activity_recent_days": store.ActivityRecentDays,
		"keys":                 keys,
		"definitions":          store.DerivedTagCatalog(),
	})
}

// ---- contact tagging (record-level scope) ------------------------------------

// handleContactTagsList answers one contact's tag links with the audit trail
// (GET /api/v1/contacts/{id}/tags;非 assignee 404 掩码)。
func (s *Server) handleContactTagsList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	links, err := s.St.ListContactTags(rec.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact tags list failed")
		return
	}
	if links == nil {
		links = []store.ContactTagLink{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": links})
}

// handleContactTagApply applies a manual tag to a contact, idempotent with
// first-stamp-wins (POST /api/v1/contacts/{id}/tags;body {tag_id})。
func (s *Server) handleContactTagApply(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		TagID string `json:"tag_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(in.TagID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "tag_id is required")
		return
	}
	// 引用完整性:标签必须属于本租户(未知 → 400,不泄露他租户标签存在性)。
	if _, err := s.St.GetContactTagDef(in.TagID, rec.TenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusBadRequest, "unknown_tag", "未知标签(必须是本租户在册标签定义)")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "tag lookup failed")
		return
	}
	link, changed, err := s.St.TagContact(rec.TenantID, rec.ID, in.TagID, c.Member.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag apply failed")
		return
	}
	s.Log.Printf("contact tagged contact=%s tag=%s changed=%t by=%s", rec.ID, in.TagID, changed, c.Member.ID)
	writeJSON(w, http.StatusOK, map[string]any{"changed": changed, "link": link})
}

// handleContactUntag removes a manual tag from a contact, idempotent
// (DELETE /api/v1/contacts/{id}/tags/{tagId})。
func (s *Server) handleContactUntag(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	tagID := r.PathValue("tagId")
	changed, err := s.St.UntagContact(rec.TenantID, rec.ID, tagID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag remove failed")
		return
	}
	s.Log.Printf("contact untagged contact=%s tag=%s changed=%t by=%s", rec.ID, tagID, changed, c.Member.ID)
	writeJSON(w, http.StatusOK, map[string]any{"changed": changed})
}

// ---- segment query -------------------------------------------------------------

// contactTagSegmentParams maps the segment query params to tag families
// (值 = 逗号分隔的标签键/人工标签 id;组间 AND、组内 OR)。
var contactTagSegmentParams = map[string]string{
	"lifecycle": store.TagFamilyLifecycle,
	"activity":  store.TagFamilyActivity,
	"source":    store.TagFamilySource,
	"followup":  store.TagFamilyFollowUp,
	"manual":    store.TagFamilyManual,
}

// handleContactSegment answers the persona segment query: contacts matching
// EVERY group (AND across groups, OR within values). Results are bare
// contact_id references — 分群结果零 PII。
func (s *Server) handleContactSegment(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	q := store.SegmentQuery{TenantID: c.Member.TenantID, AsOf: time.Now().UTC()}
	scope := "tenant"
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		q.AssigneeMemberID = c.Member.ID // 记录级作用域:非 owner 只见自己人群
		scope = "assigned_to_me"
	}
	for param, family := range contactTagSegmentParams {
		raw := r.URL.Query().Get(param)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		values := []string{}
		for _, part := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(part); v != "" {
				values = append(values, v)
			}
		}
		if len(values) > 0 {
			q.Groups = append(q.Groups, store.SegmentGroup{Family: family, Values: values})
		}
	}
	if len(q.Groups) == 0 {
		fail(w, http.StatusBadRequest, "bad_request",
			"至少需要一个标签组(lifecycle/activity/source/followup/manual,逗号分隔多值)")
		return
	}
	ids, err := s.St.ContactSegment(q)
	if errors.Is(err, store.ErrSegmentBadGroup) {
		fail(w, http.StatusBadRequest, "bad_request",
			"标签组合含未知键或未知人工标签(派生键以 /api/v1/contact-tags/derived 披露为准)")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "segment query failed")
		return
	}
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]any{"contact_id": id})
	}
	// 日志只有租户/作用域/组数/总数:零标签名、零 contact_id 明细、零 PII。
	s.Log.Printf("contact segment tenant=%s scope=%s groups=%d total=%d by=%s",
		c.Member.TenantID, scope, len(q.Groups), len(ids), c.Member.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":        scope,
		"as_of":        q.AsOf.Format(time.RFC3339),
		"combine_rule": store.SegmentCombineRule,
		"groups":       q.Groups,
		"total":        len(ids),
		"items":        items,
		"definitions":  store.DerivedTagCatalog(),
	})
}
