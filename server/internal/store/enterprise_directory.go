// HUI-1678 企业资料筛选。
//
// 本进程不连接官方企业公开库，也不保存一份供演示填满的共享企业目录。
// 唯一入口是客户声明有权再利用的导入。企业行业/地区/规模与自然人联系方式
// 分开。确认入库走 IntakeLeadInTx（HUI-1680 inbox 与 HUI-1683 的同一接缝），
// 不写 Notify 收件箱，不授予营销同意，不调用外呼/短信/SOP。
// 拒绝或删除写入独立抑制表；之后的刷新不能把抑制清掉，也不能把营销重新打开。
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	EnterpriseSourceApp     = "customer_authorized_import"
	EnterpriseIntakeApp     = "leads-engine"
	EnterpriseIntakeNS      = "enterprise_import"
	EnterpriseAllowedUse    = "enterprise_profile"
	enterpriseImportMaxRows = 50
)

var (
	ErrEnterpriseNotFound   = errors.New("enterprise record not found")
	ErrEnterpriseSuppressed = errors.New("enterprise suppression held")
)

// EnterpriseBadInput is a caller mistake (missing license, bad key, person used as a screen).
type EnterpriseBadInput struct{ Msg string }

func (e *EnterpriseBadInput) Error() string { return e.Msg }

// EnterpriseRecordIn is one company row inside a customer-authorized import.
// Person fields are stored beside the company and are not a marketing grant.
type EnterpriseRecordIn struct {
	EnterpriseID   string
	EnterpriseName string
	Industry       string
	Region         string
	Scale          string
	PersonName     string
	PersonPhone    string
	PersonEmail    string
}

// EnterpriseImportIn is one authorized batch. CollectedAt is RFC3339.
type EnterpriseImportIn struct {
	SourceKey       string
	SourceName      string
	CollectedAt     string
	UpdateCycleDays int
	License         string
	Correction      string
	Records         []EnterpriseRecordIn
}

// EnterpriseImportResult reports which rows were inserted versus updated.
type EnterpriseImportResult struct {
	ImportID  string
	SourceKey string
	Inserted  int
	Records   []EnterpriseRecordRef
}

// EnterpriseRecordRef is the stable id returned to the caller.
type EnterpriseRecordRef struct {
	ID           string `json:"id"`
	EnterpriseID string `json:"enterprise_id"`
	Status       string `json:"status"`
}

// EnterprisePerson is contact-of-a-human, never implied consent.
type EnterprisePerson struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Email string `json:"email"`
}

// EnterpriseFieldSource says where one displayed fact came from.
type EnterpriseFieldSource struct {
	Value       string `json:"value"`
	Source      string `json:"source"`
	CollectedAt string `json:"collected_at"`
	Missing     bool   `json:"missing"`
}

// EnterpriseView is the screening/preview row. MarketingConsent is always false.
type EnterpriseView struct {
	ID               string                           `json:"id"`
	EnterpriseID     string                           `json:"enterprise_id"`
	EnterpriseName   string                           `json:"enterprise_name"`
	Industry         string                           `json:"industry"`
	Region           string                           `json:"region"`
	Scale            string                           `json:"scale"`
	Person           EnterprisePerson                 `json:"person"`
	MissingFields    []string                         `json:"missing_fields"`
	FieldSources     map[string]EnterpriseFieldSource `json:"field_sources"`
	AllowedUses      []string                         `json:"allowed_uses"`
	MarketingConsent bool                             `json:"marketing_consent"`
	Freshness        string                           `json:"freshness"`
	Conflict         bool                             `json:"conflict"`
	Status           string                           `json:"status"`
	CollectedAt      string                           `json:"collected_at"`
	SourceName       string                           `json:"source_name"`
	SourceKey        string                           `json:"source_key"`
	UpdateCycleDays  int                              `json:"update_cycle_days"`
	License          string                           `json:"license"`
	Correction       string                           `json:"correction"`
}

// EnterpriseConfirmResult is the intake outcome for one confirmed company.
type EnterpriseConfirmResult struct {
	LeadID           string   `json:"lead_id"`
	ContactID        string   `json:"contact_id"`
	Class            string   `json:"class"`
	Duplicate        bool     `json:"duplicate"`
	MarketingConsent bool     `json:"marketing_consent"`
	SourceApp        string   `json:"source_app"`
	AllowedUses      []string `json:"allowed_uses"`
}

