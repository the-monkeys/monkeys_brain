package database

import (
	"context"
	"database/sql"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const ruleColumns = ` id, group_id, title, body, sort_order, created_at, updated_at`

// scanRule materializes one rule row from the ruleColumns projection.
func scanRule(s rowScanner) (*pb.GroupRule, error) {
	var (
		r         pb.GroupRule
		createdAt sql.NullTime
		updatedAt sql.NullTime
	)
	if err := s.Scan(&r.Id, &r.GroupId, &r.Title, &r.Body, &r.SortOrder, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if createdAt.Valid {
		r.CreatedAt = timestamppb.New(createdAt.Time)
	}
	if updatedAt.Valid {
		r.UpdatedAt = timestamppb.New(updatedAt.Time)
	}
	return &r, nil
}

// loadRules returns a group's rules ordered by sort_order for stable rendering.
func loadRules(ctx context.Context, q querier, groupID int64) ([]*pb.GroupRule, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT"+ruleColumns+" FROM group_rules WHERE group_id = $1 ORDER BY sort_order ASC, id ASC", groupID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load rules")
	}
	defer rows.Close()

	var rules []*pb.GroupRule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to scan rule")
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// AddGroupRule appends a rule to a group. Requires the edit_group grant.
func (db *groupDB) AddGroupRule(ctx context.Context, req *pb.GroupRuleReq) (*pb.GroupRule, error) {
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		return nil, status.Error(codes.InvalidArgument, "rule title and body are required")
	}

	var out *pb.GroupRule
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permEditGroup)
		if err != nil {
			return err
		}
		row := tx.QueryRowContext(ctx, `
			INSERT INTO group_rules (group_id, title, body, sort_order)
			VALUES ($1, $2, $3, $4)
			RETURNING`+ruleColumns,
			groupID, req.Title, req.Body, req.SortOrder)
		out, err = scanRule(row)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to add rule: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateGroupRule edits an existing rule. Requires the edit_group grant and the
// rule must belong to the named group.
func (db *groupDB) UpdateGroupRule(ctx context.Context, req *pb.GroupRuleReq) (*pb.GroupRule, error) {
	if req.RuleId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "rule id is required")
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		return nil, status.Error(codes.InvalidArgument, "rule title and body are required")
	}

	var out *pb.GroupRule
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permEditGroup)
		if err != nil {
			return err
		}
		row := tx.QueryRowContext(ctx, `
			UPDATE group_rules
			SET title = $3, body = $4, sort_order = $5, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1 AND group_id = $2
			RETURNING`+ruleColumns,
			req.RuleId, groupID, req.Title, req.Body, req.SortOrder)
		out, err = scanRule(row)
		if err == sql.ErrNoRows {
			return status.Error(codes.NotFound, "rule not found")
		}
		if err != nil {
			return status.Errorf(codes.Internal, "failed to update rule: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteGroupRule removes a rule. Requires the edit_group grant and the rule
// must belong to the named group.
func (db *groupDB) DeleteGroupRule(ctx context.Context, req *pb.GroupRuleActionReq) error {
	if req.RuleId <= 0 {
		return status.Error(codes.InvalidArgument, "rule id is required")
	}
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permEditGroup)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM group_rules WHERE id = $1 AND group_id = $2`, req.RuleId, groupID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to delete rule: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "rule not found")
		}
		return nil
	})
}
