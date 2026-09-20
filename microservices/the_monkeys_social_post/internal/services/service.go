package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_social_post/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_social_post/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_social_post/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service struct {
	pb.UnimplementedSocialPostServiceServer
	store *database.Store
}

func New(store *database.Store) *Service { return &Service{store: store} }

// --- shared helpers -------------------------------------------------------

var platformFromString = map[string]pb.Platform{
	"x": pb.Platform_PLATFORM_X, "linkedin": pb.Platform_PLATFORM_LINKEDIN,
	"instagram": pb.Platform_PLATFORM_INSTAGRAM, "facebook": pb.Platform_PLATFORM_FACEBOOK,
	"youtube": pb.Platform_PLATFORM_YOUTUBE, "tiktok": pb.Platform_PLATFORM_TIKTOK,
}

var platformToString = map[pb.Platform]string{
	pb.Platform_PLATFORM_X: "x", pb.Platform_PLATFORM_LINKEDIN: "linkedin",
	pb.Platform_PLATFORM_INSTAGRAM: "instagram", pb.Platform_PLATFORM_FACEBOOK: "facebook",
	pb.Platform_PLATFORM_YOUTUBE: "youtube", pb.Platform_PLATFORM_TIKTOK: "tiktok",
}

// resolveOwner maps the gateway-authenticated account id to the internal
// numeric owner id. It is the single point every mutating/reading RPC goes
// through, so ownership can never be established from client-supplied data.
func (s *Service) resolveOwner(ctx context.Context, reqCtx *pb.RequestContext) (int64, error) {
	if reqCtx == nil || strings.TrimSpace(reqCtx.GetAccountId()) == "" {
		return 0, status.Error(codes.Unauthenticated, "authenticated account is required")
	}
	userID, err := database.ResolveOwner(ctx, s.store.DB, reqCtx.GetAccountId())
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return 0, status.Error(codes.NotFound, "account not found")
		}
		return 0, status.Errorf(codes.Internal, "resolve owner: %v", err)
	}
	return userID, nil
}

func requireIdempotencyKey(reqCtx *pb.RequestContext) error {
	if reqCtx == nil || strings.TrimSpace(reqCtx.GetIdempotencyKey()) == "" {
		return status.Error(codes.InvalidArgument, "idempotency key is required")
	}
	return nil
}

// mapErr converts repository sentinel errors into the gRPC status codes the
// gateway already knows how to translate to HTTP.
func mapErr(err error, opDescription string) error {
	switch {
	case errors.Is(err, database.ErrNotFound):
		return status.Error(codes.NotFound, "resource not found")
	case errors.Is(err, database.ErrVersionConflict):
		return status.Error(codes.Aborted, "version conflict: reload and retry")
	default:
		return status.Errorf(codes.Internal, "%s: %v", opDescription, err)
	}
}

func (s *Service) toPBPost(ctx context.Context, p *database.Post) (*pb.SocialPost, error) {
	out := &pb.SocialPost{
		Id: p.ID, BaseText: p.BaseText, State: p.State, Version: p.Version,
		ScheduledAt: p.ScheduledAt.String, ScheduleTimezone: p.ScheduleTimezone,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if p.QueuePosition.Valid {
		out.QueuePosition = p.QueuePosition.Int64
	}
	renditions, err := database.LoadRenditions(ctx, s.store.DB, p.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load renditions: %v", err)
	}
	for _, r := range renditions {
		media, err := database.LoadRenditionMedia(ctx, s.store.DB, r.ID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "load rendition media: %v", err)
		}
		out.Renditions = append(out.Renditions, toPBRendition(r, media))
	}
	return out, nil
}

