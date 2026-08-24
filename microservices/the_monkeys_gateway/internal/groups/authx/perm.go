// Package authx is the gateway-side authorization layer for the groups module.
//
// The groups service remains the authoritative enforcer: every mutating RPC
// re-checks the caller against group_permissions inside its transaction before
// it touches a row. This package exists so the gateway can reject an
// unauthorized request before spending a service round trip, and so handlers
// can shape a response to the viewer (hiding drafts, withholding private
// groups) without each one re-deriving who the caller is.
//
// Because the service is the real gate, a briefly stale grant here cannot
// authorize anything the service would refuse. That is what lets the cache be
// TTL-only, with no invalidation protocol to keep in sync.
package authx

import "strings"

// Perm is one bit of the caller's grant on a group. A bitmask keeps the
// hot-path check to a single AND against a cached word.
type Perm uint8

const (
	PermEditGroup Perm = 1 << iota
	PermManageMembers
	PermManageEvents
	PermManageDiscussions
	PermManageDues
	PermManageRoles
	PermViewGroupAnalytics
	PermDeleteGroup
)

// wire maps group_permissions.permission_type values onto their bits. The
// organizer's rights are implicit in the schema; the service spells the full
// bundle out in Authorize responses, so the gateway never special-cases it.
var wire = map[string]Perm{
	"edit_group":           PermEditGroup,
	"manage_members":       PermManageMembers,
	"manage_events":        PermManageEvents,
	"manage_discussions":   PermManageDiscussions,
	"manage_dues":          PermManageDues,
	"manage_roles":         PermManageRoles,
	"view_group_analytics": PermViewGroupAnalytics,
	"delete_group":         PermDeleteGroup,
}

var labels = [...]struct {
	bit  Perm
	name string
}{
	{PermEditGroup, "edit_group"},
	{PermManageMembers, "manage_members"},
	{PermManageEvents, "manage_events"},
	{PermManageDiscussions, "manage_discussions"},
	{PermManageDues, "manage_dues"},
	{PermManageRoles, "manage_roles"},
	{PermViewGroupAnalytics, "view_group_analytics"},
	{PermDeleteGroup, "delete_group"},
}

// ParsePerms folds the service's permission strings into a mask. Unknown names
// are ignored so the service can add grants without breaking older gateways.
func ParsePerms(names []string) Perm {
	var p Perm
	for _, n := range names {
		p |= wire[n]
	}
	return p
}

// Has reports whether every bit in want is held.
func (p Perm) Has(want Perm) bool { return p&want == want }

// String renders the mask for error messages, e.g. "manage_members".
func (p Perm) String() string {
	var out []string
	for _, l := range labels {
		if p&l.bit != 0 {
			out = append(out, l.name)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}
