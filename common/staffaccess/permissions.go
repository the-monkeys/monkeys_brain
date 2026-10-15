// Package staffaccess defines server-owned permissions for existing staff roles.
package staffaccess

import "github.com/the-monkeys/the_monkeys/constants"

const (
	Access                = "staff.access"
	OverviewRead          = "overview.read"
	UsersRead             = "users.read"
	UsersManage           = "users.manage"
	UsersDelete           = "users.delete"
	PostsRead             = "posts.read"
	PostsManage           = "posts.manage"
	EventsRead            = "events.read"
	EventsManage          = "events.manage"
	GroupsRead            = "groups.read"
	GroupsManage          = "groups.manage"
	VerificationRead      = "verification.read"
	VerificationReview    = "verification.review"
	VerificationDocuments = "verification.documents.read"
	ModerationManage      = "moderation.manage"
	PaymentsRead          = "payments.read"
	PaymentsManage        = "payments.manage"
	OrphansRead           = "posts.orphans.read"
)

var rolePermissions = map[string][]string{
	constants.RoleAdmin:     {Access, OverviewRead, UsersRead, UsersManage, UsersDelete, PostsRead, PostsManage, EventsRead, EventsManage, GroupsRead, GroupsManage, VerificationRead, VerificationReview, VerificationDocuments, ModerationManage, PaymentsRead, PaymentsManage, OrphansRead},
	constants.RoleSupport:   {Access, OverviewRead, VerificationRead, VerificationReview, VerificationDocuments},
	constants.RoleCommunity: {Access, OverviewRead, UsersRead, UsersDelete, PostsRead, PostsManage, EventsRead, EventsManage, GroupsRead, GroupsManage, VerificationRead, VerificationReview, VerificationDocuments, ModerationManage},
}

// Permissions returns a copy so callers cannot change the authorization policy.
func Permissions(role string) []string {
	return append([]string{}, rolePermissions[role]...)
}

func Has(role, permission string) bool {
	for _, allowed := range rolePermissions[role] {
		if allowed == permission {
			return true
		}
	}
	return false
}
