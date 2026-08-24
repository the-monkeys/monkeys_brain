package database

import (
	"context"
	"database/sql"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validQuestionTypes mirrors the event_questions.question_type CHECK
// constraint. Validating here yields a clean InvalidArgument instead of a
// constraint-violation 500 from Postgres.
var validQuestionTypes = map[string]struct{}{
	"text":          {},
	"textarea":      {},
	"single_choice": {},
	"multi_choice":  {},
	"checkbox":      {},
}

const questionColumns = `
	id, event_id, question_text, question_type, required,
	COALESCE(options::text, ''), sort_order`

func scanQuestion(row rowScanner) (*pb.EventQuestion, error) {
	var q pb.EventQuestion
	if err := row.Scan(
		&q.Id, &q.EventId, &q.QuestionText, &q.QuestionType,
		&q.Required, &q.OptionsJson, &q.SortOrder,
	); err != nil {
		return nil, err
	}
	return &q, nil
}

// insertQuestions writes the given questions for an event on the supplied
// querier so it can run inside a create/replace transaction. sort_order is
// derived from slice position when the caller leaves it at zero, preserving the
// organizer's intended ordering without a second round trip.
func insertQuestions(ctx context.Context, q querier, eventID int64, questions []*pb.EventQuestion) ([]*pb.EventQuestion, error) {
	out := make([]*pb.EventQuestion, 0, len(questions))
	for i, qn := range questions {
		if qn == nil {
			continue
		}
		text := strings.TrimSpace(qn.QuestionText)
		if text == "" {
			return nil, status.Error(codes.InvalidArgument, "question_text is required")
		}
		qType := qn.QuestionType
		if qType == "" {
			qType = "text"
		}
		if _, ok := validQuestionTypes[qType]; !ok {
			return nil, status.Errorf(codes.InvalidArgument, "invalid question_type %q", qType)
		}
		order := qn.SortOrder
		if order == 0 {
			order = int32(i)
		}

		row := q.QueryRowContext(ctx, `
			INSERT INTO event_questions (
				event_id, question_text, question_type, required, options, sort_order
			) VALUES ($1,$2,$3,$4,NULLIF($5,'')::jsonb,$6)
			RETURNING`+questionColumns,
			eventID, text, qType, qn.Required, qn.OptionsJson, order)
		created, err := scanQuestion(row)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to insert question: %v", err)
		}
		out = append(out, created)
	}
	return out, nil
}

// listEventQuestions loads an event's questions in display order. Exposed as a
// helper so event hydration can reuse it once the service layer wires it in.
func listEventQuestions(ctx context.Context, q querier, eventID int64) ([]*pb.EventQuestion, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT"+questionColumns+" FROM event_questions WHERE event_id = $1 ORDER BY sort_order ASC, id ASC",
		eventID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list questions: %v", err)
	}
	defer rows.Close()

	var out []*pb.EventQuestion
	for rows.Next() {
		qn, err := scanQuestion(rows)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan question: %v", err)
		}
		out = append(out, qn)
	}
	return out, rows.Err()
}

// CreateEventQuestions appends questions to an event's registration form. The
// caller must hold manage_questions.
func (db *eventDB) CreateEventQuestions(ctx context.Context, slug, accountID string, questions []*pb.EventQuestion) ([]*pb.EventQuestion, error) {
	var created []*pb.EventQuestion
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, slug, accountID, permManageQuestions)
		if err != nil {
			return err
		}
		created, err = insertQuestions(ctx, tx, eventID, questions)
		return err
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// ReplaceEventQuestions swaps an event's entire question set atomically.
// Existing answers cascade-delete with their questions, so replacing a form
// after attendees have answered discards those answers by design; the service
// layer should gate this on the event still being in draft.
func (db *eventDB) ReplaceEventQuestions(ctx context.Context, slug, accountID string, questions []*pb.EventQuestion) ([]*pb.EventQuestion, error) {
	var result []*pb.EventQuestion
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, slug, accountID, permManageQuestions)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM event_questions WHERE event_id = $1", eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to clear questions: %v", err)
		}
		result, err = insertQuestions(ctx, tx, eventID, questions)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
