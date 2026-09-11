package services

import (
	"context"
	"encoding/json"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/common/interservice"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_groups/internal/database"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const storageRoutingKey = 0

// GroupService is the gRPC surface for communities. It owns business
// orchestration and response shaping; the database layer owns persistence and
// the final, authoritative permission checks. Every request identifies its
// caller by account_id, the immutable cross-service user identifier — usernames
// are display handles and may change, so they are never used for authorization.
type GroupService struct {
	pb.UnimplementedGroupServiceServer
	db    database.GroupDB
	log   *zap.SugaredLogger
	cfg   *config.Config
	qConn *rabbitmq.ConnManager
}

func NewGroupService(db database.GroupDB, log *zap.SugaredLogger, cfg *config.Config, qConn *rabbitmq.ConnManager) *GroupService {
	return &GroupService{db: db, log: log, cfg: cfg, qConn: qConn}
}

// -----------------------------------------------------------------------------
// Group lifecycle
// -----------------------------------------------------------------------------

func (s *GroupService) CreateGroup(ctx context.Context, req *pb.CreateGroupReq) (*pb.GroupResp, error) {
	group, err := s.db.CreateGroup(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.GroupResp{Message: "group created as draft", Group: group}, nil
}

func (s *GroupService) UpdateGroup(ctx context.Context, req *pb.UpdateGroupReq) (*pb.GroupResp, error) {
	group, err := s.db.UpdateGroup(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.GroupResp{Message: "group updated", Group: group}, nil
}

// PublishGroup transitions a draft community to published so it becomes
// discoverable and open to joins.
func (s *GroupService) PublishGroup(ctx context.Context, req *pb.GroupActionReq) (*pb.GroupResp, error) {
	group, err := s.db.SetGroupStatus(ctx, req.Slug, req.AccountId, "published")
	if err != nil {
		return nil, err
	}
	return &pb.GroupResp{Message: "group published", Group: group}, nil
}

// DeleteGroup removes a community. The database layer refuses deletion while the
// group still has captured or pending paid events.
func (s *GroupService) DeleteGroup(ctx context.Context, req *pb.GroupActionReq) (*pb.BasicResp, error) {
	if err := s.db.DeleteGroup(ctx, req); err != nil {
		return nil, err
	}
	s.publishStorageDelete(req.GetSlug())
	return &pb.BasicResp{Message: "group deleted", Success: true}, nil
}

func (s *GroupService) AdminDeleteGroup(ctx context.Context, req *pb.AdminSuspendGroupReq) (*pb.BasicResp, error) {
	if err := s.db.AdminDeleteGroup(ctx, req); err != nil {
		return nil, err
	}
	s.publishStorageDelete(req.GetSlug())
	return &pb.BasicResp{Message: "group deleted", Success: true}, nil
}

func (s *GroupService) CheckUserGroupRemoval(ctx context.Context, req *pb.AccountIdReq) (*pb.UserRemovalCheckResp, error) {
	slugs, err := s.db.CheckUserGroupRemoval(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if len(slugs) == 0 {
		return &pb.UserRemovalCheckResp{Allowed: true}, nil
	}
	return &pb.UserRemovalCheckResp{
		Allowed:       false,
		Reason:        "account created a group that still has captured or pending paid events; cancel or settle them first",
		BlockingSlugs: slugs,
	}, nil
}

func (s *GroupService) RemoveUserFromGroups(ctx context.Context, req *pb.AccountIdReq) (*pb.BasicResp, error) {
	slugs, err := s.db.RemoveUserFromGroups(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	for _, slug := range slugs {
		s.publishStorageDelete(slug)
	}
	return &pb.BasicResp{Message: "user removed from groups", Success: true}, nil
}

func (s *GroupService) publishStorageDelete(slug string) {
	if s.qConn == nil || s.cfg == nil || len(s.cfg.RabbitMQ.RoutingKeys) <= storageRoutingKey {
		return
	}
	body, err := json.Marshal(interservice.Message{
		Action:    constants.GROUP_DELETE,
		GroupSlug: slug,
	})
	if err != nil {
		s.log.Errorw("failed to marshal group delete", "slug", slug, "err", err)
		return
	}
	rk := s.cfg.RabbitMQ.RoutingKeys[storageRoutingKey]
	go func() {
		if err := s.qConn.PublishReliable(s.cfg.RabbitMQ.Exchange, rk, body, s.cfg.RabbitMQ.MaxRetries); err != nil {
			s.log.Errorw("failed to publish group delete to storage", "slug", slug, "routing_key", rk, "err", err)
		}
	}()
}

func (s *GroupService) GetGroup(ctx context.Context, req *pb.GetGroupReq) (*pb.GroupResp, error) {
	group, err := s.db.GetGroup(ctx, req)
	if err != nil {
		return nil, err
	}
	if !groupVisibleToViewer(group) {
		return nil, status.Error(codes.NotFound, "group not found")
	}
	return &pb.GroupResp{Group: group}, nil
}

func groupVisibleToViewer(g *pb.Group) bool {
	if g == nil {
		return false
	}
	role := g.ViewerRole
	if role == "organizer" || role == "co_organizer" || role == "moderator" {
		return true
	}
	if g.Status != "published" {
		return false
	}
	if g.Visibility == "public" || g.Visibility == "unlisted" {
		return true
	}
	return g.ViewerMemberStatus == "active"
}

// -----------------------------------------------------------------------------
// Discovery
// -----------------------------------------------------------------------------

func (s *GroupService) ListGroups(ctx context.Context, req *pb.ListGroupsReq) (*pb.ListGroupsResp, error) {
	groups, total, err := s.db.ListGroups(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.ListGroupsResp{Groups: groups, Total: total}, nil
}

func (s *GroupService) GetUserGroups(ctx context.Context, req *pb.ListGroupsReq) (*pb.ListGroupsResp, error) {
	groups, total, err := s.db.GetUserGroups(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.ListGroupsResp{Groups: groups, Total: total}, nil
}

// -----------------------------------------------------------------------------
// Membership
// -----------------------------------------------------------------------------

// JoinGroup admits the caller to a public group immediately or files a join
// request for a private/unlisted one. The response message reflects which path
// the group's visibility produced.
func (s *GroupService) JoinGroup(ctx context.Context, req *pb.JoinGroupReq) (*pb.BasicResp, error) {
	member, err := s.db.JoinGroup(ctx, req)
	if err != nil {
		return nil, err
	}
	msg := "joined group"
	if member.Status == "pending" {
		msg = "join request submitted"
	}
	s.notifyStaff(ctx, req.Slug, member.Username, groupJoinAction(member.Status))
	return &pb.BasicResp{Message: msg, Success: true}, nil
}

func (s *GroupService) LeaveGroup(ctx context.Context, req *pb.GroupActionReq) (*pb.BasicResp, error) {
	if err := s.db.LeaveGroup(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "left group", Success: true}, nil
}

func (s *GroupService) ListMembers(ctx context.Context, req *pb.ListGroupMembersReq) (*pb.ListGroupMembersResp, error) {
	members, total, err := s.db.ListMembers(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.ListGroupMembersResp{Members: members, Total: total}, nil
}

func (s *GroupService) UpdateMemberRole(ctx context.Context, req *pb.UpdateMemberRoleReq) (*pb.BasicResp, error) {
	if err := s.db.UpdateMemberRole(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "member role updated", Success: true}, nil
}

func (s *GroupService) RemoveMember(ctx context.Context, req *pb.UpdateMemberRoleReq) (*pb.BasicResp, error) {
	if err := s.db.RemoveMember(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "member removed", Success: true}, nil
}

func (s *GroupService) BanMember(ctx context.Context, req *pb.UpdateMemberRoleReq) (*pb.BasicResp, error) {
	if err := s.db.BanMember(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "member banned", Success: true}, nil
}

func (s *GroupService) ApproveJoinRequest(ctx context.Context, req *pb.JoinDecisionReq) (*pb.BasicResp, error) {
	if err := s.db.ApproveJoinRequest(ctx, req); err != nil {
		return nil, err
	}
	s.notify(groupNotification{
		Username:    s.actorUsername(ctx, req.AccountId),
		NewUsername: req.TargetUsername,
		Action:      constants.GROUP_JOIN_APPROVED,
		GroupSlug:   req.Slug,
		GroupName:   s.groupName(ctx, req.Slug),
	})
	return &pb.BasicResp{Message: "join request approved", Success: true}, nil
}

func (s *GroupService) RejectJoinRequest(ctx context.Context, req *pb.JoinDecisionReq) (*pb.BasicResp, error) {
	if err := s.db.RejectJoinRequest(ctx, req); err != nil {
		return nil, err
	}
	s.notify(groupNotification{
		Username:    s.actorUsername(ctx, req.AccountId),
		NewUsername: req.TargetUsername,
		Action:      constants.GROUP_JOIN_REJECTED,
		GroupSlug:   req.Slug,
		GroupName:   s.groupName(ctx, req.Slug),
	})
	return &pb.BasicResp{Message: "join request rejected", Success: true}, nil
}

// -----------------------------------------------------------------------------
// Direct add + invite links
// -----------------------------------------------------------------------------

// AddMember enrolls a user directly as an active member. Staff-only; the
// database layer enforces the manage_members permission.
func (s *GroupService) AddMember(ctx context.Context, req *pb.AddMemberReq) (*pb.BasicResp, error) {
	if err := s.db.AddMember(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "member added", Success: true}, nil
}

// CreateInvite mints a shareable invite link. Staff-only.
func (s *GroupService) CreateInvite(ctx context.Context, req *pb.CreateInviteReq) (*pb.InviteResp, error) {
	invite, err := s.db.CreateInvite(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.InviteResp{Message: "invite created", Invite: invite}, nil
}

// ListInvites returns the group's invite links. Staff-only.
func (s *GroupService) ListInvites(ctx context.Context, req *pb.ListInvitesReq) (*pb.ListInvitesResp, error) {
	invites, err := s.db.ListInvites(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.ListInvitesResp{Invites: invites}, nil
}

// RevokeInvite disables an invite link. Staff-only.
func (s *GroupService) RevokeInvite(ctx context.Context, req *pb.RevokeInviteReq) (*pb.BasicResp, error) {
	if err := s.db.RevokeInvite(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "invite revoked", Success: true}, nil
}

// GetInvite resolves an invite by token for the public accept page.
func (s *GroupService) GetInvite(ctx context.Context, req *pb.GetInviteReq) (*pb.InviteResp, error) {
	invite, err := s.db.GetInvite(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.InviteResp{Invite: invite}, nil
}

// AcceptInvite admits the caller to a group using a valid invite token.
func (s *GroupService) AcceptInvite(ctx context.Context, req *pb.AcceptInviteReq) (*pb.BasicResp, error) {
	if err := s.db.AcceptInvite(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "joined group", Success: true}, nil
}

// -----------------------------------------------------------------------------
// Rules
// -----------------------------------------------------------------------------

func (s *GroupService) AddGroupRule(ctx context.Context, req *pb.GroupRuleReq) (*pb.GroupRuleResp, error) {
	rule, err := s.db.AddGroupRule(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.GroupRuleResp{Message: "rule added", Rule: rule}, nil
}

func (s *GroupService) UpdateGroupRule(ctx context.Context, req *pb.GroupRuleReq) (*pb.GroupRuleResp, error) {
	rule, err := s.db.UpdateGroupRule(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.GroupRuleResp{Message: "rule updated", Rule: rule}, nil
}

func (s *GroupService) DeleteGroupRule(ctx context.Context, req *pb.GroupRuleActionReq) (*pb.BasicResp, error) {
	if err := s.db.DeleteGroupRule(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "rule deleted", Success: true}, nil
}

func (s *GroupService) AdminListGroups(ctx context.Context, req *pb.AdminListGroupsReq) (*pb.AdminListGroupsResp, error) {
	return s.db.AdminListGroups(ctx, req)
}

func (s *GroupService) AdminSuspendGroup(ctx context.Context, req *pb.AdminSuspendGroupReq) (*pb.GroupResp, error) {
	g, err := s.db.AdminSuspendGroup(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.GroupResp{Message: "group suspended", Group: g}, nil
}

func (s *GroupService) AdminGroupStats(ctx context.Context, _ *pb.AdminEmpty) (*pb.AdminGroupStatsResp, error) {
	return s.db.AdminGroupStats(ctx)
}
