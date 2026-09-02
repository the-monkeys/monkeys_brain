package database

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// groupListColumns extends groupColumns with a newline-joined topic list so a
// page of groups hydrates in a single round trip (no per-row topic query).
const groupListColumns = groupColumns + `,
	COALESCE((SELECT string_agg(t.topic_name, E'\n' ORDER BY t.topic_name)
	          FROM group_topics t WHERE t.group_id = g.id), '')`

// scanGroupList materializes a group plus its flattened topics from the
// groupListColumns projection.
func scanGroupList(s rowScanner) (*pb.Group, error) {
	var (
		g          pb.Group
		desc       sql.NullString
		city       sql.NullString
		region     sql.NullString
		country    sql.NullString
		lat        sql.NullFloat64
		lng        sql.NullFloat64
		cover      sql.NullString
		logo       sql.NullString
		createdAt  sql.NullTime
		updatedAt  sql.NullTime
		topicsBlob string
	)
	if err := s.Scan(
		&g.Id, &g.Slug, &g.Name, &desc, &g.Visibility, &g.Status,
		&city, &region, &country, &g.Timezone, &lat, &lng,
		&cover, &logo, &g.OrganizerAccountId, &g.OrganizerUsername, &g.MemberCount,
		&createdAt, &updatedAt, &topicsBlob,
	); err != nil {
		return nil, err
	}
	g.Description = nullStringVal(desc)
	g.City = nullStringVal(city)
	g.Region = nullStringVal(region)
	g.Country = nullStringVal(country)
	g.CoverImage = nullStringVal(cover)
	g.LogoImage = nullStringVal(logo)
	if lat.Valid {
		g.Latitude = lat.Float64
	}
	if lng.Valid {
		g.Longitude = lng.Float64
	}
	if createdAt.Valid {
		g.CreatedAt = timestamppb.New(createdAt.Time)
	}
	if updatedAt.Valid {
		g.UpdatedAt = timestamppb.New(updatedAt.Time)
	}
	if topicsBlob != "" {
		g.Topics = strings.Split(topicsBlob, "\n")
	}
	return &g, nil
}

func clampLimit(limit int32) int32 {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

// ListGroups returns a page of publicly discoverable groups matching the
// supplied filters. Only public, published groups surface here; unlisted and
// private groups are reachable by direct slug, not discovery.
func (db *groupDB) ListGroups(ctx context.Context, req *pb.ListGroupsReq) ([]*pb.Group, int32, error) {
	args := []any{}
	conds := []string{"g.visibility = 'public'"}

	if s := strings.TrimSpace(req.Status); s != "" {
		args = append(args, s)
		conds = append(conds, "g.status = $"+strconv.Itoa(len(args)))
	} else {
		conds = append(conds, "g.status = 'published'")
	}
	if c := strings.TrimSpace(req.Country); c != "" {
		args = append(args, c)
		conds = append(conds, "g.country ILIKE $"+strconv.Itoa(len(args)))
	}
	if r := strings.TrimSpace(req.Region); r != "" {
		args = append(args, r)
		conds = append(conds, "g.region ILIKE $"+strconv.Itoa(len(args)))
	}
	if c := strings.TrimSpace(req.City); c != "" {
		args = append(args, c)
		conds = append(conds, "g.city ILIKE $"+strconv.Itoa(len(args)))
	}
	if q := strings.TrimSpace(req.Query); q != "" {
		args = append(args, "%"+q+"%")
		conds = append(conds, "g.name ILIKE $"+strconv.Itoa(len(args)))
	}
	if topics := cleanTopics(req.Topics); len(topics) > 0 {
		args = append(args, topics)
		conds = append(conds, "EXISTS (SELECT 1 FROM group_topics t "+
			"WHERE t.group_id = g.id AND t.topic_name = ANY($"+strconv.Itoa(len(args))+"))")
	}

	// Spatial radius filter: only include groups within `radius` km of the user.
	if req.UserLat != 0 && req.UserLng != 0 && req.Radius > 0 {
		args = append(args, req.UserLat, req.UserLng, req.UserLat, req.Radius)
		latPos := len(args) - 3
		lngPos := len(args) - 2
		lat2Pos := len(args) - 1
		radiusPos := len(args)
		conds = append(conds, fmt.Sprintf(
			"g.latitude IS NOT NULL AND g.longitude IS NOT NULL AND "+
				"(6371 * acos(LEAST(GREATEST(cos(radians($%d)) * cos(radians(g.latitude)) * cos(radians(g.longitude) - radians($%d)) + sin(radians($%d)) * sin(radians(g.latitude)), -1), 1))) <= $%d",
			latPos, lngPos, lat2Pos, radiusPos))
	}

	where := " WHERE " + strings.Join(conds, " AND ")

	var total int32
	if err := db.db.QueryRowContext(ctx,
		"SELECT COUNT(1)"+groupFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to count groups")
	}

	limit := clampLimit(req.Limit)
	args = append(args, limit)
	limitPos := len(args)
	args = append(args, req.Offset)
	offsetPos := len(args)

	orderBy := "g.member_count DESC, g.created_at DESC"
	if req.UserLat != 0 && req.UserLng != 0 {
		orderBy = fmt.Sprintf(
			`(CASE WHEN g.latitude IS NULL OR g.longitude IS NULL THEN 1 ELSE 0 END),
			 (6371 * acos(LEAST(GREATEST(cos(radians(%f)) * cos(radians(g.latitude)) * cos(radians(g.longitude) - radians(%f)) + sin(radians(%f)) * sin(radians(g.latitude)), -1), 1))) ASC NULLS LAST,
			 g.member_count DESC`,
			req.UserLat, req.UserLng, req.UserLat)
	}

	rows, err := db.db.QueryContext(ctx,
		"SELECT"+groupListColumns+groupFrom+where+
			" ORDER BY "+orderBy+
			" LIMIT $"+strconv.Itoa(limitPos)+" OFFSET $"+strconv.Itoa(offsetPos), args...)
	if err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to list groups")
	}
	defer rows.Close()

	groups, err := collectGroups(rows)
	if err != nil {
		return nil, 0, err
	}
	return groups, total, nil
}

