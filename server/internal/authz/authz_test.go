package authz

import "testing"

// member fixtures for tenant A / tenant B.
func owner(id string) *Member {
	return &Member{ID: id, TenantID: "tA", PrincipalRef: "usr_owner", Role: RoleOwner, Enabled: true}
}
func salesAssigned(id string) *Member {
	return &Member{ID: id, TenantID: "tA", PrincipalRef: "usr_sales", Role: RoleSales, Enabled: true}
}
func agentInA(id string) *Member {
	return &Member{ID: id, TenantID: "tA", PrincipalRef: "usr_agent", Role: RoleAgent, Enabled: true}
}
func disabledInA(id string) *Member {
	return &Member{ID: id, TenantID: "tA", PrincipalRef: "usr_disabled", Role: RoleSales, Enabled: false}
}
func memberOfB(id string) *Member {
	return &Member{ID: id, TenantID: "tB", PrincipalRef: "usr_b", Role: RoleOwner, Enabled: true}
}

var grantA = &AgentGrant{TenantID: "tA", PrincipalRef: "usr_agent"}

// recA is a record in tenant A assigned to the sales member id "m_sales".
var recA = RecordScope{TenantID: "tA", AssigneeMemberID: "m_sales"}
var recAUnassigned = RecordScope{TenantID: "tA"}

func TestPermissionMatrix(t *testing.T) {
	cases := []struct {
		name    string
		member  *Member
		grant   *AgentGrant
		action  Action
		rec     RecordScope
		allowed bool
		reason  string
		mask404 bool
	}{
		// owner: full within tenant, incl. member management + export
		{"owner create", owner("m_owner"), nil, ActionCreate, recA, true, "", false},
		{"owner read record", owner("m_owner"), nil, ActionReadRecord, recA, true, "", false},
		{"owner update others", owner("m_owner"), nil, ActionUpdate, recAUnassigned, true, "", false},
		{"owner manage members", owner("m_owner"), nil, ActionManageMembers, recA, true, "", false},
		{"owner export", owner("m_owner"), nil, ActionExport, recA, true, "", false},

		// sales on own assigned record
		{"sales create", salesAssigned("m_sales"), nil, ActionCreate, recA, true, "", false},
		{"sales read own", salesAssigned("m_sales"), nil, ActionReadRecord, recA, true, "", false},
		{"sales update own", salesAssigned("m_sales"), nil, ActionUpdate, recA, true, "", false},
		{"sales list", salesAssigned("m_sales"), nil, ActionReadList, recA, true, "", false},

		// sales restricted: unassigned / others' records masked as 404
		{"sales read unassigned", salesAssigned("m_sales"), nil, ActionReadRecord, recAUnassigned, false, ReasonNotFoundMask, true},
		{"sales update unassigned", salesAssigned("m_sales"), nil, ActionUpdate, recAUnassigned, false, ReasonNotFoundMask, true},
		{"sales manage members", salesAssigned("m_sales"), nil, ActionManageMembers, recA, false, ReasonForbidden, false},
		{"sales export", salesAssigned("m_sales"), nil, ActionExport, recA, false, ReasonForbidden, false},

		// agent WITH per-tenant grant
		{"agent granted create", agentInA("m_agent"), grantA, ActionCreate, recA, true, "", false},
		{"agent granted list", agentInA("m_agent"), grantA, ActionReadList, recA, true, "", false},
		{"agent granted read own", agentInA("m_agent"), grantA, ActionReadRecord, RecordScope{TenantID: "tA", AssigneeMemberID: "m_agent"}, true, "", false},

		// agent WITHOUT grant: nothing at all (no global pool)
		{"agent ungranted create", agentInA("m_agent"), nil, ActionCreate, recA, false, ReasonAgentGrant, false},
		{"agent ungranted list", agentInA("m_agent"), nil, ActionReadList, recA, false, ReasonAgentGrant, false},
		{"agent ungranted read", agentInA("m_agent"), nil, ActionReadRecord, recA, false, ReasonAgentGrant, false},
		{"agent ungranted manage", agentInA("m_agent"), grantA, ActionManageMembers, recA, false, ReasonForbidden, false},

		// disabled member: everything refused
		{"disabled create", disabledInA("m_dis"), nil, ActionCreate, recA, false, ReasonDisabled, false},
		{"disabled read", disabledInA("m_dis"), nil, ActionReadRecord, recA, false, ReasonDisabled, false},
		{"disabled manage", disabledInA("m_dis"), nil, ActionManageMembers, recA, false, ReasonDisabled, false},

		// cross-tenant: refused for every role, even owner
		{"cross tenant owner", memberOfB("m_b"), nil, ActionReadRecord, recA, false, ReasonCrossTenant, false},
		{"cross tenant create", memberOfB("m_b"), nil, ActionCreate, recA, false, ReasonCrossTenant, false},
		{"cross tenant manage", memberOfB("m_b"), nil, ActionManageMembers, recA, false, ReasonCrossTenant, false},

		// no membership at all
		{"no member", nil, nil, ActionCreate, recA, false, ReasonNotMember, false},
	}
	for _, c := range cases {
		got := Authorize(c.member, c.grant, c.action, c.rec)
		if got.Allowed != c.allowed || got.Reason != c.reason || got.MaskAs404 != c.mask404 {
			t.Errorf("%s: got {allowed=%v reason=%s mask404=%v}, want {allowed=%v reason=%s mask404=%v}",
				c.name, got.Allowed, got.Reason, got.MaskAs404, c.allowed, c.reason, c.mask404)
		}
	}
}

func TestAgentGrantIsTenantSpecific(t *testing.T) {
	// grant for tenant B does not unlock tenant A.
	m := agentInA("m_agent")
	grantB := &AgentGrant{TenantID: "tB", PrincipalRef: "usr_agent"}
	if d := Authorize(m, grantB, ActionCreate, recA); d.Allowed {
		t.Fatalf("grant for another tenant must not unlock tenant A")
	}
	if d := Authorize(m, grantB, ActionCreate, recA); d.Reason != ReasonAgentGrant {
		t.Fatalf("reason = %s", d.Reason)
	}
}
