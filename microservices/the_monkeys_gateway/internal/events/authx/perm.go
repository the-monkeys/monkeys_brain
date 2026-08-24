// Package authx is the gateway-side authorization layer for the events module.
//
// The events service remains the authoritative enforcer: every mutating RPC
// re-checks the caller against event_permissions before it touches a row. This
// package exists so the gateway can reject an unauthorized request before
// spending a service round trip, and so handlers can shape a response to the
// viewer (hiding drafts, withholding meeting links) without each one
// re-deriving who the caller is.
//
// Because the service is the real gate, a briefly stale grant here cannot
// authorize anything the service would refuse. That is what lets the cache be
// TTL-only, with no invalidation protocol to keep in sync.
package authx

import "strings"

// Perm is one bit of the caller's grant on an event. A bitmask keeps the
// hot-path check to a single AND against a cached word.
type Perm uint8

const (
	PermEditEvent Perm = 1 << iota
	PermManageAttendees
	PermManageTickets
	PermManageCoupons
	PermManageHosts
	PermManageQuestions
	PermManageCheckins
)

// wire maps event_permissions.permission_type values onto their bits.
var wire = map[string]Perm{
	"edit_event":       PermEditEvent,
	"manage_attendees": PermManageAttendees,
	"manage_tickets":   PermManageTickets,
	"manage_coupons":   PermManageCoupons,
	"manage_hosts":     PermManageHosts,
	"manage_questions": PermManageQuestions,
	"manage_checkins":  PermManageCheckins,
}

var labels = [...]struct {
	bit  Perm
	name string
}{
	{PermEditEvent, "edit_event"},
	{PermManageAttendees, "manage_attendees"},
	{PermManageTickets, "manage_tickets"},
	{PermManageCoupons, "manage_coupons"},
	{PermManageHosts, "manage_hosts"},
	{PermManageQuestions, "manage_questions"},
	{PermManageCheckins, "manage_checkins"},
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

// String renders the mask for error messages, e.g. "manage_tickets".
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
