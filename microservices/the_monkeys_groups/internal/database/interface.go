package database

import (
	"context"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

// GroupDB is the persistence contract for the groups (communities) service.
// Writes translate the wire protocol's account_id / username into numeric
// user_account.id values internally; callers only ever speak the public
// identifiers.
type GroupDB interface {
	// Group lifecycle
	CreateGroup(ctx context.Context, req *pb.CreateGroupReq) (*pb.Group, error)
	UpdateGroup(ctx context.Context, req *pb.UpdateGroupReq) (*pb.Group, error)
	SetGroupStatus(ctx context.Context, slug, accountID, status string) (*pb.Group, error)
	DeleteGroup(ctx context.Context, req *pb.GroupActionReq) error
	GetGroup(ctx context.Context, req *pb.GetGroupReq) (*pb.Group, error)

	// Discovery
	ListGroups(ctx context.Context, req *pb.ListGroupsReq) ([]*pb.Group, int32, error)
	GetUserGroups(ctx context.Context, req *pb.ListGroupsReq) ([]*pb.Group, int32, error)

	// Membership
	JoinGroup(ctx context.Context, req *pb.JoinGroupReq) (*pb.GroupMember, error)
	LeaveGroup(ctx context.Context, req *pb.GroupActionReq) error
	ListMembers(ctx context.Context, req *pb.ListGroupMembersReq) ([]*pb.GroupMember, int32, error)
	UpdateMemberRole(ctx context.Context, req *pb.UpdateMemberRoleReq) error
	RemoveMember(ctx context.Context, req *pb.UpdateMemberRoleReq) error
	BanMember(ctx context.Context, req *pb.UpdateMemberRoleReq) error
	ApproveJoinRequest(ctx context.Context, req *pb.JoinDecisionReq) error
	RejectJoinRequest(ctx context.Context, req *pb.JoinDecisionReq) error

	// Direct add + invite links
	AddMember(ctx context.Context, req *pb.AddMemberReq) error
	CreateInvite(ctx context.Context, req *pb.CreateInviteReq) (*pb.GroupInvite, error)
	ListInvites(ctx context.Context, req *pb.ListInvitesReq) ([]*pb.GroupInvite, error)
	RevokeInvite(ctx context.Context, req *pb.RevokeInviteReq) error
	GetInvite(ctx context.Context, req *pb.GetInviteReq) (*pb.GroupInvite, error)
	AcceptInvite(ctx context.Context, req *pb.AcceptInviteReq) error

	// Rules
	AddGroupRule(ctx context.Context, req *pb.GroupRuleReq) (*pb.GroupRule, error)
	UpdateGroupRule(ctx context.Context, req *pb.GroupRuleReq) (*pb.GroupRule, error)
	DeleteGroupRule(ctx context.Context, req *pb.GroupRuleActionReq) error

	// Authorization
	AuthorizeGroup(ctx context.Context, req *pb.AuthorizeGroupReq) (*pb.AuthorizeGroupResp, error)

	Close() error
}
