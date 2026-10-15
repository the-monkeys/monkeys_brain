package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"go.uber.org/zap"
)

// registerStaffRoutes is shared by production registration and route-boundary tests.
// Authentication and private-network checks are installed by RegisterAdminRouter.
func registerStaffRoutes(staff *gin.RouterGroup, asc *AdminServiceClient, log *zap.SugaredLogger) {
	guard := func(permission string) gin.HandlerFunc { return RequirePermission(log, permission) }
	staff.GET("/session", guard(staffaccess.Access), asc.StaffSession)
	staff.GET("/stats", guard(staffaccess.OverviewRead), asc.GetStats)
	staff.GET("/payments/events", guard(staffaccess.PaymentsRead), asc.ListEventPayments)
	staff.GET("/payments/events/:slug", guard(staffaccess.PaymentsRead), asc.GetEventPayments)
	staff.POST("/payments/events/:slug/settlements", guard(staffaccess.PaymentsManage), asc.CreateSettlement)
	staff.POST("/payments/settlements/:id/mark-paid", guard(staffaccess.PaymentsManage), asc.MarkSettlementPaid)
	staff.GET("/users", guard(staffaccess.UsersRead), asc.ListUsers)
	staff.DELETE("/users/:id", guard(staffaccess.UsersDelete), asc.DeleteUserJWT)
	staff.POST("/users/:id/role", guard(staffaccess.UsersManage), asc.SetUserRole)
	staff.POST("/users/:id/flag", guard(staffaccess.UsersManage), asc.FlagUser)
	staff.POST("/users/:id/unflag", guard(staffaccess.UsersManage), asc.UnflagUserJWT)
	staff.POST("/users/:id/suspend", guard(staffaccess.UsersManage), asc.SuspendUser)
	staff.GET("/blogs", guard(staffaccess.PostsRead), asc.ListBlogs)
	staff.POST("/blogs/:blog_id/unpublish", guard(staffaccess.PostsManage), asc.UnpublishBlog)
	staff.DELETE("/blogs/:blog_id", guard(staffaccess.PostsManage), asc.DeleteBlog)
	staff.GET("/blogs/orphans", guard(staffaccess.OrphansRead), asc.ListOrphanBlogs)
	staff.GET("/events", guard(staffaccess.EventsRead), asc.ListAdminEvents)
	staff.POST("/events/:slug/cancel", guard(staffaccess.EventsManage), asc.CancelAdminEvent)
	staff.POST("/events/:slug/unpublish", guard(staffaccess.EventsManage), asc.UnpublishAdminEvent)
	staff.DELETE("/events/:slug", guard(staffaccess.EventsManage), asc.DeleteAdminEvent)
	staff.GET("/groups", guard(staffaccess.GroupsRead), asc.ListAdminGroups)
	staff.POST("/groups/:slug/suspend", guard(staffaccess.GroupsManage), asc.SuspendGroup)
	staff.DELETE("/groups/:slug", guard(staffaccess.GroupsManage), asc.DeleteAdminGroup)
	staff.GET("/verifications", guard(staffaccess.VerificationRead), asc.ListVerifications)
	staff.POST("/verifications/:id/review", guard(staffaccess.VerificationReview), asc.ReviewVerification)
	staff.POST("/events/:slug/nsfw", guard(staffaccess.ModerationManage), asc.FlagNsfw("event"))
	staff.POST("/blogs/:blog_id/nsfw", guard(staffaccess.ModerationManage), asc.FlagNsfw("blog"))
	staff.POST("/groups/:slug/nsfw", guard(staffaccess.ModerationManage), asc.FlagNsfw("group"))
	staff.POST("/users/:id/nsfw", guard(staffaccess.ModerationManage), asc.FlagNsfw("user"))
	staff.POST("/events/:slug/comments/:id/hide", guard(staffaccess.ModerationManage), asc.HideEventComment)
	staff.POST("/events/:slug/questions/:id/hide", guard(staffaccess.ModerationManage), asc.HideEventQuestion)
}
