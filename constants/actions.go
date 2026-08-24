package constants

const (
	USER_REGISTER       = "user_register"
	USERNAME_UPDATE     = "username_update"
	USER_ACCOUNT_DELETE = "user_profile_directory_delete"
	BLOG_CREATE         = "blog_create"
	BLOG_UPDATE         = "blog_update"
	BLOG_PUBLISH        = "published"
	BLOG_SCHEDULE       = "scheduled"
	BLOG_DELETE         = "delete"
	BLOG_LIKE           = "blog_like"
	USER_FOLLOWED       = "user_followed"
	READ_BLOG           = "read_blog"
	READ_DRAFT          = "read_draft"

	DELETE_USERS_ALL_BLOGS = "delete_users_all_blogs"

	PROFILE_UPDATE = "profile_update"

	// Notification-specific actions (published to RoutingKeys[4])
	CO_AUTHOR_INVITE         = "co_author_invite"
	CO_AUTHOR_ACCEPT         = "co_author_accept"
	CO_AUTHOR_DECLINE        = "co_author_decline"
	CO_AUTHOR_REMOVED        = "co_author_removed"
	CO_AUTHOR_BLOG_PUBLISHED = "co_author_blog_published"
	PASSWORD_CHANGED         = "password_changed"
	EMAIL_VERIFIED           = "email_verified"
	BLOG_COMMENT             = "blog_comment"
	LOGIN_DETECTED           = "login_detected"
	PASSWORD_RESET_REQUESTED = "password_reset_requested"
	EMAIL_CHANGED            = "email_changed"
	USERNAME_CHANGED         = "username_changed"
	USER_DEACTIVATED         = "user_deactivated"
	USER_REACTIVATED         = "user_reactivated"
	PREFERENCES_CHANGED      = "preferences_changed"

	// Event actions (published to RoutingKeys[4])
	EVENT_RSVP_CONFIRMED    = "event_rsvp_confirmed"
	EVENT_RSVP_WAITLISTED   = "event_rsvp_waitlisted"
	EVENT_WAITLIST_PROMOTED = "event_waitlist_promoted"
	EVENT_REMINDER          = "event_reminder"
	EVENT_CANCELLED         = "event_cancelled"
	EVENT_NEW_BY_FOLLOWED   = "event_new_by_followed"
	EVENT_COMMENT_NEW       = "event_comment_new"
	EVENT_PAYMENT_REFUND    = "event_payment_refund"
)

// RestrictedUsernames contains usernames that are not allowed for user registration
var RestrictedUsernames = []string{
	"admin", "administrator", "root", "superuser", "support", "help",
	"contact", "info", "user", "username", "null", "undefined",
	"system", "api", "www", "mail", "ftp", "blog", "news", "forum",
	"test", "demo", "guest", "public", "private", "staff", "moderator",
	"team", "teams", "feed", "feedback", "themonkeys", "the_monkeys", "themonkeysadmin", "themonkeysadministrator",
	"about", "access", "account", "accounts", "activity", "add", "address", "addresses",
	"all", "analytics", "and", "api", "apps", "archive", "archives", "area", "areas",
	"auth", "authentication", "authorize", "auto", "backup", "backups", "base", "billing",
}

const (
	NotificationRegister = `Subject: Welcome! Complete Your Registration

Thank you for signing up with The Monkeys!

To complete your registration and activate your account, please click the link sent to your email or copy and paste it into your browser:

We're excited to have you on board!

Best regards,
The Monkeys Team
`

	NotificationBlogLicked = `Subject: Blog Liked! 
		%s liked your blog %s
	`
)