type enterpriseRow struct {
	EnterpriseView
	importID     string
	contentSHA   string
	confirmedSHA string
	contactID    string
	leadID       string
}

// UpsertEnterpriseImport stores or refreshes a customer-authorized batch inside one tenant.
// A suppression for the same source key and enterprise id is never cleared by a refresh.
func (s *Store) UpsertEnterpriseImport(tenantID, memberID string, in EnterpriseImportIn) (EnterpriseImportResult, error) {
	if err := validateEnterpriseImport(in); err != nil {
		return EnterpriseImportResult{}, err
	}
	collected, err := time.Parse(time.RFC3339, strings.TrimSpace(in.CollectedAt))
	if err != nil {
		return EnterpriseImportResult{}, &EnterpriseBadInput{Msg: "collected_at must be RFC3339"}
	}
	collectedAt := collected.UTC().Format(time.RFC3339)

	tx, err := s.DB.Begin()
	if err != nil {
		return EnterpriseImportResult{}, err
	}
	defer tx.Rollback()

	importID, err := upsertEnterpriseImportTx(tx, tenantID, memberID, in, collectedAt)
	if err != nil {
		return EnterpriseImportResult{}, err
	}
	out := EnterpriseImportResult{ImportID: importID, SourceKey: in.SourceKey}
	seen := map[string]bool{}
	for _, rec := range in.Records {
		if seen[rec.EnterpriseID] {
			return EnterpriseImportResult{}, &EnterpriseBadInput{Msg: "duplicate enterprise_id in one import"}
		}
		seen[rec.EnterpriseID] = true
		ref, inserted, err := upsertEnterpriseRecordTx(tx, tenantID, importID, in.SourceKey, rec)
		if err != nil {
			return EnterpriseImportResult{}, err
		}
		if inserted {
			out.Inserted++
		}
		out.Records = append(out.Records, ref)
	}
	if err := tx.Commit(); err != nil {
		return EnterpriseImportResult{}, err
	}
	return out, nil
}

func validateEnterpriseImport(in EnterpriseImportIn) error {
	if !validKeyToken(in.SourceKey, 40) {
		return &EnterpriseBadInput{Msg: "source_key must be a short ASCII identifier"}
	}
	if strings.TrimSpace(in.SourceName) == "" || utf8.RuneCountInString(in.SourceName) > 100 {
		return &EnterpriseBadInput{Msg: "source_name is required (max 100)"}
	}
	if in.UpdateCycleDays < 1 || in.UpdateCycleDays > 3650 {
		return &EnterpriseBadInput{Msg: "update_cycle_days must be between 1 and 3650"}
	}
	if strings.TrimSpace(in.License) == "" || utf8.RuneCountInString(in.License) > 500 {
		return &EnterpriseBadInput{Msg: "license is required (the reuse permission, max 500)"}
	}
	if strings.TrimSpace(in.Correction) == "" || utf8.RuneCountInString(in.Correction) > 500 {
		return &EnterpriseBadInput{Msg: "correction is required (how to delete or correct, max 500)"}
	}
	if len(in.Records) == 0 || len(in.Records) > enterpriseImportMaxRows {
		return &EnterpriseBadInput{Msg: "records must contain 1 to 50 enterprises"}
	}
	for _, rec := range in.Records {
		if !validKeyToken(rec.EnterpriseID, 64) {
			return &EnterpriseBadInput{Msg: "enterprise_id must be a short ASCII identifier"}
		}
		if strings.TrimSpace(rec.EnterpriseName) == "" || utf8.RuneCountInString(rec.EnterpriseName) > 100 {
			return &EnterpriseBadInput{Msg: "enterprise_name is required (max 100)"}
		}
		if utf8.RuneCountInString(rec.Industry) > 64 || utf8.RuneCountInString(rec.Region) > 64 || utf8.RuneCountInString(rec.Scale) > 64 {
			return &EnterpriseBadInput{Msg: "industry/region/scale exceed 64"}
		}
		if utf8.RuneCountInString(rec.PersonName) > 100 || utf8.RuneCountInString(rec.PersonEmail) > 200 || utf8.RuneCountInString(rec.PersonPhone) > 32 {
			return &EnterpriseBadInput{Msg: "person fields exceed their length caps"}
		}
	}
	return nil
}

