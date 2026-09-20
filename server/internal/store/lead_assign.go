// HUI-1685 / FEAT-0186 线索自动分配域存储层(lead_assign_pool,additive 新表):
//
//   - 策略 = 确定性平滑加权轮询(nginx smooth WRR):同权重退化为纯轮询;
//     权重比例在任意整周期窗口内精确成立(如 1:2 → 任意连续 3 次里重权者
//     恰好 2 次),无突发连发超配;
//   - 分配池 = 本租户在册在职、role 含销售语义(role='sales',复用既有角色
//     模型,不发明新角色)的成员;池条目是配置,有效池在分配时刻 JOIN members
//     现算 —— 成员被停用/改角色后动态退出轮询,配置行保留;
//   - 地域/行业精确匹配优先:线索提供的每个非空维度(region/industry)都必须
//     与池条目的对应标签完全相等;无匹配(或线索无标签)→ 全池平滑加权轮询;
//   - 只分配未分配线索:AssignLeadInTx 只被 intake 首投调用(重放在幂等键
//     查询处短路,绝不走到这里),且在写入前线索必然无指派(建档与分配同
//     事务);人工改派走普通更新路径,永不被本域覆盖;
//   - 跨租户严格隔离:一切查询带 tenant_id,池成员必须归属同租户;
//   - 池空不阻塞建档:无有效池时 AssignLeadInTx 返回空指派、零错误。
//
// 每个查询都租户作用域并全参数化,沿用本包既有纪律。
package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Weight domain and tag caps (mirrored by the migration CHECK domain).
const (
	AssignWeightMin   = 1
	AssignWeightMax   = 1000
	AssignTagMaxRunes = 64
)

// assignSalesRole is the only role with sales semantics in the existing role
// model; pool members must carry it (owner/agent never auto-receive leads).
const assignSalesRole = "sales"

// Domain errors surfaced to the HTTP layer for explicit status mapping.
var (
	ErrAssignPoolDuplicate = errors.New("assign pool: entry already exists for this member")
	ErrAssignPoolNotFound  = errors.New("assign pool: entry not found")
)