func toPBRendition(r *database.Rendition, media []*database.MediaRef) *pb.Rendition {
	out := &pb.Rendition{
		Id: r.ID, SocialAccountId: r.SocialAccountID, Platform: platformFromString[r.Platform],
		TextOverride: r.TextOverride.String, ScheduledAt: r.ScheduledAtOverride.String,
		ScheduleTimezone: r.ScheduleTimezoneOverride.String, State: r.State, Version: r.Version,
		ProviderPostRef: r.ProviderPostRef.String, LastErrorCode: r.LastErrorCode.String,
		LastErrorMessage: r.LastErrorMessage.String,
	}
	for _, m := range media {
		out.Media = append(out.Media, toPBMediaRef(m))
	}
	return out
}

func toPBMediaRef(m *database.MediaRef) *pb.MediaAsset {
	out := &pb.MediaAsset{
		Id: m.ID, ObjectKey: m.ObjectKey, Checksum: m.Checksum, ContentType: m.ContentType,
		ByteSize: m.ByteSize, MediaKind: m.MediaKind, SourceKind: m.SourceKind,
	}
	if m.Width.Valid {
		out.Width = int32(m.Width.Int64)
	}
	if m.Height.Valid {
		out.Height = int32(m.Height.Int64)
	}
	if m.DurationMs.Valid {
		out.DurationMs = m.DurationMs.Int64
	}
	return out
}

func toPBAsset(a *database.Asset) *pb.MediaAsset {
	out := &pb.MediaAsset{
		Id: a.ID, ObjectKey: a.ObjectKey, Checksum: a.Checksum, ContentType: a.ContentType,
		ByteSize: a.ByteSize, MediaKind: a.MediaKind, SourceKind: a.SourceKind,
	}
	if a.Width.Valid {
		out.Width = int32(a.Width.Int64)
	}
	if a.Height.Valid {
		out.Height = int32(a.Height.Int64)
	}
	if a.DurationMs.Valid {
		out.DurationMs = a.DurationMs.Int64
	}
	return out
}

// --- draft lifecycle --------------------------------------------------------