func upsertEnterpriseImportTx(tx *sql.Tx, tenantID, memberID string, in EnterpriseImportIn, collectedAt string) (string, error) {
	id := newID("eimp_")
	_, err := tx.Exec(`INSERT INTO enterprise_imports(
		id,tenant_id,source_key,source_name,collected_at,update_cycle_days,license,correction,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id, source_key) DO UPDATE SET
		  source_name=excluded.source_name,
		  collected_at=excluded.collected_at,
		  update_cycle_days=excluded.update_cycle_days,
		  license=excluded.license,
		  correction=excluded.correction`,
		id, tenantID, in.SourceKey, strings.TrimSpace(in.SourceName), collectedAt, in.UpdateCycleDays,
		strings.TrimSpace(in.License), strings.TrimSpace(in.Correction), memberID, now())
	if err != nil {
		return "", err
	}
	var kept string
	err = tx.QueryRow(`SELECT id FROM enterprise_imports WHERE tenant_id=? AND source_key=?`, tenantID, in.SourceKey).Scan(&kept)
	return kept, err
}

func upsertEnterpriseRecordTx(tx *sql.Tx, tenantID, importID, sourceKey string, rec EnterpriseRecordIn) (EnterpriseRecordRef, bool, error) {
	name := strings.TrimSpace(rec.EnterpriseName)
	industry := strings.TrimSpace(rec.Industry)
	region := strings.TrimSpace(rec.Region)
	scale := strings.TrimSpace(rec.Scale)
	sha := enterpriseContentSHA(rec.EnterpriseID, name, industry, region, scale)
	reason, suppressed, err := suppressionReasonTx(tx, tenantID, sourceKey, rec.EnterpriseID)
	if err != nil {
		return EnterpriseRecordRef{}, false, err
	}

	var id, oldSHA, status string
	var conflict int
	err = tx.QueryRow(`SELECT id,content_sha,status,conflict FROM enterprise_records
		WHERE tenant_id=? AND source_key=? AND enterprise_id=?`, tenantID, sourceKey, rec.EnterpriseID).
		Scan(&id, &oldSHA, &status, &conflict)
	if errors.Is(err, sql.ErrNoRows) {
		id = newID("erec_")
		status = "draft"
		if suppressed {
			status = statusForSuppression(reason)
		}
		_, err = tx.Exec(`INSERT INTO enterprise_records(
			id,tenant_id,import_id,source_key,enterprise_id,enterprise_name,industry,region,scale,
			person_name,person_phone,person_email,content_sha,confirmed_sha,conflict,status,lead_id,contact_id,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'',0,?,NULL,NULL,?,?)`,
			id, tenantID, importID, sourceKey, rec.EnterpriseID, name, industry, region, scale,
			strings.TrimSpace(rec.PersonName), strings.TrimSpace(rec.PersonPhone), strings.TrimSpace(rec.PersonEmail),
			sha, status, now(), now())
		if err != nil {
			return EnterpriseRecordRef{}, false, err
		}
		return EnterpriseRecordRef{ID: id, EnterpriseID: rec.EnterpriseID, Status: status}, true, nil
	}
	if err != nil {
		return EnterpriseRecordRef{}, false, err
	}
	if oldSHA != sha {
		conflict = 1
	}
	if suppressed {
		status = statusForSuppression(reason)
	}
	_, err = tx.Exec(`UPDATE enterprise_records SET
		import_id=?, enterprise_name=?, industry=?, region=?, scale=?,
		person_name=?, person_phone=?, person_email=?, content_sha=?, conflict=?, status=?, updated_at=?
		WHERE id=? AND tenant_id=?`,
		importID, name, industry, region, scale,
		strings.TrimSpace(rec.PersonName), strings.TrimSpace(rec.PersonPhone), strings.TrimSpace(rec.PersonEmail),
		sha, conflict, status, now(), id, tenantID)
	if err != nil {
		return EnterpriseRecordRef{}, false, err
	}
	return EnterpriseRecordRef{ID: id, EnterpriseID: rec.EnterpriseID, Status: status}, false, nil
}

func statusForSuppression(reason string) string {
	if reason == "deleted" {
		return "deleted"
	}
	return "refused"
}

