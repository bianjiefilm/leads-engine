package store

import "strings"

// PendingLeadIDsByCampaign returns leads that still have no follow-up row,
// grouped by the campaign_ref stored on the notify snapshot. Requested
// campaigns with no such lead are present with an empty slice. Contact
// fields are not loaded.
func (s *Store) PendingLeadIDsByCampaign(tenantID string, campaigns []string) (map[string][]string, error) {
	want := map[string]bool{}
	out := map[string][]string{}
	for _, campaign := range campaigns {
		campaign = strings.TrimSpace(campaign)
		if campaign == "" || want[campaign] {
			continue
		}
		want[campaign] = true
		out[campaign] = []string{}
	}
	if len(out) == 0 {
		return out, nil
	}
	rows, err := s.DB.Query(`
		SELECT l.id, COALESCE(sr.auth_scope_snapshot,'')
		FROM leads l
		LEFT JOIN source_refs sr ON sr.id = l.source_ref_id AND sr.tenant_id = l.tenant_id
		WHERE l.tenant_id=?
		  AND NOT EXISTS (
		    SELECT 1 FROM follow_ups f
		    WHERE f.tenant_id=l.tenant_id AND f.lead_id=l.id
		  )`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, snapshot string
		if err := rows.Scan(&id, &snapshot); err != nil {
			return nil, err
		}
		campaign := campaignRefFromSnapshot(snapshot)
		if !want[campaign] {
			continue
		}
		out[campaign] = append(out[campaign], id)
	}
	return out, rows.Err()
}
