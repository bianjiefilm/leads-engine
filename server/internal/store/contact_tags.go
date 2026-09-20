// HUI-1690 / FEAT-0191 客户画像标签体系·人工标签层存储(0010 additive 两新表):
//   - contact_tag_defs:租户自定义标签定义(名称租户内唯一 / 颜色可空 / 描述可空);
//     目录是租户级运营配置,写路径由 authz manage_contact_tags 单点裁决(HTTP 层),
//     库层 UNIQUE(tenant_id,name) 是并发兜底;
//   - contact_tag_links:打标关联。幂等唯一键 (tenant, tag, contact):重复打标
//     零新行、首戳不改写(applied_by/applied_at = 谁/何时,与 consent 撤销首戳、
//     跟进完结首戳同款纪律);去标物理删行,删前的留痕可经 ListContactTags 回查;
//   - 在用拒删:仍有联系人挂载时删除显式冲突(ErrTagDefInUse),绝不静默级联
//     (留痕不蒸发);墓碑联系人不可再打标(一切读取/写入 JOIN 掉软删行)。
//
// 派生标签(生命周期/活跃度/来源渠道/跟进状态)是纯只读按需重算,零迁移、
// 零落库,见 contact_tags_derived.go。每个查询都租户作用域并全参数化。
package store

import (
	"database/sql"
	"errors"
)

// 4xx 语义锚点(HTTP 层显式映射,绝不静默):
var (
	// ErrTagDefDuplicate: 标签名租户内唯一 -> 409。
	ErrTagDefDuplicate = errors.New("contact tags: tag name already exists in this tenant")
	// ErrTagDefInUse: 标签仍被联系人挂载,删除被拒 -> 409(留痕不蒸发)。
	ErrTagDefInUse = errors.New("contact tags: tag is still applied to contacts")
)

// ContactTagDef is one tenant-authored tag definition (人工标签目录项).
type ContactTagDef struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

const contactTagDefCols = `id,tenant_id,name,color,description,created_by,created_at,updated_at`

