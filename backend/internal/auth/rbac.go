// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"slices"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Permission is one entry of the permission catalogue. Every call names
// the permission it needs; a principal that does not hold it is refused
// (SEC-102). The catalogue grows with each phase; custom roles composed
// from it arrive with GOV-002 in P9.
type Permission string

// The permissions this phase defines.
const (
	OrganizationRead   Permission = "organization.read"
	OrganizationManage Permission = "organization.manage"
	MembersManage      Permission = "members.manage"
	AppRead            Permission = "app.read"
	AppManage          Permission = "app.manage"
	EnvironmentManage  Permission = "environment.manage"
	SecretsManage      Permission = "secrets.manage"
	LimitsManage       Permission = "limits.manage"
	PluginRead         Permission = "plugin.read"
	PluginEdit         Permission = "plugin.edit"
	PluginLockOverride Permission = "plugin.lock.override"
	ReleasePublish     Permission = "release.publish"
	ReleasePromote     Permission = "release.promote"
	TokensManage       Permission = "tokens.manage"
	KeysManage         Permission = "keys.manage"
	AuditRead          Permission = "audit.read"
	TrashManage        Permission = "trash.manage"
)

// permissions is the catalogue, sorted.
var permissions = []Permission{
	AppManage, AppRead, AuditRead, EnvironmentManage, KeysManage, LimitsManage,
	MembersManage, OrganizationManage, OrganizationRead, PluginEdit,
	PluginLockOverride, PluginRead, ReleasePromote, ReleasePublish,
	SecretsManage, TokensManage, TrashManage,
}

// Permissions returns the catalogue, in order.
func Permissions() []Permission { return slices.Clone(permissions) }

// appPermissions are the permissions that make sense on one app; an
// app-level grant carries only these, whatever its role (GOV-001).
var appPermissions = []Permission{
	AppManage, AppRead, EnvironmentManage, PluginEdit, PluginLockOverride,
	PluginRead, ReleasePromote, ReleasePublish, SecretsManage, TrashManage,
}

// Role is a named set of permissions. P2 defines the four roles a
// small team needs; the full catalogue of GOV-002 arrives in P9, and
// this set is a subset of it, so no role is renamed later.
type Role string

// The roles of this phase.
const (
	// RoleOwner may do everything, including managing members and keys.
	RoleOwner Role = "owner"
	// RoleAdmin may do everything except manage members and keys.
	RoleAdmin Role = "admin"
	// RoleDeveloper may edit plugins and publish, but not promote,
	// manage secrets or read the audit log.
	RoleDeveloper Role = "developer"
	// RoleViewer may read.
	RoleViewer Role = "viewer"
)

// grants maps each role to the permissions it holds. A role holds
// exactly what is listed: there is no inheritance to reason about.
var grants = map[Role][]Permission{
	RoleOwner: permissions,
	RoleAdmin: {
		AppManage, AppRead, AuditRead, EnvironmentManage, LimitsManage,
		OrganizationManage, OrganizationRead, PluginEdit, PluginLockOverride,
		PluginRead, ReleasePromote, ReleasePublish, SecretsManage, TokensManage,
		TrashManage,
	},
	RoleDeveloper: {AppRead, OrganizationRead, PluginEdit, PluginRead, ReleasePublish},
	RoleViewer:    {AppRead, OrganizationRead, PluginRead},
}

// Roles returns every role, in order.
func Roles() []Role {
	out := make([]Role, 0, len(grants))
	for r := range grants {
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}

// ParseRole reads a role name, refusing anything that is not a role.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if _, ok := grants[r]; !ok {
		return "", plxerr.New(plxerr.InvalidEnumValue, "%q is not a role; the roles are owner, admin, developer and viewer", s)
	}
	return r, nil
}

// RolePermissions returns the permissions a role holds, in order.
func RolePermissions(r Role) []Permission { return slices.Clone(grants[r]) }

// stepUp lists the permissions that require a second factor (SEC-100):
// publishing, approving (promotion, until approvals arrive in P9),
// managing keys and managing members — and, beyond the requirement,
// secrets and tokens, which carry the same power by other means.
var stepUp = []Permission{
	KeysManage, MembersManage, ReleasePromote, ReleasePublish, SecretsManage, TokensManage,
}

// NeedsSecondFactor reports whether a permission may be used only after
// a second factor.
func NeedsSecondFactor(p Permission) bool { return slices.Contains(stepUp, p) }

// Principal is who is calling, resolved against one organisation.
type Principal struct {
	Identity
	// OrganizationID is the organisation this call acts in.
	OrganizationID string
	// Permissions are those held across the organisation, after a
	// token's scopes are applied.
	Permissions []Permission
	// AppPermissions are those held on single apps, by app ID, after a
	// token's scopes are applied (GOV-001).
	AppPermissions map[string][]Permission
}