// GetUserGroups returns the groups the target user actively belongs to, across
// all statuses, stamping the viewer's role on each. The user is identified by
// account id when present, otherwise username.
func (db *groupDB) GetUserGroups(ctx context.Context, req *pb.ListGroupsReq) ([]*pb.Group, int32, error) {
	var (
		userID int64
		err    error
	)
	switch {
	case strings.TrimSpace(req.Username) != "":
		userID, err = resolveUsername(ctx, db.db, req.Username)
	case strings.TrimSpace(req.AccountId) != "":
		userID, err = resolveAccount(ctx, db.db, req.AccountId)
	default:
		return nil, 0, status.Error(codes.InvalidArgument, "account id or username is required")
	}
	if err != nil {
		return nil, 0, err
	}

	const membershipJoin = ` JOIN group_members gm ON gm.group_id = g.id
		AND gm.user_id = $1 AND gm.status = 'active'`

	publicFilter := ""
	if req.PublicOnly {
		publicFilter = " AND g.visibility = 'public' AND g.status = 'published'"
	}

	var total int32
	if err = db.db.QueryRowContext(ctx,
		"SELECT COUNT(1)"+groupFrom+membershipJoin+publicFilter, userID).Scan(&total); err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to count user groups")
	}

	limit := clampLimit(req.Limit)
	rows, err := db.db.QueryContext(ctx,
		"SELECT"+groupListColumns+", gm.role"+groupFrom+membershipJoin+publicFilter+
			" ORDER BY g.name ASC LIMIT $2 OFFSET $3", userID, limit, req.Offset)
	if err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to list user groups")
	}
	defer rows.Close()

	var groups []*pb.Group
	for rows.Next() {
		g, role, err := scanGroupListWithRole(rows)
		if err != nil {
			return nil, 0, status.Error(codes.Internal, "failed to scan group")
		}
		g.ViewerRole = role
		g.ViewerMemberStatus = memberStatusActive
		groups = append(groups, g)
	}
	return groups, total, rows.Err()
}

// cleanTopics trims and dedupes a topic filter list.
func cleanTopics(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// collectGroups drains a rows cursor of groupListColumns into a slice.
func collectGroups(rows *sql.Rows) ([]*pb.Group, error) {
	var groups []*pb.Group
	for rows.Next() {
		g, err := scanGroupList(rows)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to scan group")
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, status.Error(codes.Internal, "failed to iterate groups")
	}
	return groups, nil
}

// scanGroupListWithRole scans a group row that carries a trailing member role
// column (used by GetUserGroups).
func scanGroupListWithRole(rows *sql.Rows) (*pb.Group, string, error) {
	var (
		g          pb.Group
		desc       sql.NullString
		city       sql.NullString
		region     sql.NullString
		country    sql.NullString
		lat        sql.NullFloat64
		lng        sql.NullFloat64
		cover      sql.NullString
		logo       sql.NullString
		createdAt  sql.NullTime
		updatedAt  sql.NullTime
		topicsBlob string
		role       string
	)
	if err := rows.Scan(
		&g.Id, &g.Slug, &g.Name, &desc, &g.Visibility, &g.Status,
		&city, &region, &country, &g.Timezone, &lat, &lng,
		&cover, &logo, &g.OrganizerAccountId, &g.OrganizerUsername, &g.MemberCount,
		&createdAt, &updatedAt, &topicsBlob, &role,
	); err != nil {
		return nil, "", err
	}
	g.Description = nullStringVal(desc)
	g.City = nullStringVal(city)
	g.Region = nullStringVal(region)
	g.Country = nullStringVal(country)
	g.CoverImage = nullStringVal(cover)
	g.LogoImage = nullStringVal(logo)
	if lat.Valid {
		g.Latitude = lat.Float64
	}
	if lng.Valid {
		g.Longitude = lng.Float64
	}
	if createdAt.Valid {
		g.CreatedAt = timestamppb.New(createdAt.Time)
	}
	if updatedAt.Valid {
		g.UpdatedAt = timestamppb.New(updatedAt.Time)
	}
	if topicsBlob != "" {
		g.Topics = strings.Split(topicsBlob, "\n")
	}
	return &g, role, nil
}
