package services

import (
	"context"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/discussions"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The gateway sets this metadata on POST /discussions/:id/hide.
// Hide stays on the existing delete RPC so the contract does not need a new proto generation.
const (
	discussionActionHeader = "x-discussion-action"
	discussionActionHide   = "hide"
)

// DiscussionService is the gRPC adapter over the discussions database.
type DiscussionService struct {
	pb.UnimplementedDiscussionServiceServer
	db database.DiscussionDB
}

func NewDiscussionService(db database.DiscussionDB) *DiscussionService {
	return &DiscussionService{db: db}
}

func (s *DiscussionService) CreateDiscussion(ctx context.Context, req *pb.CreateDiscussionReq) (*pb.DiscussionResp, error) {
	post, err := s.db.CreateDiscussion(ctx, discussions.CreateInput{
		AccountID: req.GetAccountId(),
		GroupSlug: req.GetGroupSlug(),
		Body:      req.GetBody(),
		Audience:  req.GetAudience(),
		Files:     filesFromProto(req.GetFiles()),
	})
	if err != nil {
		return nil, err
	}
	return &pb.DiscussionResp{Discussion: discussionProto(post)}, nil
}

func (s *DiscussionService) ListDiscussions(ctx context.Context, req *pb.ListDiscussionsReq) (*pb.ListDiscussionsResp, error) {
	posts, err := s.db.ListDiscussions(ctx, discussions.ListInput{
		AccountID:      req.GetAccountId(),
		GroupSlug:      req.GetGroupSlug(),
		BeforePublicID: req.GetBeforePublicId(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*pb.Discussion, 0, len(posts))
	for _, post := range posts {
		out = append(out, discussionProto(post))
	}
	return &pb.ListDiscussionsResp{Discussions: out}, nil
}

func (s *DiscussionService) GetDiscussion(ctx context.Context, req *pb.DiscussionActionReq) (*pb.DiscussionResp, error) {
	post, err := s.db.GetDiscussion(ctx, req.GetAccountId(), req.GetPublicId())
	if err != nil {
		return nil, err
	}
	return &pb.DiscussionResp{Discussion: discussionProto(post)}, nil
}

func (s *DiscussionService) ReplyToDiscussion(ctx context.Context, req *pb.ReplyDiscussionReq) (*pb.ReplyDiscussionResp, error) {
	reply, err := s.db.ReplyToDiscussion(ctx, discussions.ReplyInput{
		AccountID:      req.GetAccountId(),
		PostPublicID:   req.GetPublicId(),
		ParentPublicID: req.GetParentPublicId(),
		Body:           req.GetBody(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.ReplyDiscussionResp{Reply: replyProto(*reply)}, nil
}

func (s *DiscussionService) LikeDiscussion(ctx context.Context, req *pb.DiscussionActionReq) (*pb.LikeDiscussionResp, error) {
	liked, err := s.db.LikeDiscussion(ctx, req.GetAccountId(), req.GetPublicId())
	if err != nil {
		return nil, err
	}
	return &pb.LikeDiscussionResp{Liked: liked}, nil
}

func (s *DiscussionService) EditDiscussion(ctx context.Context, req *pb.EditDiscussionReq) (*pb.DiscussionResp, error) {
	post, err := s.db.EditDiscussion(ctx, req.GetAccountId(), req.GetPublicId(), req.GetBody())
	if err != nil {
		return nil, err
	}
	return &pb.DiscussionResp{Discussion: discussionProto(post)}, nil
}

func (s *DiscussionService) DeleteDiscussion(ctx context.Context, req *pb.DiscussionActionReq) (*pb.DiscussionResp, error) {
	var err error
	if discussionAction(ctx) == discussionActionHide {
		err = s.db.HideDiscussion(ctx, req.GetAccountId(), req.GetPublicId())
	} else {
		err = s.db.DeleteDiscussion(ctx, req.GetAccountId(), req.GetPublicId())
	}
	if err != nil {
		return nil, err
	}
	return s.GetDiscussion(ctx, req)
}

func discussionAction(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(discussionActionHeader)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func filesFromProto(in []*pb.DiscussionFile) []discussions.File {
	out := make([]discussions.File, 0, len(in))
	for _, f := range in {
		if f == nil {
			continue
		}
		out = append(out, discussions.File{StorageKey: f.GetStorageKey(), ContentType: f.GetContentType()})
	}
	return out
}

func discussionProto(p *discussions.Post) *pb.Discussion {
	if p == nil {
		return nil
	}
	files := make([]*pb.DiscussionFile, 0, len(p.Files))
	for _, f := range p.Files {
		files = append(files, &pb.DiscussionFile{StorageKey: f.StorageKey, ContentType: f.ContentType})
	}
	replies := make([]*pb.DiscussionReply, 0, len(p.Replies))
	for _, r := range p.Replies {
		replies = append(replies, replyProto(r))
	}
	return &pb.Discussion{
		PublicId:       p.PublicID,
		Body:           p.Body,
		Status:         p.Status,
		AuthorUsername: p.AuthorUsername,
		AuthorGone:     p.AuthorGone,
		GroupSlug:      p.GroupSlug,
		Audience:       p.Audience,
		ReplyCount:     p.ReplyCount,
		LikeCount:      p.LikeCount,
		Liked:          p.Liked,
		CreatedAt:      timestamppb.New(p.CreatedAt),
		EditedUntil:    timestamppb.New(p.EditedUntil),
		Files:          files,
		Replies:        replies,
	}
}

func replyProto(r discussions.Reply) *pb.DiscussionReply {
	return &pb.DiscussionReply{
		PublicId:       r.PublicID,
		ParentPublicId: r.ParentPublicID,
		Body:           r.Body,
		Status:         r.Status,
		AuthorUsername: r.AuthorUsername,
		AuthorGone:     r.AuthorGone,
		CreatedAt:      timestamppb.New(r.CreatedAt),
	}
}