// resolve computes a principal's permissions from its roles, intersected
// with a token's scopes when it has any. A token can therefore never do
// more than its user can do now: removing the user's role removes it
// from the token too.
func resolve(id Identity, organizationID string, roles []Role, appRoles map[string][]Role, direct []Permission) Principal {
	p := Principal{Identity: id, OrganizationID: organizationID, AppPermissions: map[string][]Permission{}}
	held := slices.Clone(direct)
	for _, r := range roles {
		held = append(held, grants[r]...)
	}
	p.Permissions = restrict(held, id.Scopes)
	for app, rs := range appRoles {
		var appHeld []Permission
		for _, r := range rs {
			for _, perm := range grants[r] {
				if slices.Contains(appPermissions, perm) {
					appHeld = append(appHeld, perm)
				}
			}
		}
		if perms := restrict(appHeld, id.Scopes); len(perms) > 0 {
			p.AppPermissions[app] = perms
		}
	}
	return p
}

// restrict sorts and de-duplicates held. scopes is nil for a session,
// which is not restricted, and never nil for a token, whose scopes are
// fixed when it is created: a token with no scopes can do nothing.
func restrict(held, scopes []Permission) []Permission {
	out := make([]Permission, 0, len(held))
	for _, perm := range held {
		if scopes != nil && !slices.Contains(scopes, perm) {
			continue
		}
		out = append(out, perm)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Holds reports whether the principal holds a permission across the
// organisation, ignoring the second-factor rule.
func (p Principal) Holds(want Permission) bool { return slices.Contains(p.Permissions, want) }

// HoldsOnApp reports whether the principal holds a permission on an app,
// through the organisation or through a grant on that app.
func (p Principal) HoldsOnApp(want Permission, appID string) bool {
	return p.Holds(want) || slices.Contains(p.AppPermissions[appID], want)
}

// Authorize returns nil when the principal may use a permission across
// the organisation, and otherwise the refusal. It is the only way a call
// is allowed: there is no default yes (SEC-102).
func (p Principal) Authorize(want Permission) error {
	return p.check(want, p.Holds(want))
}

// AuthorizeApp is Authorize for a permission on one app.
func (p Principal) AuthorizeApp(want Permission, appID string) error {
	return p.check(want, p.HoldsOnApp(want, appID))
}

// check turns a decision into the error the edge reports.
func (p Principal) check(want Permission, held bool) error {
	if p.OrganizationID == "" {
		return plxerr.New(plxerr.AuthenticationRequired, "this call acts in an organisation, and none was named")
	}
	if !held {
		return plxerr.New(plxerr.PermissionDenied, "%s is required", want)
	}
	if NeedsSecondFactor(want) && !p.SecondFactor {
		return plxerr.New(plxerr.MultiFactorRequired, "%s requires a second factor in this session", want)
	}
	return nil
}

// AllowedApps returns nil when the principal may read every app, and
// otherwise the apps it was granted one by one.
func (p Principal) AllowedApps() []string {
	if p.Holds(AppRead) {
		return nil
	}
	out := make([]string, 0, len(p.AppPermissions))
	for app, perms := range p.AppPermissions {
		if slices.Contains(perms, AppRead) {
			out = append(out, app)
		}
	}
	slices.Sort(out)
	return out
}

// Actor is how the audit log records the principal.
func (p Principal) Actor() audit.Actor { return p.Identity.Actor() }

// System is the principal of work the server does for itself, such as
// purging the trash. It holds everything and is audited as "system".
func System(organizationID string) Principal {
	return Principal{
		Identity:       Identity{Kind: KindSystem, ID: "server", Display: "Plux", SecondFactor: true},
		OrganizationID: organizationID,
		Permissions:    Permissions(),
		AppPermissions: map[string][]Permission{},
	}
}

// ParseScopes reads the scopes of a token, refusing anything outside
// the catalogue so that a typo cannot widen a token silently.
func ParseScopes(values []string) ([]Permission, error) {
	out := make([]Permission, 0, len(values))
	for _, v := range values {
		p := Permission(strings.TrimSpace(v))
		if !slices.Contains(permissions, p) {
			return nil, plxerr.New(plxerr.InvalidEnumValue, "%q is not a permission; see the catalogue", v)
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// ScopeStrings renders scopes for storage and for responses.
func ScopeStrings(scopes []Permission) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}

// subset reports the first scope a principal does not hold across the
// organisation, so that no one can mint a token that does more than
// they can (SRV-064).
func subset(p Principal, scopes []Permission) error {
	for _, s := range scopes {
		if !p.Holds(s) {
			return plxerr.New(plxerr.PermissionDenied, "a token cannot carry %s, which you do not hold", s)
		}
	}
	return nil
}