func suppressionReasonTx(tx *sql.Tx, tenantID, sourceKey, enterpriseID string) (string, bool, error) {
	var reason string
	err := tx.QueryRow(`SELECT reason FROM enterprise_suppressions WHERE tenant_id=? AND source_key=? AND enterprise_id=?`,
		tenantID, sourceKey, enterpriseID).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return reason, true, nil
}

// ListEnterpriseRecords screens company dimensions only. industry/region/scale
// empty means "no constraint". Person facts are never a filter.
func (s *Store) ListEnterpriseRecords(tenantID, industry, region, scale string) ([]EnterpriseView, error) {
	rows, err := s.DB.Query(`SELECT `+enterpriseViewCols+`
		FROM enterprise_records r
		JOIN enterprise_imports i ON i.id=r.import_id AND i.tenant_id=r.tenant_id
		WHERE r.tenant_id=?
		  AND (?='' OR r.industry=?)
		  AND (?='' OR r.region=?)
		  AND (?='' OR r.scale=?)
		ORDER BY r.created_at, r.enterprise_id`,
		tenantID, industry, industry, region, region, scale, scale)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnterpriseView
	for rows.Next() {
		row, err := scanEnterpriseRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.EnterpriseView)
	}
	return out, rows.Err()
}

// GetEnterpriseRecord loads one row in the tenant, or ErrEnterpriseNotFound.
func (s *Store) GetEnterpriseRecord(tenantID, id string) (EnterpriseView, error) {
	row := s.DB.QueryRow(`SELECT `+enterpriseViewCols+`
		FROM enterprise_records r
		JOIN enterprise_imports i ON i.id=r.import_id AND i.tenant_id=r.tenant_id
		WHERE r.tenant_id=? AND r.id=?`, tenantID, id)
	got, err := scanEnterpriseRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EnterpriseView{}, ErrEnterpriseNotFound
	}
	if err != nil {
		return EnterpriseView{}, err
	}
	return got.EnterpriseView, nil
}

// ConfirmEnterpriseRecord puts the company into this tenant's candidate pool
// through IntakeLeadInTx. Person phone/email are not copied onto the contact.
// Marketing consent is not written. A held suppression refuses the confirm.
func (s *Store) ConfirmEnterpriseRecord(tenantID, id, memberID, pepper string, filterOn, assignOn bool) (EnterpriseConfirmResult, error) {
	if strings.TrimSpace(pepper) == "" {
		return EnterpriseConfirmResult{}, errors.New("enterprise confirm: pepper required")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return EnterpriseConfirmResult{}, err
	}
	defer tx.Rollback()

	row, err := loadEnterpriseRowTx(tx, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EnterpriseConfirmResult{}, ErrEnterpriseNotFound
	}
	if err != nil {
		return EnterpriseConfirmResult{}, err
	}
	if _, held, err := suppressionReasonTx(tx, tenantID, row.SourceKey, row.EnterpriseID); err != nil {
		return EnterpriseConfirmResult{}, err
	} else if held || row.Status == "refused" || row.Status == "deleted" {
		return EnterpriseConfirmResult{}, ErrEnterpriseSuppressed
	}

	content := enterpriseContentBytes(row.EnterpriseID, row.EnterpriseName, row.Industry, row.Region, row.Scale)
	snapshot, err := json.Marshal(map[string]any{
		"accepted_source":   EnterpriseSourceApp,
		"license":           row.License,
		"collected_at":      row.CollectedAt,
		"update_cycle_days": row.UpdateCycleDays,
		"correction":        row.Correction,
		"allowed_uses":      []string{EnterpriseAllowedUse},
		"marketing_implied": false,
		"freshness":         row.Freshness,
		"conflict":          row.Conflict,
	})
	if err != nil {
		return EnterpriseConfirmResult{}, err
	}
	sourceRefID, err := ensureSourceRefTx(tx, tenantID, EnterpriseSourceApp, row.SourceKey+"/"+row.EnterpriseID, string(snapshot), memberID)
	if err != nil {
		return EnterpriseConfirmResult{}, err
	}
	eventID := "ent:" + row.SourceKey + ":" + row.EnterpriseID
	intake, err := IntakeLeadInTx(tx, IntakeInput{
		TenantID: tenantID, SourceApp: EnterpriseIntakeApp, SourceNS: EnterpriseIntakeNS,
		EventID: eventID, Content: content,
		ContactName: row.EnterpriseName, BusinessCategory: "merchant_customer",
		SourceType: "manual", SourceRefID: sourceRefID, Pepper: pepper,
		FilterEnabled: filterOn, AssignEnabled: assignOn,
		Region: row.Region, Industry: row.Industry,
	})
	if errors.Is(err, ErrEventContentConflict) {
		if _, uerr := tx.Exec(`UPDATE enterprise_records SET conflict=1, updated_at=? WHERE id=? AND tenant_id=?`, now(), id, tenantID); uerr != nil {
			return EnterpriseConfirmResult{}, uerr
		}
		if cerr := tx.Commit(); cerr != nil {
			return EnterpriseConfirmResult{}, cerr
		}
		return EnterpriseConfirmResult{}, ErrEventContentConflict
	}
	if err != nil {
		return EnterpriseConfirmResult{}, err
	}
	sha := enterpriseContentSHA(row.EnterpriseID, row.EnterpriseName, row.Industry, row.Region, row.Scale)
	if _, err := tx.Exec(`UPDATE enterprise_records SET status='confirmed', lead_id=?, contact_id=?, confirmed_sha=?, updated_at=?
		WHERE id=? AND tenant_id=?`, intake.LeadID, intake.ContactID, sha, now(), id, tenantID); err != nil {
		return EnterpriseConfirmResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return EnterpriseConfirmResult{}, err
	}
	return EnterpriseConfirmResult{
		LeadID: intake.LeadID, ContactID: intake.ContactID, Class: intake.Class, Duplicate: intake.Duplicate,
		MarketingConsent: false, SourceApp: EnterpriseSourceApp, AllowedUses: []string{EnterpriseAllowedUse},
	}, nil
}

