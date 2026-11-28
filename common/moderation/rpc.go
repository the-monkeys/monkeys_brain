package moderation

import (
	"context"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
)

type Backend interface {
	Check(context.Context, *pb.CheckRequest) (*pb.CheckResponse, error)
	ReportContent(context.Context, *pb.ActionRequest) (*pb.ActionResponse, error)
	Reveal(context.Context, *pb.ActionRequest) (*pb.ActionResponse, error)
	SetAuthorLabel(context.Context, *pb.ActionRequest) (*pb.ActionResponse, error)
	ListReports(context.Context, *pb.ListRequest) (*pb.ListResponse, error)
	ReviewReport(context.Context, *pb.ReviewRequest) (*pb.ActionResponse, error)
	CheckMedia(context.Context, *pb.MediaRequest) (*pb.MediaResponse, error)
}
type Server struct {
	pb.UnimplementedModerationServiceServer
	store Backend
}

func NewServer(store Backend) *Server { return &Server{store: store} }
func (s *Server) Check(c context.Context, r *pb.CheckRequest) (*pb.CheckResponse, error) {
	return s.store.Check(c, r)
}
func (s *Server) ReportContent(c context.Context, r *pb.ActionRequest) (*pb.ActionResponse, error) {
	return s.store.ReportContent(c, r)
}
func (s *Server) Reveal(c context.Context, r *pb.ActionRequest) (*pb.ActionResponse, error) {
	return s.store.Reveal(c, r)
}
func (s *Server) SetAuthorLabel(c context.Context, r *pb.ActionRequest) (*pb.ActionResponse, error) {
	return s.store.SetAuthorLabel(c, r)
}
func (s *Server) ListReports(c context.Context, r *pb.ListRequest) (*pb.ListResponse, error) {
	return s.store.ListReports(c, r)
}
func (s *Server) ReviewReport(c context.Context, r *pb.ReviewRequest) (*pb.ActionResponse, error) {
	return s.store.ReviewReport(c, r)
}
func (s *Server) CheckMedia(c context.Context, r *pb.MediaRequest) (*pb.MediaResponse, error) {
	return s.store.CheckMedia(c, r)
}
