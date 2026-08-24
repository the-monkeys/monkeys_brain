package groups

// GroupBody is the JSON payload for creating and updating a group.
//
// Location and imagery are optional; a group is legal with only a name and a
// visibility. Enum values are validated at this boundary so an invalid
// visibility never reaches the service.
type GroupBody struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	Visibility  string   `json:"visibility" binding:"required,oneof=public private unlisted"`
	City        string   `json:"city"`
	Region      string   `json:"region"`
	Country     string   `json:"country"`
	Timezone    string   `json:"timezone"`
	Latitude    float64  `json:"latitude"`
	Longitude   float64  `json:"longitude"`
	CoverImage  string   `json:"cover_image"`
	LogoImage   string   `json:"logo_image"`
	Topics      []string `json:"topics"`
}

// JoinBody carries the optional answers a private or restricted group asks of
// applicants. The answers are opaque JSON to the gateway; the service records
// them against the join request.
type JoinBody struct {
	Answers map[string]string `json:"answers"`
}

// MemberRoleBody is the JSON payload for promoting or demoting a member. The
// organizer role is deliberately excluded: transferring ownership is not a
// role edit.
type MemberRoleBody struct {
	Role string `json:"role" binding:"required,oneof=co_organizer moderator member"`
}

// BanBody is the JSON payload for banning a member. The reason is recorded on
// the membership row for the audit trail.
type BanBody struct {
	Reason string `json:"reason"`
}

// AddMemberBody enrolls a user directly as an active member. Role is optional
// and defaults to plain member; the organizer role is never assignable here.
type AddMemberBody struct {
	Username string `json:"username" binding:"required"`
	Role     string `json:"role" binding:"omitempty,oneof=co_organizer moderator member"`
}

// InviteBody parameterizes a new invite link. Every field is optional: the zero
// value produces an unlimited, non-expiring member invite. MaxUses = 0 means
// unlimited; ExpiresInHours = 0 means never expires.
type InviteBody struct {
	Role           string `json:"role" binding:"omitempty,oneof=co_organizer moderator member"`
	MaxUses        int32  `json:"max_uses" binding:"omitempty,min=0"`
	ExpiresInHours int32  `json:"expires_in_hours" binding:"omitempty,min=0"`
}

// RuleBody is the JSON payload for creating and updating a group rule.
type RuleBody struct {
	Title     string `json:"title" binding:"required"`
	Body      string `json:"body" binding:"required"`
	SortOrder int32  `json:"sort_order"`
}
