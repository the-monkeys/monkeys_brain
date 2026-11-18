package services

import (
	"context"
	"encoding/json"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/common/homepage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

type homepageReader interface {
	GetHomepageBlogs(context.Context, int, int, []string) ([]map[string]interface{}, bool, error)
}

func homepageRequested(ctx context.Context) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get(homepage.MetadataKey)
	return len(values) == 1 && values[0] == homepage.Version
}

func (blog *BlogService) getHomepageMetadata(req *pb.BlogListReq, stream pb.BlogService_GetBlogsMetadataServer) error {
	if req.Limit < 1 || req.Limit > 50 || req.Offset < 0 || req.Offset > 1000 || req.AccountId != "" || req.IsDraft || req.IsScheduled || req.GroupSlug != "" || req.IncludeGroupOnly || len(req.Tags) > 0 {
		return status.Error(codes.InvalidArgument, "homepage requires public catalog pagination")
	}
	reader, ok := blog.osClient.(homepageReader)
	if !ok {
		return status.Error(codes.Unavailable, "homepage policy is unavailable")
	}
	rows, more, err := reader.GetHomepageBlogs(stream.Context(), int(req.Limit), int(req.Offset), req.BlogIds)
	if err != nil {
		if stream.Context().Err() != nil {
			return status.FromContextError(stream.Context().Err()).Err()
		}
		return status.Error(codes.Unavailable, "homepage catalog is unavailable")
	}
	if rows == nil {
		rows = []map[string]interface{}{}
	}
	body, err := json.Marshal(map[string]interface{}{"blogs": rows, "has_more": more, "homepage_policy_version": homepage.Version})
	if err != nil {
		return status.Error(codes.Internal, "cannot encode homepage catalog")
	}
	return stream.Send(&anypb.Any{TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse", Value: body})
}