// CreatePost resolves the authenticated account id in the service, not the
// gateway, so ownership cannot be bypassed by direct gRPC.
func (s *Service) CreatePost(ctx context.Context, req *pb.CreatePostRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if err := requireIdempotencyKey(req.GetContext()); err != nil {
		return nil, err
	}
	p, created, err := database.CreatePostIdempotent(ctx, s.store.DB, userID, req.GetBaseText(), req.GetContext().GetIdempotencyKey())
	if err != nil {
		return nil, mapErr(err, "create social post")
	}
	if created {
		if err := database.RecordEvent(ctx, s.store.DB, p.ID, "", "", "user", userID, "post.created", req.GetContext().GetCorrelationId(), req.GetContext().GetIdempotencyKey(), ""); err != nil {
			return nil, status.Errorf(codes.Internal, "record social post creation: %v", err)
		}
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

func (s *Service) GetPost(ctx context.Context, req *pb.GetPostRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	p, err := database.GetPost(ctx, s.store.DB, userID, req.GetPostId())
	if err != nil {
		return nil, mapErr(err, "get social post")
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

func (s *Service) UpdatePost(ctx context.Context, req *pb.UpdatePostRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	p, err := database.UpdatePost(ctx, s.store.DB, userID, req.GetPostId(), req.GetBaseText(), req.GetExpectedVersion())
	if err != nil {
		return nil, mapErr(err, "update social post")
	}
	if err := database.RecordEvent(ctx, s.store.DB, p.ID, "", "", "user", userID, "post.updated", req.GetContext().GetCorrelationId(), req.GetContext().GetIdempotencyKey(), ""); err != nil {
		return nil, status.Errorf(codes.Internal, "record social post update: %v", err)
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

func (s *Service) DeleteDraft(ctx context.Context, req *pb.DeleteDraftRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if err := database.DeleteDraft(ctx, s.store.DB, userID, req.GetPostId(), req.GetExpectedVersion()); err != nil {
		if errors.Is(err, database.ErrNotFound) || errors.Is(err, database.ErrVersionConflict) {
			return nil, mapErr(err, "delete social post draft")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if err := database.RecordEvent(ctx, s.store.DB, req.GetPostId(), "", "", "user", userID, "post.deleted", req.GetContext().GetCorrelationId(), req.GetContext().GetIdempotencyKey(), ""); err != nil {
		return nil, status.Errorf(codes.Internal, "record social post deletion: %v", err)
	}
	return &pb.PostResponse{}, nil
}

func (s *Service) ListPosts(ctx context.Context, req *pb.ListPostsRequest) (*pb.ListPostsResponse, error) {
	return s.listPosts(ctx, req, database.ListFilter{})
}

func (s *Service) ListCalendar(ctx context.Context, req *pb.ListPostsRequest) (*pb.ListPostsResponse, error) {
	return s.listPosts(ctx, req, database.ListFilter{States: []string{"scheduled", "publishing", "published", "published_with_errors", "failed"}})
}

func (s *Service) ListQueue(ctx context.Context, req *pb.ListPostsRequest) (*pb.ListPostsResponse, error) {
	// Scope to posts still on their way out; once a post finishes
	// publishing (or fails terminally) it should drop off the "upcoming"
	// queue view even though its queue_position column is left set.
	return s.listPosts(ctx, req, database.ListFilter{
		States:       []string{"scheduled", "publishing"},
		QueueOnly:    true,
		OrderByQueue: true,
	})
}

func (s *Service) listPosts(ctx context.Context, req *pb.ListPostsRequest, base database.ListFilter) (*pb.ListPostsResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	f := base
	if len(req.GetStates()) > 0 {
		f.States = req.GetStates()
	}
	if req.GetPlatform() != pb.Platform_PLATFORM_UNSPECIFIED {
		f.Platform = platformToString[req.GetPlatform()]
	}
	f.From = req.GetFrom()
	f.To = req.GetTo()
	f.Limit = int(req.GetPageSize())

	posts, err := database.ListPosts(ctx, s.store.DB, userID, f)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list social posts: %v", err)
	}
	out := &pb.ListPostsResponse{}
	for _, p := range posts {
		pbPost, err := s.toPBPost(ctx, p)
		if err != nil {
			return nil, err
		}
		out.Posts = append(out.Posts, pbPost)
	}
	return out, nil
}

func (s *Service) ReorderQueue(ctx context.Context, req *pb.ReorderQueueRequest) (*pb.ReorderQueueResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if err := database.ReorderQueue(ctx, s.store.DB, userID, req.GetPostIdsInOrder()); err != nil {
		return nil, status.Errorf(codes.Internal, "reorder social post queue: %v", err)
	}
	posts, err := database.ListPosts(ctx, s.store.DB, userID, database.ListFilter{QueueOnly: true, OrderByQueue: true, Limit: 100})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list reordered queue: %v", err)
	}
	out := &pb.ReorderQueueResponse{}
	for _, p := range posts {
		pbPost, err := s.toPBPost(ctx, p)
		if err != nil {
			return nil, err
		}
		out.Posts = append(out.Posts, pbPost)
	}
	return out, nil
}

// --- renditions/media --------------------------------------------------------

func (s *Service) UpsertRendition(ctx context.Context, req *pb.UpsertRenditionRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	platform, err := database.GetAccountPlatform(ctx, s.store.DB, userID, req.GetSocialAccountId())
	if err != nil {
		return nil, mapErr(err, "resolve social account")
	}
	if req.GetTextOverride() != "" {
		if violation := models.ValidateText(platform, req.GetTextOverride()); violation != nil {
			return &pb.PostResponse{Violations: []*pb.FieldViolation{{
				Field: violation.Field, RuleId: violation.RuleID, Message: violation.Message,
				Actual: violation.Actual, Allowed: violation.Allowed,
			}}}, nil
		}
	}
	if _, err := database.UpsertRendition(ctx, s.store.DB, userID, req.GetPostId(), req.GetSocialAccountId(),
		req.GetTextOverride(), req.GetScheduledAtOverride(), req.GetScheduleTimezoneOverride(), req.GetExpectedVersion()); err != nil {
		return nil, mapErr(err, "upsert social post rendition")
	}
	if err := database.RecordEvent(ctx, s.store.DB, req.GetPostId(), "", "", "user", userID, "rendition.upserted", req.GetContext().GetCorrelationId(), req.GetContext().GetIdempotencyKey(), ""); err != nil {
		return nil, status.Errorf(codes.Internal, "record rendition upsert: %v", err)
	}
	p, err := database.GetPost(ctx, s.store.DB, userID, req.GetPostId())
	if err != nil {
		return nil, mapErr(err, "reload social post after rendition upsert")
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

func (s *Service) SetRenditionMedia(ctx context.Context, req *pb.SetRenditionMediaRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if err := database.SetRenditionMedia(ctx, s.store.DB, userID, req.GetPostId(), req.GetSocialAccountId(), req.GetAssetIds()); err != nil {
		return nil, mapErr(err, "set rendition media")
	}
	if err := database.RecordEvent(ctx, s.store.DB, req.GetPostId(), "", "", "user", userID, "rendition.media_set", req.GetContext().GetCorrelationId(), req.GetContext().GetIdempotencyKey(), ""); err != nil {
		return nil, status.Errorf(codes.Internal, "record rendition media set: %v", err)
	}
	p, err := database.GetPost(ctx, s.store.DB, userID, req.GetPostId())
	if err != nil {
		return nil, mapErr(err, "reload social post after media set")
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

// --- schedule/publish --------------------------------------------------------

func (s *Service) SchedulePost(ctx context.Context, req *pb.SchedulePostRequest) (*pb.PostResponse, error) {
	return s.applySchedule(ctx, req, database.SchedulePost)
}

func (s *Service) ReschedulePost(ctx context.Context, req *pb.SchedulePostRequest) (*pb.PostResponse, error) {
	return s.applySchedule(ctx, req, database.ReschedulePost)
}

func (s *Service) applySchedule(ctx context.Context, req *pb.SchedulePostRequest,
	op func(context.Context, *sql.DB, int64, string, string, string, int64) (*database.Post, error)) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetScheduledAt()) == "" || strings.TrimSpace(req.GetScheduleTimezone()) == "" {
		return nil, status.Error(codes.InvalidArgument, "scheduled_at and schedule_timezone are required")
	}
	if _, err := time.Parse(time.RFC3339, req.GetScheduledAt()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "scheduled_at must be an RFC3339 timestamp")
	}
	if _, err := time.LoadLocation(req.GetScheduleTimezone()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid schedule_timezone: %v", err)
	}
	p, err := op(ctx, s.store.DB, userID, req.GetPostId(), req.GetScheduledAt(), req.GetScheduleTimezone(), req.GetExpectedVersion())
	if err != nil {
		if errors.Is(err, database.ErrNotFound) || errors.Is(err, database.ErrVersionConflict) {
			return nil, mapErr(err, "apply social post schedule")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

func (s *Service) CancelSchedule(ctx context.Context, req *pb.PostActionRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	p, err := database.CancelSchedule(ctx, s.store.DB, userID, req.GetPostId(), req.GetExpectedVersion())
	if err != nil {
		if errors.Is(err, database.ErrNotFound) || errors.Is(err, database.ErrVersionConflict) {
			return nil, mapErr(err, "cancel social post schedule")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

func (s *Service) PublishNow(ctx context.Context, req *pb.PostActionRequest) (*pb.PostResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	p, err := database.PublishNow(ctx, s.store.DB, userID, req.GetPostId(), req.GetExpectedVersion())
	if err != nil {
		if errors.Is(err, database.ErrNotFound) || errors.Is(err, database.ErrVersionConflict) {
			return nil, mapErr(err, "publish social post now")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	out, err := s.toPBPost(ctx, p)
	if err != nil {
		return nil, err
	}
	return &pb.PostResponse{Post: out}, nil
}

// --- jobs ---------------------------------------------------------------

func (s *Service) GetJobStatus(ctx context.Context, req *pb.JobStatusRequest) (*pb.JobStatus, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	j, err := database.GetJobStatus(ctx, s.store.DB, userID, req.GetJobId())
	if err != nil {
		return nil, mapErr(err, "get social publish job status")
	}
	return toPBJobStatus(j), nil
}

func (s *Service) ReplayJob(ctx context.Context, req *pb.ReplayJobRequest) (*pb.JobStatus, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	j, err := database.ReplayJob(ctx, s.store.DB, userID, req.GetJobId())
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, mapErr(err, "replay social publish job")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return toPBJobStatus(j), nil
}

func toPBJobStatus(j *database.JobStatusRow) *pb.JobStatus {
	return &pb.JobStatus{
		Id: j.ID, PostId: j.PostID, RenditionId: j.RenditionID, Status: j.Status,
		AttemptCount: int32(j.AttemptCount), RunAt: j.RunAt,
		LastErrorCode: j.LastErrorCode.String, LastErrorMessage: j.LastErrorMessage.String,
	}
}

// --- accounts/validation metadata --------------------------------------------

func toPBSocialAccount(a *database.Account) *pb.SocialAccount {
	pbAccount := &pb.SocialAccount{
		Id: a.ID, Platform: platformFromString[a.Platform], DisplayName: a.DisplayName,
		Handle: a.Handle, Status: a.Status, IsMock: a.IsMock, AvatarUrl: a.AvatarURL,
	}
	if policy, ok := models.PlatformPolicies[a.Platform]; ok {
		pbAccount.Validation = &pb.ValidationMetadata{
			Platform: platformFromString[a.Platform], MaxTextCharacters: int32(policy.MaxTextCharacters),
			AllowedMediaKinds: policy.AllowedMediaKinds, MaxMediaCount: int32(policy.MaxMediaCount),
			MaxMediaBytes: policy.MaxMediaBytes, MaxVideoDurationMs: policy.MaxVideoDuration,
			MediaRequired: policy.MediaRequired,
		}
	}
	return pbAccount
}

func (s *Service) ListAccounts(ctx context.Context, req *pb.ListAccountsRequest) (*pb.ListAccountsResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	accounts, err := database.ListAccounts(ctx, s.store.DB, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list social accounts: %v", err)
	}
	out := &pb.ListAccountsResponse{}
	for _, a := range accounts {
		out.Accounts = append(out.Accounts, toPBSocialAccount(a))
	}
	return out, nil
}

func (s *Service) LinkAccount(ctx context.Context, req *pb.LinkAccountRequest) (*pb.SocialAccount, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	platform := strings.ToLower(strings.TrimSpace(req.GetPlatform()))
	if _, ok := platformFromString[platform]; !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid platform %q", req.GetPlatform())
	}
	handle := strings.TrimSpace(req.GetHandle())
	if handle == "" {
		return nil, status.Error(codes.InvalidArgument, "handle is required")
	}
	displayName := strings.TrimSpace(req.GetDisplayName())
	if displayName == "" {
		displayName = handle
	}
	externalRef := strings.TrimSpace(req.GetExternalAccountRef())
	if externalRef == "" {
		if req.GetIsMock() {
			externalRef = fmt.Sprintf("mock:%s:%s", platform, handle)
		} else {
			externalRef = fmt.Sprintf("%s:%s", platform, handle)
		}
	}

	a, err := database.LinkAccount(
		ctx, s.store.DB, userID, platform, handle, displayName,
		externalRef, req.GetAvatarUrl(), req.GetIsMock(),
		req.GetAccessToken(), req.GetRefreshToken(),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "link social account: %v", err)
	}
	return toPBSocialAccount(a), nil
}

func (s *Service) DisconnectAccount(ctx context.Context, req *pb.DisconnectAccountRequest) (*pb.DisconnectAccountResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	accountID := strings.TrimSpace(req.GetAccountId())
	if accountID == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	cancelledJobs, draftsReverted, err := database.DisconnectAccount(ctx, s.store.DB, userID, accountID)
	if err != nil {
		return nil, mapErr(err, "disconnect social account")
	}
	return &pb.DisconnectAccountResponse{
		Success:             true,
		CancelledJobsCount:  int32(cancelledJobs),
		DraftsRevertedCount: int32(draftsReverted),
	}, nil
}

func (s *Service) ListValidationMetadata(ctx context.Context, req *pb.ListValidationMetadataRequest) (*pb.ListValidationMetadataResponse, error) {
	if _, err := s.resolveOwner(ctx, req.GetContext()); err != nil {
		return nil, err
	}
	out := &pb.ListValidationMetadataResponse{}
	for platform, policy := range models.PlatformPolicies {
		out.Platforms = append(out.Platforms, &pb.ValidationMetadata{
			Platform: platformFromString[platform], MaxTextCharacters: int32(policy.MaxTextCharacters),
			AllowedMediaKinds: policy.AllowedMediaKinds, MaxMediaCount: int32(policy.MaxMediaCount),
			MaxMediaBytes: policy.MaxMediaBytes, MaxVideoDurationMs: policy.MaxVideoDuration,
			MediaRequired: policy.MediaRequired,
		})
	}
	return out, nil
}

// --- media assets -------------------------------------------------------

// ImportStudioAsset registers a Studio/Snapshot asset reference into the
// caller's social-media asset library. The gateway must have already
// verified ownership of source_asset_ref against the blog/storage service
// before invoking this RPC (see gateway routes.go); this call additionally
// scopes the resulting row to the resolved owner so a forged reference can
// never be attributed to another user.
func (s *Service) ImportStudioAsset(ctx context.Context, req *pb.ImportStudioAssetRequest) (*pb.ImportStudioAssetResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if req.GetSourceKind() != "snapshot" && req.GetSourceKind() != "card" && req.GetSourceKind() != "upload" {
		return nil, status.Error(codes.InvalidArgument, "source_kind must be snapshot, card, or upload")
	}
	a, err := database.ImportStudioAsset(ctx, s.store.DB, userID, req.GetSourceAssetRef(), req.GetSourceKind())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "import studio asset: %v", err)
	}
	return &pb.ImportStudioAssetResponse{Asset: toPBAsset(a)}, nil
}

func (s *Service) ListMediaAssets(ctx context.Context, req *pb.ListMediaAssetsRequest) (*pb.ListMediaAssetsResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	assets, err := database.ListMediaAssets(ctx, s.store.DB, userID, int(req.GetPageSize()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list social media assets: %v", err)
	}
	out := &pb.ListMediaAssetsResponse{}
	for _, a := range assets {
		out.Assets = append(out.Assets, toPBAsset(a))
	}
	return out, nil
}

func (s *Service) DeleteMediaAsset(ctx context.Context, req *pb.DeleteMediaAssetRequest) (*pb.DeleteMediaAssetResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	deleted, err := database.DeleteMediaAsset(ctx, s.store.DB, userID, req.GetAssetId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete social media asset: %v", err)
	}
	return &pb.DeleteMediaAssetResponse{Deleted: deleted}, nil
}

// --- history --------------------------------------------------------------

func (s *Service) ListPostHistory(ctx context.Context, req *pb.HistoryRequest) (*pb.HistoryResponse, error) {
	userID, err := s.resolveOwner(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	entries, err := database.ListHistory(ctx, s.store.DB, userID, req.GetPostId(), int(req.GetPageSize()))
	if err != nil {
		return nil, mapErr(err, "list social post history")
	}
	out := &pb.HistoryResponse{}
	for _, e := range entries {
		out.Events = append(out.Events, &pb.HistoryEvent{
			Id: e.ID, EventType: e.EventType, ActorType: e.ActorType, CreatedAt: e.CreatedAt, DetailJson: e.DetailJSON,
		})
	}
	return out, nil
}