// AssignPoolEntry is one tenant's pool configuration row (per member).
type AssignPoolEntry struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	MemberID string `json:"member_id"`
	Weight   int    `json:"weight"`
	// Region/Industry are optional exact-match tags ("" = 无此维度约束).
	Region    string `json:"region,omitempty"`
	Industry  string `json:"industry,omitempty"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const assignPoolCols = `id,tenant_id,member_id,weight,region,industry,created_by,created_at,updated_at`

func scanAssignPoolEntry(sc interface{ Scan(...any) error }) (AssignPoolEntry, error) {
	var e AssignPoolEntry
	err := sc.Scan(&e.ID, &e.TenantID, &e.MemberID, &e.Weight, &e.Region, &e.Industry,
		&e.CreatedBy, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

// assignTagProblem validates one optional tag (trim + rune cap); returns the
// field name in the error so 400s name the offending dimension.
func assignTagProblem(field, v string) string {
	if runeLen64(v) > AssignTagMaxRunes {
		return "assign pool: " + field + " must be at most 64 characters"
	}
	return ""
}

// assignWeightProblem validates the weight domain.
func assignWeightProblem(weight int) string {
	if weight < AssignWeightMin || weight > AssignWeightMax {
		return "assign pool: weight must be between 1 and 1000"
	}
	return ""
}

// assignableMemberProblem checks the member is in THIS tenant, live, enabled
// and carries the sales role. Server-side single point of judgment: the HTTP
// layer only maps the message.
func (s *Store) assignableMemberProblem(tenantID, memberID string) string {
	m, err := s.GetMember(memberID)
	if err != nil {
		return "assign pool: member_id does not exist in this tenant"
	}
	if m.TenantID != tenantID {
		return "assign pool: member_id does not exist in this tenant"
	}
	if m.Role != assignSalesRole {
		return "assign pool: member must have the sales role"
	}
	if !m.Enabled {
		return "assign pool: member must be enabled (在册在职)"
	}
	return ""
}

// CreateAssignPoolEntry adds one member to the tenant's pool (one row per
// member; duplicates answer ErrAssignPoolDuplicate).
func (s *Store) CreateAssignPoolEntry(tenantID, memberID string, weight int, region, industry, createdBy string) (AssignPoolEntry, error) {
	region, industry = strings.TrimSpace(region), strings.TrimSpace(industry)
	if msg := assignWeightProblem(weight); msg != "" {
		return AssignPoolEntry{}, errors.New(msg)
	}
	if msg := assignTagProblem("region", region); msg != "" {
		return AssignPoolEntry{}, errors.New(msg)
	}
	if msg := assignTagProblem("industry", industry); msg != "" {
		return AssignPoolEntry{}, errors.New(msg)
	}
	if msg := s.assignableMemberProblem(tenantID, memberID); msg != "" {
		return AssignPoolEntry{}, errors.New(msg)
	}
	e := AssignPoolEntry{
		ID: newID("pap_"), TenantID: tenantID, MemberID: memberID,
		Weight: weight, Region: region, Industry: industry,
		CreatedBy: createdBy,
	}
	e.CreatedAt, e.UpdatedAt = now(), now()
	_, err := s.DB.Exec(
		`INSERT INTO lead_assign_pool(`+assignPoolCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		e.ID, e.TenantID, e.MemberID, e.Weight, e.Region, e.Industry, e.CreatedBy, e.CreatedAt, e.UpdatedAt)
	if err != nil && isUniqueViolation(err) {
		return AssignPoolEntry{}, ErrAssignPoolDuplicate
	}
	return e, err
}

// UpdateAssignPoolEntry edits weight and the optional tags. Nil pointers leave
// the field unchanged; an explicit "" clears a tag (absent vs empty discipline).
func (s *Store) UpdateAssignPoolEntry(id, tenantID string, weight *int, region, industry *string) (AssignPoolEntry, error) {
	row := s.DB.QueryRow(
		`SELECT `+assignPoolCols+` FROM lead_assign_pool WHERE id=? AND tenant_id=?`, id, tenantID)
	cur, err := scanAssignPoolEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AssignPoolEntry{}, ErrAssignPoolNotFound
	}
	if err != nil {
		return AssignPoolEntry{}, err
	}
	if weight != nil {
		if msg := assignWeightProblem(*weight); msg != "" {
			return AssignPoolEntry{}, errors.New(msg)
		}
		cur.Weight = *weight
	}
	if region != nil {
		v := strings.TrimSpace(*region)
		if msg := assignTagProblem("region", v); msg != "" {
			return AssignPoolEntry{}, errors.New(msg)
		}
		cur.Region = v
	}
	if industry != nil {
		v := strings.TrimSpace(*industry)
		if msg := assignTagProblem("industry", v); msg != "" {
			return AssignPoolEntry{}, errors.New(msg)
		}
		cur.Industry = v
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE lead_assign_pool SET weight=?,region=?,industry=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Weight, cur.Region, cur.Industry, cur.UpdatedAt, cur.ID, tenantID)
	return cur, err
}

// DeleteAssignPoolEntry removes one entry within the tenant.
func (s *Store) DeleteAssignPoolEntry(id, tenantID string) error {
	res, err := s.DB.Exec(`DELETE FROM lead_assign_pool WHERE id=? AND tenant_id=?`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAssignPoolNotFound
	}
	return nil
}

// ListAssignPoolEntries answers the tenant's pool config, stable order.
func (s *Store) ListAssignPoolEntries(tenantID string) ([]AssignPoolEntry, error) {
	rows, err := s.DB.Query(
		`SELECT `+assignPoolCols+` FROM lead_assign_pool WHERE tenant_id=? ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssignPoolEntry
	for rows.Next() {
		e, err := scanAssignPoolEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- the pick ------------------------------------------------------------------

// assignPoolRow is the live pick-time shape: config plus rotation cursor.
type assignPoolRow struct {
	ID       string
	MemberID string
	Weight   int
	Current  int
	Region   string
	Industry string
}

// loadEffectiveAssignPool returns the ENABLED sales-tagged entries of the
// tenant in deterministic order (created_at, id). 停用/改角色成员在此动态退出。
func loadEffectiveAssignPool(tx *sql.Tx, tenantID string) ([]assignPoolRow, error) {
	rows, err := tx.Query(
		`SELECT p.id,p.member_id,p.weight,p.current_weight,p.region,p.industry
		 FROM lead_assign_pool p
		 JOIN members m ON m.id = p.member_id
		 WHERE p.tenant_id=? AND m.enabled=1 AND m.role='sales'
		 ORDER BY p.created_at, p.id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assignPoolRow
	for rows.Next() {
		var e assignPoolRow
		if err := rows.Scan(&e.ID, &e.MemberID, &e.Weight, &e.Current, &e.Region, &e.Industry); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// poolTagMatches reports exact-match priority: every non-empty dimension the
// lead carries must equal the entry's tag; entries only serve leads whose
// provided tags they carry. 空标签条目只接无标签线索(该维度)。
func poolTagMatches(e assignPoolRow, region, industry string) bool {
	if region != "" && e.Region != region {
		return false
	}
	if industry != "" && e.Industry != industry {
		return false
	}
	return true
}

// advanceSmoothWRR advances nginx smooth weighted round-robin over entries
// (in place) and returns the chosen index. Equal weights degenerate to pure
// round robin; total weight >= 1 is guaranteed by the weight CHECK domain.
// Strict ">" keeps the FIRST maximum on ties, so the sequence is deterministic
// given the (created_at, id) order.
func advanceSmoothWRR(entries []assignPoolRow) int {
	total := 0
	best := 0
	for i := range entries {
		entries[i].Current += entries[i].Weight
		total += entries[i].Weight
		if entries[i].Current > entries[best].Current {
			best = i
		}
	}
	entries[best].Current -= total
	return best
}

// AssignLeadInTx routes one unassigned lead to a pool member inside the
// CALLER's transaction (intake 首投唯一调用点:重放在幂等键处短路,人工改派
// 在普通更新路径,二者永不经过这里)。Returns the assigned member id, or ""
// when the pool is empty (池空不阻塞建档) or no member was selected.
//
// 地域/行业精确匹配优先:matching subset first;无匹配 → 全池。轮询游标
// (current_weight)与线索指派在同一事务内推进,崩溃安全。
func AssignLeadInTx(tx *sql.Tx, tenantID, leadID, region, industry string) (string, error) {
	entries, err := loadEffectiveAssignPool(tx, tenantID)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	set := entries
	if region != "" || industry != "" {
		matched := make([]assignPoolRow, 0, len(entries))
		for _, e := range entries {
			if poolTagMatches(e, region, industry) {
				matched = append(matched, e)
			}
		}
		if len(matched) > 0 {
			set = matched
		}
		// 无匹配:保持全池(确定性回退)。
	}
	best := advanceSmoothWRR(set)
	// 推进所选子集的游标(内部状态,不碰 updated_at)。
	for _, e := range set {
		if _, err := tx.Exec(
			`UPDATE lead_assign_pool SET current_weight=? WHERE id=? AND tenant_id=?`,
			e.Current, e.ID, tenantID); err != nil {
			return "", err
		}
	}
	pick := set[best].MemberID
	ts := now()
	if _, err := tx.Exec(
		`UPDATE leads SET assigned_member_id=?,updated_at=? WHERE id=? AND tenant_id=?`,
		pick, ts, leadID, tenantID); err != nil {
		return "", err
	}
	return pick, nil
}
