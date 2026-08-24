package groups

import (
	"github.com/gin-gonic/gin"
	eventpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/groups/authx"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/storage_v2"
	"go.uber.org/zap"
)

// RegisterGroupRouter wires the groups REST surface.
//
// Reads that a stranger may see (public group listings and detail pages) are
// AuthOptional: the token, when present, lets the service resolve the viewer's
// role and unlock private groups they belong to. Everything that mutates state
// or exposes a member roster sits behind AuthRequired.
//
// Every route scoped to one group carries a guard from the authx package. It
// is an early-reject layer only: the groups service still re-checks each
// mutation against group_permissions inside its transaction, so a briefly
// stale gateway grant cannot authorize anything the service would refuse. The
// guard also keeps drafts and private groups out of read responses.
//
// eventsClient is threaded in so a group-scoped event can be created through
// the events service without the caller leaving the /groups surface.
//
// storageSvc is threaded in so group logo/cover uploads live on the /groups
// surface behind the same group permission guard, rather than on the storage
// surface which only knows how to authorize by user id.
func RegisterGroupRouter(
	router *gin.Engine,
	cfg *config.Config,
	authClient *auth.ServiceClient,
	eventsClient eventpb.EventServiceClient,
	storageSvc *storage_v2.Service,
	lg *zap.SugaredLogger,
) *GroupServiceClient {
	mware := auth.InitAuthMiddleware(authClient, lg)

	gsc := &GroupServiceClient{
		Client: NewGroupServiceClient(cfg, lg),
		log:    lg,
	}
	gec := &GroupEventClient{
		client: eventsClient,
		log:    lg,
	}
	guard := authx.NewGuard(gsc.Client, lg)

	// -------------------------------------------------------------------
	// Public reads
	// -------------------------------------------------------------------
	pub := router.Group("/api/v1/groups", rateLimit(rateRead))

	pub.GET("", mware.AuthOptional, gsc.ListGroups)
	pub.GET("/user/:username", mware.AuthOptional, gsc.GetUserGroups)
	pub.GET("/:slug", mware.AuthOptional, guard.RequireGroupVisible(), gsc.GetGroup)

	// -------------------------------------------------------------------
	// Authenticated
	// -------------------------------------------------------------------
	authed := router.Group("/api/v1/groups", mware.AuthRequired)

	write := authed.Group("", rateLimit(rateWrite))
	read := authed.Group("", rateLimit(rateRead))

	// Group lifecycle. Creating a group needs no group guard (there is no
	// group yet); editing needs edit_group; deleting stays with the organizer
	// even though co-organizers carry delete_group in their bundle.
	write.POST("", gsc.CreateGroup)
	write.PUT("/:slug", guard.RequireGroupPermission(authx.PermEditGroup), gsc.UpdateGroup)
	write.DELETE("/:slug", guard.RequireGroupOrganizer(), gsc.DeleteGroup)
	write.POST("/:slug/publish", guard.RequireGroupPermission(authx.PermEditGroup), gsc.PublishGroup)

	// Membership. Join and leave act on the caller's own membership, so the
	// service is left to arbitrate them (a private group must stay joinable by
	// invited strangers, whom RequireGroupVisible would turn away). The roster
	// and the moderation verbs act on other members and are gated here.
	write.POST("/:slug/join", gsc.JoinGroup)
	write.DELETE("/:slug/membership", gsc.LeaveGroup)
	read.GET("/:slug/members", guard.RequireGroupVisible(), gsc.ListMembers)
	write.PUT("/:slug/members/:username/role", guard.RequireGroupPermission(authx.PermManageRoles), gsc.UpdateMemberRole)
	write.DELETE("/:slug/members/:username", guard.RequireCanManageMember(), gsc.RemoveMember)
	write.POST("/:slug/members/:username/ban", guard.RequireCanManageMember(), gsc.BanMember)
	write.POST("/:slug/members/:username/approve", guard.RequireCanManageMember(), gsc.ApproveJoinRequest)
	write.POST("/:slug/members/:username/reject", guard.RequireCanManageMember(), gsc.RejectJoinRequest)

	// Direct add. Staff enroll a user straight to active, gated by
	// manage_members. The invite roster and link lifecycle sit behind the same
	// permission.
	write.POST("/:slug/members", guard.RequireGroupPermission(authx.PermManageMembers), gsc.AddMember)
	write.POST("/:slug/invites", guard.RequireGroupPermission(authx.PermManageMembers), gsc.CreateInvite)
	read.GET("/:slug/invites", guard.RequireGroupPermission(authx.PermManageMembers), gsc.ListInvites)
	write.DELETE("/:slug/invites/:id", guard.RequireGroupPermission(authx.PermManageMembers), gsc.RevokeInvite)

	// Group imagery. Logo and cover uploads are gated by edit_group and handled
	// by the storage service, which stores them under groups/{slug}/{kind} and
	// returns a domain-free URL the caller persists on the group via UpdateGroup.
	if storageSvc != nil {
		write.POST("/:slug/images/:kind", guard.RequireGroupPermission(authx.PermEditGroup), storageSvc.UploadGroupImage)
		write.DELETE("/:slug/images/:kind", guard.RequireGroupPermission(authx.PermEditGroup), storageSvc.DeleteGroupImage)
	}

	// Rules are group settings, gated by edit_group.
	write.POST("/:slug/rules", guard.RequireGroupPermission(authx.PermEditGroup), gsc.AddGroupRule)
	write.PUT("/:slug/rules/:id", guard.RequireGroupPermission(authx.PermEditGroup), gsc.UpdateGroupRule)
	write.DELETE("/:slug/rules/:id", guard.RequireGroupPermission(authx.PermEditGroup), gsc.DeleteGroupRule)

	// Group-scoped event creation, delegated to the events service. Creating
	// an event under the group needs manage_events on the group.
	write.POST("/:slug/events", guard.RequireGroupPermission(authx.PermManageEvents), gec.CreateGroupEvent)

	// -------------------------------------------------------------------
	// Invite links, addressed by token on a separate top-level prefix.
	//
	// These live under /api/v1/group-invites rather than under /:slug so the
	// token path segment never collides with gin's :slug wildcard. The token
	// is itself the capability: previewing an invite is AuthOptional, while
	// accepting one requires a signed-in caller to enroll.
	// -------------------------------------------------------------------
	invitePub := router.Group("/api/v1/group-invites", rateLimit(rateRead))
	invitePub.GET("/:token", mware.AuthOptional, gsc.GetInvite)

	inviteAuthed := router.Group("/api/v1/group-invites", mware.AuthRequired, rateLimit(rateWrite))
	inviteAuthed.POST("/:token/accept", gsc.AcceptInvite)

	return gsc
}