// SuppressEnterpriseRecord records an independent refusal or deletion.
// A later import of the same enterprise cannot clear it. Existing marketing
// consents on the linked contact are revoked; none are created here.
func (s *Store) SuppressEnterpriseRecord(tenantID, id, memberID, reason string) error {
	if reason != "refused" && reason != "deleted" {
		return &EnterpriseBadInput{Msg: "suppression reason must be refused or deleted"}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadEnterpriseRowTx(tx, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEnterpriseNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO enterprise_suppressions(id,tenant_id,source_key,enterprise_id,reason,created_by,created_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id, source_key, enterprise_id) DO NOTHING`,
		newID("esup_"), tenantID, row.SourceKey, row.EnterpriseID, reason, memberID, now()); err != nil {
		return err
	}
	status := statusForSuppression(reason)
	if existing, held, err := suppressionReasonTx(tx, tenantID, row.SourceKey, row.EnterpriseID); err != nil {
		return err
	} else if held {
		status = statusForSuppression(existing)
	}
	if _, err := tx.Exec(`UPDATE enterprise_records SET status=?, updated_at=? WHERE id=? AND tenant_id=?`,
		status, now(), id, tenantID); err != nil {
		return err
	}
	if row.contactID != "" {
		if err := RevokeMarketingTx(tx, tenantID, row.contactID, "enterprise_"+reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func loadEnterpriseRowTx(tx *sql.Tx, tenantID, id string) (enterpriseRow, error) {
	row := tx.QueryRow(`SELECT `+enterpriseViewCols+`, r.import_id, r.content_sha, r.confirmed_sha, IFNULL(r.contact_id,''), IFNULL(r.lead_id,'')
		FROM enterprise_records r
		JOIN enterprise_imports i ON i.id=r.import_id AND i.tenant_id=r.tenant_id
		WHERE r.tenant_id=? AND r.id=?`, tenantID, id)
	return scanEnterpriseRowFull(row)
}

const enterpriseViewCols = `r.id, r.enterprise_id, r.enterprise_name, r.industry, r.region, r.scale,
	r.person_name, r.person_phone, r.person_email, r.conflict, r.status,
	i.collected_at, i.source_name, i.source_key, i.update_cycle_days, i.license, i.correction`

func scanEnterpriseRow(sc interface{ Scan(...any) error }) (enterpriseRow, error) {
	var row enterpriseRow
	var conflict int
	err := sc.Scan(&row.ID, &row.EnterpriseID, &row.EnterpriseName, &row.Industry, &row.Region, &row.Scale,
		&row.Person.Name, &row.Person.Phone, &row.Person.Email, &conflict, &row.Status,
		&row.CollectedAt, &row.SourceName, &row.SourceKey, &row.UpdateCycleDays, &row.License, &row.Correction)
	if err != nil {
		return enterpriseRow{}, err
	}
	finishEnterpriseView(&row, conflict)
	return row, nil
}

func scanEnterpriseRowFull(sc interface{ Scan(...any) error }) (enterpriseRow, error) {
	var row enterpriseRow
	var conflict int
	err := sc.Scan(&row.ID, &row.EnterpriseID, &row.EnterpriseName, &row.Industry, &row.Region, &row.Scale,
		&row.Person.Name, &row.Person.Phone, &row.Person.Email, &conflict, &row.Status,
		&row.CollectedAt, &row.SourceName, &row.SourceKey, &row.UpdateCycleDays, &row.License, &row.Correction,
		&row.importID, &row.contentSHA, &row.confirmedSHA, &row.contactID, &row.leadID)
	if err != nil {
		return enterpriseRow{}, err
	}
	finishEnterpriseView(&row, conflict)
	return row, nil
}

func finishEnterpriseView(row *enterpriseRow, conflict int) {
	row.Conflict = conflict == 1
	row.MarketingConsent = false
	row.AllowedUses = []string{EnterpriseAllowedUse}
	row.MissingFields = missingEnterpriseFields(row.Industry, row.Region, row.Scale)
	row.Freshness = enterpriseFreshness(row.CollectedAt, row.UpdateCycleDays, time.Now().UTC())
	row.FieldSources = map[string]EnterpriseFieldSource{
		"enterprise_id":   {Value: row.EnterpriseID, Source: row.SourceName, CollectedAt: row.CollectedAt, Missing: false},
		"enterprise_name": {Value: row.EnterpriseName, Source: row.SourceName, CollectedAt: row.CollectedAt, Missing: false},
		"industry":        {Value: row.Industry, Source: row.SourceName, CollectedAt: row.CollectedAt, Missing: row.Industry == ""},
		"region":          {Value: row.Region, Source: row.SourceName, CollectedAt: row.CollectedAt, Missing: row.Region == ""},
		"scale":           {Value: row.Scale, Source: row.SourceName, CollectedAt: row.CollectedAt, Missing: row.Scale == ""},
	}
}

func missingEnterpriseFields(industry, region, scale string) []string {
	var out []string
	if industry == "" {
		out = append(out, "industry")
	}
	if region == "" {
		out = append(out, "region")
	}
	if scale == "" {
		out = append(out, "scale")
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func enterpriseFreshness(collectedAt string, cycleDays int, now time.Time) string {
	collected, err := time.Parse(time.RFC3339, collectedAt)
	if err != nil {
		return "expired"
	}
	if collected.Add(time.Duration(cycleDays) * 24 * time.Hour).Before(now) {
		return "expired"
	}
	return "fresh"
}

func enterpriseContentSHA(id, name, industry, region, scale string) string {
	sum := sha256.Sum256(enterpriseContentBytes(id, name, industry, region, scale))
	return hex.EncodeToString(sum[:])
}

func enterpriseContentBytes(id, name, industry, region, scale string) []byte {
	raw, _ := json.Marshal(struct {
		EnterpriseID string `json:"enterprise_id"`
		Name         string `json:"enterprise_name"`
		Industry     string `json:"industry"`
		Region       string `json:"region"`
		Scale        string `json:"scale"`
	}{id, name, industry, region, scale})
	return raw
}

func ensureSourceRefTx(tx *sql.Tx, tenantID, sourceApp, sourceRef, snapshot, createdBy string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM source_refs WHERE tenant_id=? AND source_app=? AND source_ref=? ORDER BY created_at LIMIT 1`,
		tenantID, sourceApp, sourceRef).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID("src_")
	_, err = tx.Exec(`INSERT INTO source_refs(id,tenant_id,source_app,source_ref,auth_scope_snapshot,created_by,created_at)
		VALUES(?,?,?,?,?,?,?)`, id, tenantID, sourceApp, sourceRef, snapshot, createdBy, now())
	return id, err
}
