package authx

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

// ctxGrant is where a middleware parks the resolved grant for its handler.
const ctxGrant = "groups_grant"

// grantTTL bounds how long a role or membership change takes to show up at the
// gateway. It is short because the win is collapsing bursts, not long-lived
// caching.
const grantTTL = 15 * time.Second

// lookupTimeout caps a single Authorize call. It sits in front of every group
// route, so it must fail fast rather than hold the request open.
const lookupTimeout = 3 * time.Second

// Guard resolves and enforces the caller's standing on a group.
//
// The hot path for a popular public group is one Authorize call per slug per
// TTL, because every anonymous viewer shares a cache key. Signed-in viewers
// get their own key, and concurrent misses on the same key are collapsed into
// a single upstream call.
type Guard struct {
	client pb.GroupServiceClient
	log    *zap.SugaredLogger
	cache  *cache
	flight singleflight.Group
}

func NewGuard(client pb.GroupServiceClient, log *zap.SugaredLogger) *Guard {
	return &Guard{client: client, log: log, cache: newCache(grantTTL)}
}

// GrantFrom returns the grant stored by a Guard middleware. The zero value is
// a safe deny, so a handler that runs without a guard cannot accidentally pass.
func GrantFrom(ctx *gin.Context) Grant {
	g, _ := ctx.Value(ctxGrant).(Grant)
	return g
}

// resolve returns the caller's grant on the group, from cache when possible.
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

		res, err := g.client.Authorize(call, &pb.AuthorizeGroupReq{
			AccountId: accountID,
			GroupSlug: slug,
		})
		if err != nil {
			return nil, err
		}
		grant := Grant{
			Exists:       res.GroupExists,
			Status:       res.GroupStatus,
			Visibility:   res.GroupVisibility,
			Role:         res.Role,
			MemberStatus: res.MemberStatus,
			Perms:        ParsePerms(res.Permissions),
			IsMember:     res.IsMember,
			IsBanned:     res.IsBanned,
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
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "group slug is required"})
		return Grant{}, false
	}

	grant, err := g.resolve(ctx, ctx.GetString("accountId"), slug)
	if err != nil {
		g.log.Errorw("groups authz lookup failed", "slug", slug, "err", err)
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"message": "cannot verify access right now, please retry",
		})
		return Grant{}, false
	}

	ctx.Set(ctxGrant, grant)
	return grant, true
}

// notFound is the response for both a missing group and one the caller may not
// see (a draft, or a private group they do not belong to). They are
// deliberately indistinguishable: a 403 would confirm the slug exists.
func notFound(ctx *gin.Context) {
	ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "group not found"})
}

// RequireGroupVisible admits anyone who may see the group: it is published and
// public/unlisted, the caller is an active member, or the caller is staff.
// Use on every route that reads or acts on a single group.
func (g *Guard) RequireGroupVisible() gin.HandlerFunc {
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

// RequireGroupMember admits only active members (staff included). A banned or
// pending caller is refused. The group must still be visible, so a member of
// an archived group is turned away with a 404 rather than a 403.
func (g *Guard) RequireGroupMember() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		grant, ok := g.load(ctx)
		if !ok {
			return
		}
		if !grant.Visible() {
			notFound(ctx)
			return
		}
		if !grant.IsMember && !grant.IsStaff() {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"message": "you must be a member of this group",
			})
			return
		}
		ctx.Next()
	}
}

// RequireGroupPermission admits only callers holding every bit in want. The
// organizer holds all of them; other roles hold whatever group_permissions
// grants. A banned caller never holds any grant, so this refuses them too.
func (g *Guard) RequireGroupPermission(want Perm) gin.HandlerFunc {
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
				"message": "you need " + want.String() + " permission on this group",
			})
			return
		}
		ctx.Next()
	}
}

// RequireGroupOrganizer admits only the group's owner. Some actions, deleting
// the group above all, are destructive enough that delegating them to a
// co-organizer is not appropriate.
func (g *Guard) RequireGroupOrganizer() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		grant, ok := g.load(ctx)
		if !ok {
			return
		}
		if !grant.Visible() {
			notFound(ctx)
			return
		}
		if !grant.IsOrganizer() {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"message": "only the group organizer can do this",
			})
			return
		}
		ctx.Next()
	}
}

// RequireCanManageMember gates the member-moderation verbs (approve, reject,
// remove, ban). It needs manage_members. The service still enforces the role
// hierarchy authoritatively, so a moderator cannot use it to act on a
// co-organizer even though both pass this gate.
func (g *Guard) RequireCanManageMember() gin.HandlerFunc {
	return g.RequireGroupPermission(PermManageMembers)
}