func scanTagDef(sc interface{ Scan(...any) error }) (ContactTagDef, error) {
	var d ContactTagDef
	err := sc.Scan(&d.ID, &d.TenantID, &d.Name, &d.Color, &d.Description, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func (s *Store) GetContactTagDef(id, tenantID string) (ContactTagDef, error) {
	return scanTagDef(s.DB.QueryRow(
		`SELECT `+contactTagDefCols+` FROM contact_tag_defs WHERE id=? AND tenant_id=?`, id, tenantID))
}

func (s *Store) ListContactTagDefs(tenantID string) ([]ContactTagDef, error) {
	rows, err := s.DB.Query(
		`SELECT `+contactTagDefCols+` FROM contact_tag_defs WHERE tenant_id=? ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactTagDef
	for rows.Next() {
		d, err := scanTagDef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CreateContactTagDef inserts one definition. 重名冲突显式返回 ErrTagDefDuplicate
// (预查给稳定语义,UNIQUE 约束做并发兜底)。
func (s *Store) CreateContactTagDef(tenantID, name, color, description, createdBy string) (ContactTagDef, error) {
	if _, err := s.GetContactTagDefByName(tenantID, name); err == nil {
		return ContactTagDef{}, ErrTagDefDuplicate
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ContactTagDef{}, err
	}
	d := ContactTagDef{
		ID: newID("ctd_"), TenantID: tenantID, Name: name, Color: color,
		Description: description, CreatedBy: createdBy,
	}
	d.CreatedAt, d.UpdatedAt = now(), now()
	if _, err := s.DB.Exec(
		`INSERT INTO contact_tag_defs(`+contactTagDefCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		d.ID, d.TenantID, d.Name, d.Color, d.Description, d.CreatedBy, d.CreatedAt, d.UpdatedAt); err != nil {
		if isUniqueViolation(err) {
			return ContactTagDef{}, ErrTagDefDuplicate
		}
		return ContactTagDef{}, err
	}
	return d, nil
}

// GetContactTagDefByName resolves one definition by its tenant-unique name.
func (s *Store) GetContactTagDefByName(tenantID, name string) (ContactTagDef, error) {
	return scanTagDef(s.DB.QueryRow(
		`SELECT `+contactTagDefCols+` FROM contact_tag_defs WHERE tenant_id=? AND name=?`, tenantID, name))
}

// ContactTagDefPatch carries optional field updates; nil leaves unchanged.
type ContactTagDefPatch struct {
	Name        *string
	Color       *string
	Description *string
}

// UpdateContactTagDef edits name/color/description. 改名撞既有名同样显式冲突。
func (s *Store) UpdateContactTagDef(id, tenantID string, p ContactTagDefPatch) (ContactTagDef, error) {
	cur, err := s.GetContactTagDef(id, tenantID)
	if err != nil {
		return ContactTagDef{}, err
	}
	if p.Name != nil && *p.Name != cur.Name {
		if _, err := s.GetContactTagDefByName(tenantID, *p.Name); err == nil {
			return ContactTagDef{}, ErrTagDefDuplicate
		} else if !errors.Is(err, sql.ErrNoRows) {
			return ContactTagDef{}, err
		}
		cur.Name = *p.Name
	}
	if p.Color != nil {
		cur.Color = *p.Color
	}
	if p.Description != nil {
		cur.Description = *p.Description
	}
	cur.UpdatedAt = now()
	if _, err := s.DB.Exec(
		`UPDATE contact_tag_defs SET name=?,color=?,description=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Name, cur.Color, cur.Description, cur.UpdatedAt, id, tenantID); err != nil {
		if isUniqueViolation(err) {
			return ContactTagDef{}, ErrTagDefDuplicate
		}
		return ContactTagDef{}, err
	}
	return cur, nil
}

// DeleteContactTagDef removes an UNUSED definition. 在用拒删:仍有联系人挂载时
// 显式冲突,绝不静默级联(打标留痕不蒸发)。
func (s *Store) DeleteContactTagDef(id, tenantID string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(
		`SELECT COUNT(1) FROM contact_tag_links WHERE tenant_id=? AND tag_id=?`, tenantID, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrTagDefInUse
	}
	res, err := tx.Exec(`DELETE FROM contact_tag_defs WHERE id=? AND tenant_id=?`, id, tenantID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// ContactTagLink is one tagging fact with its audit trail (谁/何时/哪标签).
// TagName/TagColor are JOIN-ed disclosure for回查; the link itself is the
// durable record until untagged.
type ContactTagLink struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	TagID     string `json:"tag_id"`
	TagName   string `json:"tag_name"`
	TagColor  string `json:"tag_color"`
	ContactID string `json:"contact_id"`
	AppliedBy string `json:"applied_by"`
	AppliedAt string `json:"applied_at"`
}

const contactTagLinkCols = `l.id,l.tenant_id,l.tag_id,d.name,d.color,l.contact_id,l.applied_by,l.applied_at`

func scanTagLink(sc interface{ Scan(...any) error }) (ContactTagLink, error) {
	var l ContactTagLink
	err := sc.Scan(&l.ID, &l.TenantID, &l.TagID, &l.TagName, &l.TagColor, &l.ContactID, &l.AppliedBy, &l.AppliedAt)
	return l, err
}

// TagContact applies one tag to one live contact. Idempotent: a replay keeps
// the FIRST stamp (changed=false, 零新行) — 与 consent 撤销首戳同款纪律.
// Unknown contact / tombstoned contact / unknown or cross-tenant tag all
// answer sql.ErrNoRows (fail-closed; HTTP 层映射 404/400).
func (s *Store) TagContact(tenantID, contactID, tagID, memberID string) (ContactTagLink, bool, error) {
	// 联系人必须在册且未墓碑(打标是运营动作,不作用于已删档案)。
	if _, err := s.GetContact(contactID, tenantID); err != nil {
		return ContactTagLink{}, false, err
	}
	def, err := s.GetContactTagDef(tagID, tenantID)
	if err != nil {
		return ContactTagLink{}, false, err
	}
	l := ContactTagLink{
		ID: newID("ctl_"), TenantID: tenantID, TagID: tagID, TagName: def.Name,
		TagColor: def.Color, ContactID: contactID, AppliedBy: memberID, AppliedAt: now(),
	}
	if _, err := s.DB.Exec(
		`INSERT INTO contact_tag_links(id,tenant_id,tag_id,contact_id,applied_by,applied_at)
		 VALUES(?,?,?,?,?,?)`,
		l.ID, l.TenantID, l.TagID, l.ContactID, l.AppliedBy, l.AppliedAt); err != nil {
		if !isUniqueViolation(err) {
			return ContactTagLink{}, false, err
		}
		// 幂等重放:回读既有行,首戳原样返回(零新行、零改写)。
		cur, err := s.GetContactTagLink(tenantID, contactID, tagID)
		if err != nil {
			return ContactTagLink{}, false, err
		}
		return cur, false, nil
	}
	return l, true, nil
}

// GetContactTagLink fetches one link by its idempotent key.
func (s *Store) GetContactTagLink(tenantID, contactID, tagID string) (ContactTagLink, error) {
	return scanTagLink(s.DB.QueryRow(
		`SELECT `+contactTagLinkCols+` FROM contact_tag_links l
		 JOIN contact_tag_defs d ON d.id = l.tag_id
		 WHERE l.tenant_id=? AND l.contact_id=? AND l.tag_id=?`, tenantID, contactID, tagID))
}

// UntagContact removes the link. Idempotent: removing an absent link is a
// no-op (changed=false). 去标是显式运营动作:物理删行,删前留痕已在账。
func (s *Store) UntagContact(tenantID, contactID, tagID string) (bool, error) {
	res, err := s.DB.Exec(
		`DELETE FROM contact_tag_links WHERE tenant_id=? AND contact_id=? AND tag_id=?`,
		tenantID, contactID, tagID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListContactTags answers the contact's tagging audit trail (谁/何时/哪标签),
// oldest first — 可回查是打标留痕的硬要求。
func (s *Store) ListContactTags(tenantID, contactID string) ([]ContactTagLink, error) {
	rows, err := s.DB.Query(
		`SELECT `+contactTagLinkCols+` FROM contact_tag_links l
		 JOIN contact_tag_defs d ON d.id = l.tag_id
		 WHERE l.tenant_id=? AND l.contact_id=? ORDER BY l.applied_at, l.id`, tenantID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactTagLink
	for rows.Next() {
		l, err := scanTagLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
