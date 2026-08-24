package database

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// allowedReactions bounds what can be stored so the column stays a small,
// indexable set rather than free text from clients.
var allowedReactions = map[string]struct{}{
	"like": {}, "love": {}, "celebrate": {}, "insightful": {}, "curious": {},
}

// CommentResult adds the organizer's username so the caller can notify them.
type CommentResult struct {
	Comment           *pb.EventComment
	EventTitle        string
	OrganizerUsername string
}

func (db *eventDB) AddComment(ctx context.Context, req *pb.AddCommentReq) (*CommentResult, error) {
	text := strings.TrimSpace(req.CommentText)
	if text == "" {
		return nil, status.Error(codes.InvalidArgument, "comment text is required")
	}
	if len(text) > 2000 {
		return nil, status.Error(codes.InvalidArgument, "comment is too long")
	}

	userID, err := resolveAccount(ctx, db.db, req.AccountId)
	if err != nil {
		return nil, err
	}

	var eventID int64
	out := &CommentResult{Comment: &pb.EventComment{AccountId: req.AccountId, CommentText: text}}
	if err := db.db.QueryRowContext(ctx, `
		SELECT e.id, e.title, u.username FROM events e
		JOIN user_account u ON u.id = e.organizer_id WHERE e.slug = $1`,
		req.EventSlug).Scan(&eventID, &out.EventTitle, &out.OrganizerUsername); err != nil {
		if err == sql.ErrNoRows {
			return nil, status.Error(codes.NotFound, "event not found")
		}
		return nil, status.Error(codes.Internal, "failed to load event")
	}

	var created time.Time
	if err := db.db.QueryRowContext(ctx, `
		INSERT INTO event_comments (event_id, user_id, comment_text)
		VALUES ($1,$2,$3) RETURNING id, created_at`,
		eventID, userID, text).Scan(&out.Comment.Id, &created); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add comment: %v", err)
	}

	if err := db.db.QueryRowContext(ctx,
		"SELECT username FROM user_account WHERE id = $1", userID).Scan(&out.Comment.UserName); err != nil {
		return nil, status.Error(codes.Internal, "failed to load commenter")
	}

	out.Comment.EventId = eventID
	out.Comment.CreatedAt = timestamppb.New(created)
	return out, nil
}

func (db *eventDB) ListComments(ctx context.Context, req *pb.ListCommentsReq) ([]*pb.EventComment, int32, error) {
	eventID, _, err := resolveEvent(ctx, db.db, req.EventSlug)
	if err != nil {
		return nil, 0, err
	}

	var total int32
	if err := db.db.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM event_comments WHERE event_id = $1", eventID).Scan(&total); err != nil {
		return nil, 0, status.Errorf(codes.Internal, "failed to count comments: %v", err)
	}

	limit := int32(50)
	if req.Limit > 0 && req.Limit <= 200 {
		limit = req.Limit
	}

	rows, err := db.db.QueryContext(ctx, `
		SELECT c.id, c.event_id, u.account_id, u.username, c.comment_text, c.created_at
		FROM event_comments c JOIN user_account u ON u.id = c.user_id
		WHERE c.event_id = $1 ORDER BY c.created_at DESC LIMIT $2 OFFSET $3`,
		eventID, limit, req.Offset)
	if err != nil {
		return nil, 0, status.Errorf(codes.Internal, "failed to list comments: %v", err)
	}
	defer rows.Close()

	out := make([]*pb.EventComment, 0, limit)
	for rows.Next() {
		var c pb.EventComment
		var created time.Time
		if err := rows.Scan(&c.Id, &c.EventId, &c.AccountId, &c.UserName, &c.CommentText, &created); err != nil {
			return nil, 0, status.Errorf(codes.Internal, "failed to scan comment: %v", err)
		}
		c.CreatedAt = timestamppb.New(created)
		out = append(out, &c)
	}
	return out, total, rows.Err()
}

