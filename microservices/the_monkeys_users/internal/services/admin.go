package services

import (
	"context"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (us *UserSvc) AdminListUsers(ctx context.Context, req *pb.AdminListUsersReq) (*pb.AdminListUsersResp, error) {
	users, total, err := us.dbConn.AdminListUsers(ctx, req.GetQuery(), req.GetLimit(), req.GetOffset())
	if err != nil {
		return nil, err
	}
	return &pb.AdminListUsersResp{Users: users, Total: total}, nil
}

func (us *UserSvc) AdminSetUserRole(ctx context.Context, req *pb.AdminSetUserRoleReq) (*pb.AdminUserActionResp, error) {
	if err := us.dbConn.AdminSetUserRole(ctx, req.GetUsername(), req.GetRole(), req.GetActor()); err != nil {
		return nil, err
	}
	return &pb.AdminUserActionResp{Message: "role updated", Success: true}, nil
}

func (us *UserSvc) AdminFlagUser(ctx context.Context, req *pb.AdminFlagUserReq) (*pb.AdminUserActionResp, error) {
	if err := us.dbConn.AdminFlagUser(ctx, req.GetUsername(), req.GetFlagType(), req.GetReason(), req.GetActor()); err != nil {
		return nil, err
	}
	return &pb.AdminUserActionResp{Message: "flagged", Success: true}, nil
}

func (us *UserSvc) AdminUnflagUser(ctx context.Context, req *pb.AdminFlagUserReq) (*pb.AdminUserActionResp, error) {
	if err := us.dbConn.AdminUnflagUser(ctx, req.GetUsername(), req.GetFlagType(), req.GetReason(), req.GetActor()); err != nil {
		return nil, err
	}
	return &pb.AdminUserActionResp{Message: "unflagged", Success: true}, nil
}

func (us *UserSvc) AdminSuspendUser(ctx context.Context, req *pb.AdminSuspendUserReq) (*pb.AdminUserActionResp, error) {
	if err := us.dbConn.AdminSuspendUser(ctx, req.GetUsername(), req.GetReason(), req.GetActor()); err != nil {
		return nil, err
	}
	return &pb.AdminUserActionResp{Message: "suspended", Success: true}, nil
}

func (us *UserSvc) AdminDeleteUser(ctx context.Context, req *pb.AdminDeleteUserReq) (*pb.DeleteUserProfileRes, error) {
	username := strings.TrimSpace(req.GetUsername())
	if username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := us.dbConn.AdminWriteAudit(ctx, &pb.AdminWriteAuditReq{
		Action: "user.delete", EntityType: "user", EntityId: username, Actor: req.GetActor(),
	}); err != nil {
		us.log.Warnw("admin delete audit failed", "err", err)
	}
	return us.DeleteUserAccount(ctx, &pb.DeleteUserProfileReq{Username: username})
}

func (us *UserSvc) AdminUserStats(ctx context.Context, _ *pb.AdminEmpty) (*pb.AdminUserStatsResp, error) {
	return us.dbConn.AdminUserStats(ctx)
}

func (us *UserSvc) AdminListBlogs(ctx context.Context, req *pb.AdminListBlogsReq) (*pb.AdminListBlogsResp, error) {
	blogs, total, err := us.dbConn.AdminListBlogs(ctx, req.GetQuery(), req.GetStatus(), req.GetLimit(), req.GetOffset())
	if err != nil {
		return nil, err
	}
	return &pb.AdminListBlogsResp{Blogs: blogs, Total: total}, nil
}

func (us *UserSvc) AdminBlogStats(ctx context.Context, _ *pb.AdminEmpty) (*pb.AdminBlogStatsResp, error) {
	return us.dbConn.AdminBlogStats(ctx)
}

func (us *UserSvc) AdminMissingBlogIds(ctx context.Context, req *pb.AdminMissingBlogIdsReq) (*pb.AdminMissingBlogIdsResp, error) {
	ids, err := us.dbConn.AdminMissingBlogIds(ctx, req.GetBlogIds())
	if err != nil {
		return nil, err
	}
	return &pb.AdminMissingBlogIdsResp{BlogIds: ids}, nil
}

func (us *UserSvc) AdminWriteAudit(ctx context.Context, req *pb.AdminWriteAuditReq) (*pb.AdminUserActionResp, error) {
	if err := us.dbConn.AdminWriteAudit(ctx, req); err != nil {
		return nil, err
	}
	return &pb.AdminUserActionResp{Message: "audited", Success: true}, nil
}
