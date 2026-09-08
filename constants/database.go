package constants

const (
	UserPublic  = "public"
	UserPrivate = "private"
	UserFriends = "friends"
)

const (
	RoleAdmin     = "Admin"
	RoleOwner     = "Owner"
	RoleEditor    = "Editor"
	RoleViewer    = "Viewer"
	RoleSupport   = "Support"
	RoleCommunity = "Community"
)

const (
	PermissionRead    = "read"
	PermissionEdit    = "Edit"
	PermissionDelete  = "delete"
	PermissionAchieve = "archive"
	PermissionCreate  = "Create"
)

const (
	ClientChrome  = "Chrome"
	ClientFirefox = "firefox"
	ClientSafari  = "safari"
	ClientEdge    = "edge"
	ClientOpera   = "opera"
	ClientAndroid = "android"
	ClientIOS     = "ios"
	ClientBrave   = "brave"
	ClientOthers  = "others"
)

const (
	EmailVerificationStatusUnverified       = "Unverified"
	EmailVerificationStatusVerificationSent = "Verification link sent"
	EmailVerificationStatusVerified         = "Verified"
)

const (
	AuthTheMonkeys   = "the-monkeys"
	AuthGoogleOauth2 = "google-oauth2"
	AuthInstaOauth2  = "instagram-oauth2"
)

const (
	UserActive   = "active"
	UserInactive = "inactive"
	UserHidden   = "hidden"
)

const (
	BlogStatusDraft     = "Draft"
	BlogStatusPublished = "Published"
	BlogStatusScheduled = "Scheduled"
	BlogStatusArchived  = "Archived"
	BlogDeleted         = "Deleted"
)

var Clients = []string{"Chrome", "Firefox", "Safari", "Edge", "Opera", "Android", "iOS", "Brave", "Others"}
