package authx

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

// ctxGrant is where a middleware parks the resolved grant for its handler.
const ctxGrant = "events_grant"

// grantTTL bounds how long a co-host change takes to show up at the gateway.
// It is short because the win is collapsing bursts, not long-lived caching.
const grantTTL = 15 * time.Second

// lookupTimeout caps a single Authorize call. It sits in front of every event
// route, so it must fail fast rather than hold the request open.
const lookupTimeout = 3 * time.Second

// Guard resolves and enforces the caller's standing on an event.
//
// The hot path for a popular public event is one Authorize call per slug per
// TTL, because every anonymous viewer shares a cache key. Signed-in viewers
// get their own key, and concurrent misses on the same key are collapsed into
// a single upstream call.
type Guard struct {
	client pb.EventServiceClient
	log    *zap.SugaredLogger
	cache  *cache
	flight singleflight.Group
}

func NewGuard(client pb.EventServiceClient, log *zap.SugaredLogger) *Guard {
	return &Guard{client: client, log: log, cache: newCache(grantTTL)}
}

// GrantFrom returns the grant stored by a Guard middleware. The zero value is
// a safe deny, so a handler that runs without a guard cannot accidentally pass.
func GrantFrom(ctx *gin.Context) Grant {
	g, _ := ctx.Value(ctxGrant).(Grant)
	return g
}

// resolve returns the caller's grant on the event, from cache when possible.
func (g *Guard) resolve(ctx *gin.Context, accountID, slug string) (Grant, error) {
	key := accountID + "\x00" + slug
	if grant, ok := g.cache.get(key); ok {
		return grant, nil
	}

	v, err, _ := g.flight.Do(key, func() (any, error) {
		// Detach from the request that happened to win the race: if that
		// client disconnects, everyone waiting on the same key would
		// otherwise inherit its cancellation.
		call, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
		defer cancel()

		res, err := g.client.Authorize(call, &pb.AuthorizeReq{
			AccountId: accountID,
			EventSlug: slug,
		})
		if err != nil {
			return nil, err
		}
		grant := Grant{
			Exists:     res.EventExists,
			Status:     res.EventStatus,
			Role:       res.Role,
			Perms:      ParsePerms(res.Permissions),
			IsAttendee: res.IsAttendee,
		}
		g.cache.put(key, grant)
		return grant, nil
	})
	if err != nil {
		return Grant{}, err
	}
	return v.(Grant), nil
}

// load resolves the grant and parks it, or writes an error response and
// reports false. Failure is closed: if we cannot tell what the caller is
// allowed to see, we do not guess.
func (g *Guard) load(ctx *gin.Context) (Grant, bool) {
	slug := ctx.Param("slug")
	if slug == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "event slug is required"})
		return Grant{}, false
	}

	grant, err := g.resolve(ctx, ctx.GetString("accountId"), slug)
	if err != nil {
		g.log.Errorw("events authz lookup failed", "slug", slug, "err", err)
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"message": "cannot verify access right now, please retry",
		})
		return Grant{}, false
	}

	ctx.Set(ctxGrant, grant)
	return grant, true
}

// notFound is the response for both a missing event and a draft the caller
// does not host. They are deliberately indistinguishable: a 403 on a draft
// would confirm the slug exists.
func notFound(ctx *gin.Context) {
	ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "event not found"})
}

// RequireVisible admits anyone who may see the event: it is published, or the
// caller hosts it. Use on every route that reads or acts on a single event.
func (g *Guard) RequireVisible() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		grant, ok := g.load(ctx)
		if !ok {
			return
		}
		if !grant.Visible() {
			notFound(ctx)
			return
		}
		ctx.Next()
	}
}

// Require admits only callers holding every bit in want. The organizer holds
// all of them; a co-host holds whatever event_permissions grants.
func (g *Guard) Require(want Perm) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		grant, ok := g.load(ctx)
		if !ok {
			return
		}
		if !grant.Visible() {
			notFound(ctx)
			return
		}
		if !grant.Perms.Has(want) {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"message": "you need " + want.String() + " permission on this event",
			})
			return
		}
		ctx.Next()
	}
}

// RequireOrganizer admits only the event's owner. Some actions are destructive
// enough that delegating them to a co-host is not appropriate.
func (g *Guard) RequireOrganizer() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		grant, ok := g.load(ctx)
		if !ok {
			return
		}
		if !grant.Visible() {
			notFound(ctx)
			return
		}
		if grant.Role != "organizer" {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"message": "only the event organizer can do this",
			})
			return
		}
		ctx.Next()
	}
}

// RequireCommentOwner admits the comment's author, plus event hosts acting as
// moderators. Hosts are settled from the cached grant; only a non-host pays
// for the ownership probe, which is never cached because it is per comment.
func (g *Guard) RequireCommentOwner() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		grant, ok := g.load(ctx)
		if !ok {
			return
		}
		if !grant.Visible() {
			notFound(ctx)
			return
		}
		if grant.IsHost() {
			ctx.Next()
			return
		}

		commentID, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid comment id"})
			return
		}

		res, err := g.client.Authorize(ctx, &pb.AuthorizeReq{
			AccountId: ctx.GetString("accountId"),
			EventSlug: ctx.Param("slug"),
			CommentId: commentID,
		})
		if err != nil {
			g.log.Errorw("comment ownership lookup failed", "comment_id", commentID, "err", err)
			ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"message": "cannot verify access right now, please retry",
			})
			return
		}
		if !res.OwnsComment {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"message": "you can only delete your own comments",
			})
			return
		}
		ctx.Next()
	}
}

// RequireCanRSVP admits anyone who may see the event. RSVP acts on the
// caller's own row; the service enforces the RSVP window, capacity and any
// block on the caller, so a visible event is the only gate the gateway adds.
func (g *Guard) RequireCanRSVP() gin.HandlerFunc { return g.RequireVisible() }

// RequireCanCheckIn gates marking an attendee checked in or no-show. It touches
// another person's row, so it needs manage_checkins — the same permission the
// service demands, so a holder of only manage_attendees is refused here rather
// than after a wasted round trip.
func (g *Guard) RequireCanCheckIn() gin.HandlerFunc { return g.Require(PermManageCheckins) }

// RequireCanManageQuestions gates the event Q&A moderation surface.
func (g *Guard) RequireCanManageQuestions() gin.HandlerFunc { return g.Require(PermManageQuestions) }

// RequireCanViewAttendeeContact gates attendee PII (contact details, the
// export). It needs manage_attendees.
func (g *Guard) RequireCanViewAttendeeContact() gin.HandlerFunc {
	return g.Require(PermManageAttendees)
}