// DeleteComment removes a comment. The author may delete their own; an event
// host may moderate any comment on their event.
func (db *eventDB) DeleteComment(ctx context.Context, req *pb.DeleteCommentReq) error {
	userID, err := resolveAccount(ctx, db.db, req.AccountId)
	if err != nil {
		return err
	}
	eventID, organizerID, err := resolveEvent(ctx, db.db, req.EventSlug)
	if err != nil {
		return err
	}

	var authorID int64
	if err := db.db.QueryRowContext(ctx,
		"SELECT user_id FROM event_comments WHERE id = $1 AND event_id = $2",
		req.CommentId, eventID).Scan(&authorID); err != nil {
		if err == sql.ErrNoRows {
			return status.Error(codes.NotFound, "comment not found")
		}
		return status.Error(codes.Internal, "failed to load comment")
	}

	if authorID != userID && organizerID != userID {
		var n int
		if err := db.db.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM event_co_hosts WHERE event_id = $1 AND co_host_id = $2`,
			eventID, userID).Scan(&n); err != nil {
			return status.Error(codes.Internal, "failed to check host")
		}
		if n == 0 {
			return status.Error(codes.PermissionDenied, "you cannot delete this comment")
		}
	}

	if _, err := db.db.ExecContext(ctx, "DELETE FROM event_comments WHERE id = $1", req.CommentId); err != nil {
		return status.Errorf(codes.Internal, "failed to delete comment: %v", err)
	}
	return nil
}

func (db *eventDB) AddReaction(ctx context.Context, req *pb.ReactReq) error {
	reaction := strings.ToLower(strings.TrimSpace(req.ReactionType))
	if _, ok := allowedReactions[reaction]; !ok {
		return status.Error(codes.InvalidArgument, "unsupported reaction type")
	}
	userID, eventID, err := db.actorAndEvent(ctx, req.AccountId, req.EventSlug)
	if err != nil {
		return err
	}
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO event_reactions (event_id, user_id, reaction_type)
		VALUES ($1,$2,$3) ON CONFLICT (event_id, user_id, reaction_type) DO NOTHING`,
		eventID, userID, reaction); err != nil {
		return status.Errorf(codes.Internal, "failed to add reaction: %v", err)
	}
	return nil
}

func (db *eventDB) RemoveReaction(ctx context.Context, req *pb.ReactReq) error {
	userID, eventID, err := db.actorAndEvent(ctx, req.AccountId, req.EventSlug)
	if err != nil {
		return err
	}
	if _, err := db.db.ExecContext(ctx, `
		DELETE FROM event_reactions WHERE event_id = $1 AND user_id = $2 AND reaction_type = $3`,
		eventID, userID, strings.ToLower(strings.TrimSpace(req.ReactionType))); err != nil {
		return status.Errorf(codes.Internal, "failed to remove reaction: %v", err)
	}
	return nil
}

func (db *eventDB) ReportEvent(ctx context.Context, req *pb.ReportEventReq) error {
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return status.Error(codes.InvalidArgument, "a reason is required")
	}
	userID, eventID, err := db.actorAndEvent(ctx, req.AccountId, req.EventSlug)
	if err != nil {
		return err
	}
	if _, err := db.db.ExecContext(ctx,
		"INSERT INTO event_reports (event_id, user_id, reason) VALUES ($1,$2,$3)",
		eventID, userID, reason); err != nil {
		return status.Errorf(codes.Internal, "failed to report event: %v", err)
	}
	return nil
}

// actorAndEvent resolves the two identifiers every social write needs.
func (db *eventDB) actorAndEvent(ctx context.Context, accountID, slug string) (userID, eventID int64, err error) {
	if userID, err = resolveAccount(ctx, db.db, accountID); err != nil {
		return 0, 0, err
	}
	eventID, _, err = resolveEvent(ctx, db.db, slug)
	return userID, eventID, err
}
