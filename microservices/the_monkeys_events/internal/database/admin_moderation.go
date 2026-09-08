package database

import (
	"context"
	"database/sql"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (db *eventDB) AdminFlagNsfw(ctx context.Context, req *pb.AdminFlagNsfwReq) error {
	entityType := strings.ToLower(strings.TrimSpace(req.GetEntityType()))
	entityID := strings.TrimSpace(req.GetEntityId())
	reason := strings.TrimSpace(req.GetReason())
	if entityID == "" || reason == "" {
		return status.Error(codes.InvalidArgument, "entity id and reason are required")
	}
	createdBy := actorLabel(req.GetActor())
	if createdBy == "" {
		createdBy = "staff"
	}

	return db.inTx(ctx, func(tx *sql.Tx) error {
		var (
			q   string
			arg any
		)
		switch entityType {
		case "event":
			q = `INSERT INTO event_admin_flags (event_id, flag_type, reason, created_by)
				SELECT id, 'nsfw', $2, $3 FROM events WHERE slug = $1`
			arg = entityID
		case "group":
			q = `INSERT INTO group_admin_flags (group_id, flag_type, reason, created_by)
				SELECT id, 'nsfw', $2, $3 FROM groups WHERE slug = $1`
			arg = entityID
		case "blog":
			q = `INSERT INTO blog_admin_flags (blog_id, flag_type, reason, created_by)
				SELECT id, 'nsfw', $2, $3 FROM blog WHERE blog_id = $1`
			arg = entityID
		case "user":
			q = `INSERT INTO user_admin_flags (user_id, flag_type, reason, created_by)
				SELECT id, 'nsfw', $2, $3 FROM user_account WHERE account_id = $1 OR username = $1`
			arg = entityID
		default:
			return status.Error(codes.InvalidArgument, "entity_type must be event, blog, group, or user")
		}
		res, err := tx.ExecContext(ctx, q, arg, reason, createdBy)
		if err != nil {
			return status.Error(codes.Internal, "failed to flag nsfw")
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return status.Error(codes.NotFound, "not found")
		}
		return writeAudit(ctx, tx, req.GetActor(), "nsfw.flag", entityType, entityID, map[string]any{"reason": reason})
	})
}

func (db *eventDB) AdminHideEventComment(ctx context.Context, req *pb.AdminHideEventCommentReq) error {
	slug := strings.TrimSpace(req.GetEventSlug())
	if slug == "" || req.GetCommentId() <= 0 {
		return status.Error(codes.InvalidArgument, "event slug and comment id are required")
	}
	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, err := resolveEventID(ctx, tx, slug)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			"DELETE FROM event_comments WHERE id = $1 AND event_id = $2", req.CommentId, eventID)
		if err != nil {
			return status.Error(codes.Internal, "failed to hide comment")
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return status.Error(codes.NotFound, "comment not found")
		}
		return writeAudit(ctx, tx, req.GetActor(), "comment.hide", "event", slug, map[string]any{
			"comment_id": req.CommentId,
		})
	})
}

func (db *eventDB) AdminHideEventQuestion(ctx context.Context, req *pb.AdminHideEventQuestionReq) error {
	slug := strings.TrimSpace(req.GetEventSlug())
	if slug == "" || req.GetQuestionId() <= 0 {
		return status.Error(codes.InvalidArgument, "event slug and question id are required")
	}
	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, err := resolveEventID(ctx, tx, slug)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			"DELETE FROM event_questions WHERE id = $1 AND event_id = $2", req.QuestionId, eventID)
		if err != nil {
			return status.Error(codes.Internal, "failed to hide question")
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return status.Error(codes.NotFound, "question not found")
		}
		return writeAudit(ctx, tx, req.GetActor(), "question.hide", "event", slug, map[string]any{
			"question_id": req.QuestionId,
		})
	})
}

func resolveEventID(ctx context.Context, q querier, slug string) (int64, error) {
	var id int64
	if err := q.QueryRowContext(ctx, "SELECT id FROM events WHERE slug = $1", slug).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return 0, status.Error(codes.NotFound, "event not found")
		}
		return 0, status.Error(codes.Internal, "failed to load event")
	}
	return id, nil
}
