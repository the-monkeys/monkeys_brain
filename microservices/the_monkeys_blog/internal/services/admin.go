package services

import (
	"context"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (blog *BlogService) AdminListESBlogIds(ctx context.Context, req *pb.AdminListESBlogIdsReq) (*pb.AdminListESBlogIdsResp, error) {
	ids, total, err := blog.osClient.ListBlogIDs(ctx, req.GetLimit(), req.GetOffset())
	if err != nil {
		blog.logger.Errorw("admin list es blog ids failed", "err", err)
		return nil, status.Error(codes.Internal, "failed to list elasticsearch blog ids")
	}
	return &pb.AdminListESBlogIdsResp{BlogIds: ids, Total: int32(total)}, nil
}
