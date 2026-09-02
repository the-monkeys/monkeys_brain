package groups

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

// accountID returns the caller's account id, empty for anonymous requests.
// account_id is the immutable cross-service identity; it is the only key we
// authorize on, never a username.
func accountID(ctx *gin.Context) string {
	id, _ := ctx.Get("accountId")
	if s, ok := id.(string); ok {
		return s
	}
	return ""
}

// bind parses and validates a JSON body, writing a 400 on failure.
func bind[T any](ctx *gin.Context) (*T, bool) {
	var body T
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	return &body, true
}

// page reads the shared limit/offset pagination window.
func page(ctx *gin.Context) (int32, int32) {
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return int32(limit), int32(offset)
}

// -----------------------------------------------------------------------------
// Group CRUD
// -----------------------------------------------------------------------------

// ListGroups is a public discovery endpoint. The service scopes results to
// what the viewer may see, so an anonymous account id is legitimate.
func (gsc *GroupServiceClient) ListGroups(ctx *gin.Context) {
	limit, offset := page(ctx)

	req := &pb.ListGroupsReq{
		Limit:      limit,
		Offset:     offset,
		Status:     ctx.Query("status"),
		Country:    ctx.Query("country"),
		Region:     ctx.Query("region"),
		City:       ctx.Query("city"),
		Query:      ctx.Query("q"),
		AccountId:  accountID(ctx),
		Username:   ctx.Param("username"),
		ClientInfo: clientInfo(ctx),
	}

	if lat, err := strconv.ParseFloat(ctx.Query("user_lat"), 64); err == nil {
		req.UserLat = lat
	}
	if lng, err := strconv.ParseFloat(ctx.Query("user_lng"), 64); err == nil {
		req.UserLng = lng
	}
	if radius, err := strconv.Atoi(ctx.DefaultQuery("radius", "0")); err == nil {
		req.Radius = int32(radius)
	}

	if topics := ctx.QueryArray("topics"); len(topics) > 0 {
		req.Topics = topics
	}

	res, err := gsc.Client.ListGroups(ctx, req)
	if gsc.fail(ctx, err, "list groups") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// GetUserGroups lists the groups a user organizes or belongs to.
func (gsc *GroupServiceClient) GetUserGroups(ctx *gin.Context) {
	limit, offset := page(ctx)

	res, err := gsc.Client.GetUserGroups(ctx, &pb.ListGroupsReq{
		Limit:      limit,
		Offset:     offset,
		Status:     ctx.Query("status"),
		PublicOnly: ctx.Query("public_only") == "1" || ctx.Query("public_only") == "true",
		AccountId:  accountID(ctx),
		Username:   ctx.Param("username"),
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "get user groups") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) CreateGroup(ctx *gin.Context) {
	body, ok := bind[GroupBody](ctx)
	if !ok {
		return
	}

	res, err := gsc.Client.CreateGroup(ctx, &pb.CreateGroupReq{
		AccountId:   accountID(ctx),
		Name:        body.Name,
		Description: body.Description,
		Visibility:  body.Visibility,
		City:        body.City,
		Region:      body.Region,
		Country:     body.Country,
		Timezone:    body.Timezone,
		Latitude:    body.Latitude,
		Longitude:   body.Longitude,
		CoverImage:  body.CoverImage,
		LogoImage:   body.LogoImage,
		Topics:      body.Topics,
		ClientInfo:  clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "create group") {
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

// GetGroup serves a single group. AuthOptional supplies the viewer so the
// service can resolve the viewer's role and hide a private group from
// non-members.
func (gsc *GroupServiceClient) GetGroup(ctx *gin.Context) {
	res, err := gsc.Client.GetGroup(ctx, &pb.GetGroupReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "get group") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) UpdateGroup(ctx *gin.Context) {
	body, ok := bind[GroupBody](ctx)
	if !ok {
		return
	}

	res, err := gsc.Client.UpdateGroup(ctx, &pb.UpdateGroupReq{
		Slug:        ctx.Param("slug"),
		AccountId:   accountID(ctx),
		Name:        body.Name,
		Description: body.Description,
		Visibility:  body.Visibility,
		City:        body.City,
		Region:      body.Region,
		Country:     body.Country,
		Timezone:    body.Timezone,
		Latitude:    body.Latitude,
		Longitude:   body.Longitude,
		CoverImage:  body.CoverImage,
		LogoImage:   body.LogoImage,
		Topics:      body.Topics,
		ClientInfo:  clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "update group") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// action is the shared shape of the slug-scoped verbs.
func (gsc *GroupServiceClient) action(ctx *gin.Context) *pb.GroupActionReq {
	return &pb.GroupActionReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	}
}

func (gsc *GroupServiceClient) DeleteGroup(ctx *gin.Context) {
	res, err := gsc.Client.DeleteGroup(ctx, gsc.action(ctx))
	if gsc.fail(ctx, err, "delete group") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) PublishGroup(ctx *gin.Context) {
	res, err := gsc.Client.PublishGroup(ctx, gsc.action(ctx))
	if gsc.fail(ctx, err, "publish group") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// -----------------------------------------------------------------------------
// Membership
// -----------------------------------------------------------------------------

// JoinGroup submits a membership request. For a public group the service
// admits immediately; for private or restricted groups it records a pending
// request with the applicant's answers.
func (gsc *GroupServiceClient) JoinGroup(ctx *gin.Context) {
	// The body is optional: a public group needs no answers.
	var answersJSON string
	if ctx.Request.ContentLength != 0 {
		body, ok := bind[JoinBody](ctx)
		if !ok {
			return
		}
		if len(body.Answers) > 0 {
			raw, err := json.Marshal(body.Answers)
			if err != nil {
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid answers"})
				return
			}
			answersJSON = string(raw)
		}
	}

	res, err := gsc.Client.JoinGroup(ctx, &pb.JoinGroupReq{
		Slug:        ctx.Param("slug"),
		AccountId:   accountID(ctx),
		AnswersJson: answersJSON,
		ClientInfo:  clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "join group") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// LeaveGroup drops the caller's own membership.
func (gsc *GroupServiceClient) LeaveGroup(ctx *gin.Context) {
	res, err := gsc.Client.LeaveGroup(ctx, gsc.action(ctx))
	if gsc.fail(ctx, err, "leave group") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) ListMembers(ctx *gin.Context) {
	limit, offset := page(ctx)

	res, err := gsc.Client.ListMembers(ctx, &pb.ListGroupMembersReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		Limit:      limit,
		Offset:     offset,
		Role:       ctx.Query("role"),
		Status:     ctx.Query("status"),
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "list members") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) UpdateMemberRole(ctx *gin.Context) {
	body, ok := bind[MemberRoleBody](ctx)
	if !ok {
		return
	}

	res, err := gsc.Client.UpdateMemberRole(ctx, &pb.UpdateMemberRoleReq{
		Slug:           ctx.Param("slug"),
		AccountId:      accountID(ctx),
		TargetUsername: ctx.Param("username"),
		Role:           body.Role,
		ClientInfo:     clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "update member role") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) RemoveMember(ctx *gin.Context) {
	res, err := gsc.Client.RemoveMember(ctx, &pb.UpdateMemberRoleReq{
		Slug:           ctx.Param("slug"),
		AccountId:      accountID(ctx),
		TargetUsername: ctx.Param("username"),
		ClientInfo:     clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "remove member") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) BanMember(ctx *gin.Context) {
	// The reason is optional; an empty body bans without a recorded note.
	var reason string
	if ctx.Request.ContentLength != 0 {
		body, ok := bind[BanBody](ctx)
		if !ok {
			return
		}
		reason = body.Reason
	}

	res, err := gsc.Client.BanMember(ctx, &pb.UpdateMemberRoleReq{
		Slug:           ctx.Param("slug"),
		AccountId:      accountID(ctx),
		TargetUsername: ctx.Param("username"),
		Reason:         reason,
		ClientInfo:     clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "ban member") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// decision is the shared shape of approve/reject on a pending join request.
func (gsc *GroupServiceClient) decision(ctx *gin.Context) *pb.JoinDecisionReq {
	return &pb.JoinDecisionReq{
		Slug:           ctx.Param("slug"),
		AccountId:      accountID(ctx),
		TargetUsername: ctx.Param("username"),
		ClientInfo:     clientInfo(ctx),
	}
}

func (gsc *GroupServiceClient) ApproveJoinRequest(ctx *gin.Context) {
	res, err := gsc.Client.ApproveJoinRequest(ctx, gsc.decision(ctx))
	if gsc.fail(ctx, err, "approve join request") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) RejectJoinRequest(ctx *gin.Context) {
	res, err := gsc.Client.RejectJoinRequest(ctx, gsc.decision(ctx))
	if gsc.fail(ctx, err, "reject join request") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// -----------------------------------------------------------------------------
// Direct add + invite links
// -----------------------------------------------------------------------------

// AddMember enrolls a user directly as an active member, bypassing the pending
// queue. Staff-only; the service enforces manage_members.
func (gsc *GroupServiceClient) AddMember(ctx *gin.Context) {
	body, ok := bind[AddMemberBody](ctx)
	if !ok {
		return
	}

	res, err := gsc.Client.AddMember(ctx, &pb.AddMemberReq{
		Slug:           ctx.Param("slug"),
		AccountId:      accountID(ctx),
		TargetUsername: body.Username,
		Role:           body.Role,
		ClientInfo:     clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "add member") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// CreateInvite mints a shareable invite link. The body is optional: an empty
// body yields an unlimited, non-expiring member invite. Staff-only.
func (gsc *GroupServiceClient) CreateInvite(ctx *gin.Context) {
	var body InviteBody
	if ctx.Request.ContentLength != 0 {
		b, ok := bind[InviteBody](ctx)
		if !ok {
			return
		}
		body = *b
	}

	res, err := gsc.Client.CreateInvite(ctx, &pb.CreateInviteReq{
		Slug:           ctx.Param("slug"),
		AccountId:      accountID(ctx),
		Role:           body.Role,
		MaxUses:        body.MaxUses,
		ExpiresInHours: body.ExpiresInHours,
		ClientInfo:     clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "create invite") {
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

// ListInvites returns the group's invite links, newest first. Staff-only.
func (gsc *GroupServiceClient) ListInvites(ctx *gin.Context) {
	res, err := gsc.Client.ListInvites(ctx, &pb.ListInvitesReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "list invites") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// RevokeInvite disables an invite link. Staff-only.
func (gsc *GroupServiceClient) RevokeInvite(ctx *gin.Context) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid invite id"})
		return
	}

	res, err := gsc.Client.RevokeInvite(ctx, &pb.RevokeInviteReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		InviteId:   id,
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "revoke invite") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// GetInvite resolves an invite by token for the public accept page. The token
// is the capability, so this route is AuthOptional; the account id, when
// present, lets the service surface whether the viewer is already a member.
func (gsc *GroupServiceClient) GetInvite(ctx *gin.Context) {
	res, err := gsc.Client.GetInvite(ctx, &pb.GetInviteReq{
		Token:      ctx.Param("token"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "get invite") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// AcceptInvite admits the caller to a group using a valid invite token.
func (gsc *GroupServiceClient) AcceptInvite(ctx *gin.Context) {
	res, err := gsc.Client.AcceptInvite(ctx, &pb.AcceptInviteReq{
		Token:      ctx.Param("token"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "accept invite") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// -----------------------------------------------------------------------------
// Rules
// -----------------------------------------------------------------------------

func (gsc *GroupServiceClient) AddGroupRule(ctx *gin.Context) {
	body, ok := bind[RuleBody](ctx)
	if !ok {
		return
	}

	res, err := gsc.Client.AddGroupRule(ctx, &pb.GroupRuleReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		Title:      body.Title,
		Body:       body.Body,
		SortOrder:  body.SortOrder,
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "add group rule") {
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

func (gsc *GroupServiceClient) UpdateGroupRule(ctx *gin.Context) {
	id, ok := ruleID(ctx)
	if !ok {
		return
	}
	body, ok := bind[RuleBody](ctx)
	if !ok {
		return
	}

	res, err := gsc.Client.UpdateGroupRule(ctx, &pb.GroupRuleReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		RuleId:     id,
		Title:      body.Title,
		Body:       body.Body,
		SortOrder:  body.SortOrder,
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "update group rule") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (gsc *GroupServiceClient) DeleteGroupRule(ctx *gin.Context) {
	id, ok := ruleID(ctx)
	if !ok {
		return
	}

	res, err := gsc.Client.DeleteGroupRule(ctx, &pb.GroupRuleActionReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		RuleId:     id,
		ClientInfo: clientInfo(ctx),
	})
	if gsc.fail(ctx, err, "delete group rule") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// ruleID parses the numeric rule id from the path.
func ruleID(ctx *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid rule id"})
		return 0, false
	}
	return id, true
}
