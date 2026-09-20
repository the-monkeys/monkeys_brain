package blog

import (
	"net/http"

	"github.com/gin-gonic/gin"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/blogacl"
)

func isActiveGroupMember(authz *grouppb.AuthorizeGroupResp) bool {
	return authz != nil && authz.GetIsMember() && authz.GetMemberStatus() == "active"
}

func (asc *BlogServiceClient) resolvePublishGroup(ctx *gin.Context, accountID, groupSlug, rawAudience string) (string, bool) {
	if asc.Groups == nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return "", false
	}

	authz, err := asc.Groups.Authorize(ctx.Request.Context(), &grouppb.AuthorizeGroupReq{
		AccountId: accountID,
		GroupSlug: groupSlug,
	})
	if err != nil {
		asc.log.Errorf("authorize group %s for publish: %v", groupSlug, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return "", false
	}
	if authz == nil || !authz.GetGroupExists() {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return "", false
	}
	if !isActiveGroupMember(authz) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "join this group to publish there"})
		return "", false
	}
	return audience.Coerce(rawAudience, authz.GetGroupVisibility()), true
}

func (asc *BlogServiceClient) acl() *blogacl.Checker {
	return &blogacl.Checker{Blogs: asc.Client, Groups: asc.Groups, Log: asc.log}
}
