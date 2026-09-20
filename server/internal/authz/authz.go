// Package authz is the pure server-side authorization core.
//
// 分层纪律(ADR-0001 D5):登录身份(identity principal)≠ 租户成员资格
// (members 行)≠ 记录级授权(assignee/agent grant)。本包只裁决后两层;
// principal 的真伪由 identity 解析保证。
package authz

// Role of a tenant member.
type Role string

const (
	RoleOwner Role = "owner"
	RoleSales Role = "sales"
	RoleAgent Role = "agent"
)

// Valid reports whether the role is one of the known values.
func (r Role) Valid() bool { return r == RoleOwner || r == RoleSales || r == RoleAgent }

// Member is a tenant membership resolved from the members table.
type Member struct {
	ID           string // members.id, used for assignment comparisons
	TenantID     string
	PrincipalRef string
	Role         Role
	Enabled      bool
}

// AgentGrant is the per-tenant authorization row that lets an agent touch a
// specific tenant. No row, no access; there is no global customer pool.
type AgentGrant struct {
	TenantID     string
	PrincipalRef string
}

// Action is an operation the API can authorize.
type Action string

const (
	ActionCreate        Action = "create"
	ActionReadList      Action = "read_list"
	ActionReadRecord    Action = "read_record"
	ActionUpdate        Action = "update"
	ActionManageMembers Action = "manage_members"
	ActionExport        Action = "export"
	// ActionDelete is the profile soft delete (HUI-1691). Deleting a customer
	// profile is a tenant-grade, hard-to-walk-back action and therefore shares
	// the export privilege level: owner only.
	ActionDelete Action = "delete"
	// ActionMerge is the contact merge surface (HUI-1683): merging two customer
	// profiles, the merge-candidate pool and its resolution, dedup stats and
	// merge undo. 合并客户档案是租户级、影响面最大的动作:owner 专属,绝不静默。
	ActionMerge Action = "merge"
	// ActionManageForms is the versioned lead-form management surface
	// (HUI-1679 / FEAT-0180): draft/publish/disable forms and the schema
	// contract export. 表单领域配置与语义归获客,owner 专属。
	ActionManageForms Action = "manage_forms"
	// ActionManageAssignPool is the lead auto-assignment pool configuration
	// surface (HUI-1685 / FEAT-0186): which enabled sales receive auto-routed
	// leads, with what weight and region/industry tags. 池配置是租户级销售
	// 运营决策,直接决定线索流向:owner 专属,绝不静默。
	ActionManageAssignPool Action = "manage_assign_pool"
)

// Deny reason codes. ReasonNotFoundMask maps to HTTP 404 so a sales cannot
// probe records assigned to others.
const (
	ReasonNotMember    = "not_member"
	ReasonDisabled     = "member_disabled"
	ReasonCrossTenant  = "cross_tenant"
	ReasonAgentGrant   = "agent_grant_missing"
	ReasonForbidden    = "forbidden"
	ReasonNotFoundMask = "not_found"
)

// Decision is the outcome of an authorization check.
type Decision struct {
	Allowed bool
	// Reason is a stable machine-readable code for tests/logs.
	Reason string
	// MaskAs404 marks record-level probing that must answer 404, not 403.
	MaskAs404 bool
}

func allow() Decision { return Decision{Allowed: true} }

func deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

// RecordScope describes the target record's tenancy and assignment.
type RecordScope struct {
	TenantID string
	// AssigneeMemberID is the members.id the record is assigned to; empty for
	// unassigned records.
	AssigneeMemberID string
}

// agentHasGrant: agents need a grant for THEIR OWN tenant (a grant for another
// tenant never unlocks anything); other roles ignore grants.
func agentHasGrant(member *Member, grant *AgentGrant) bool {
	if member.Role != RoleAgent {
		return true
	}
	return grant != nil && grant.TenantID == member.TenantID && grant.PrincipalRef == member.PrincipalRef
}

// Authorize decides whether member may perform action on the tenant/record.
// member must be non-nil (a resolved membership for the caller's principal).
func Authorize(member *Member, grant *AgentGrant, action Action, rec RecordScope) Decision {
	if member == nil {
		return deny(ReasonNotMember)
	}
	if !member.Enabled {
		return deny(ReasonDisabled)
	}
	if !member.Role.Valid() {
		return deny(ReasonForbidden)
	}

	// Membership is per-tenant: acting on another tenant's record requires a
	// membership row in THAT tenant. Without it: cross-tenant refusal.
	if rec.TenantID != "" && rec.TenantID != member.TenantID {
		return deny(ReasonCrossTenant)
	}

	switch action {
	case ActionManageMembers, ActionExport, ActionDelete, ActionMerge, ActionManageForms, ActionManageAssignPool:
		if member.Role != RoleOwner {
			return deny(ReasonForbidden)
		}
		return allow()
	case ActionCreate, ActionReadList:
		if !agentHasGrant(member, grant) {
			return deny(ReasonAgentGrant)
		}
		return allow()
	case ActionReadRecord, ActionUpdate:
		if !agentHasGrant(member, grant) {
			return deny(ReasonAgentGrant)
		}
		if member.Role == RoleOwner {
			return allow()
		}
		// sales/agent: own records only. Another member's (or unassigned)
		// record is masked as not-found so assignment topology never leaks.
		if rec.AssigneeMemberID != "" && rec.AssigneeMemberID == member.ID {
			return allow()
		}
		d := deny(ReasonNotFoundMask)
		d.MaskAs404 = true
		return d
	default:
		return deny(ReasonForbidden)
	}
}
