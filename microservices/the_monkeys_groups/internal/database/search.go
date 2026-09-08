package database

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/common/geo"
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

func groupListFilter(req *pb.ListGroupsReq) (args []any, where string) {
	args = []any{}
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
	radiusKm, applyRadius := geo.ClampSearchRadius(req.Radius)
	geoOn := req.UserLat != 0 && req.UserLng != 0 && applyRadius
	city := strings.TrimSpace(req.City)
	if city != "" && !geoOn {
		args = append(args, city)
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

	if geoOn {
		var cityPos int
		if city != "" {
			args = append(args, city)
			cityPos = len(args)
		}
		minLat, maxLat, minLng, maxLng := geoBox(req.UserLat, req.UserLng, radiusKm)
		args = append(args, req.UserLat, req.UserLng, req.UserLat, radiusKm,
			minLat, maxLat, minLng, maxLng)
		latPos := len(args) - 7
		lngPos := len(args) - 6
		lat2Pos := len(args) - 5
		radiusPos := len(args) - 4
		minLatPos := len(args) - 3
		maxLatPos := len(args) - 2
		minLngPos := len(args) - 1
		maxLngPos := len(args)
		inRange := fmt.Sprintf(
			"g.latitude IS NOT NULL AND g.longitude IS NOT NULL AND "+
				"g.latitude BETWEEN $%d AND $%d AND g.longitude BETWEEN $%d AND $%d AND "+
				"(6371 * acos(LEAST(GREATEST(cos(radians($%d)) * cos(radians(g.latitude)) * cos(radians(g.longitude) - radians($%d)) + sin(radians($%d)) * sin(radians(g.latitude)), -1), 1))) <= $%d",
			minLatPos, maxLatPos, minLngPos, maxLngPos, latPos, lngPos, lat2Pos, radiusPos)
		if cityPos > 0 {
			conds = append(conds, fmt.Sprintf(
				"((%s) OR ((g.latitude IS NULL OR g.longitude IS NULL) AND g.city ILIKE $%d))",
				inRange, cityPos))
		} else {
			conds = append(conds, inRange)
		}
	}

	return args, " WHERE " + strings.Join(conds, " AND ")
}

func groupNearestOrderBy(lat, lng float64, existingArgs int) (string, []any) {
	extra := []any{lat, lng, lat}
	a := existingArgs + 1
	b := existingArgs + 2
	c := existingArgs + 3
	order := fmt.Sprintf(
		`(CASE WHEN g.latitude IS NULL OR g.longitude IS NULL THEN 1 ELSE 0 END),
			 (6371 * acos(LEAST(GREATEST(cos(radians($%d)) * cos(radians(g.latitude)) * cos(radians(g.longitude) - radians($%d)) + sin(radians($%d)) * sin(radians(g.latitude)), -1), 1))) ASC NULLS LAST,
			 g.member_count DESC`,
		a, b, c)
	return order, extra
}

// ListGroups returns a page of publicly discoverable groups matching the
// supplied filters. Only public, published groups surface here; unlisted and
// private groups are reachable by direct slug, not discovery.
func (db *groupDB) ListGroups(ctx context.Context, req *pb.ListGroupsReq) ([]*pb.Group, int32, error) {
	args, where := groupListFilter(req)

	var total int32
	if err := db.db.QueryRowContext(ctx,
		"SELECT COUNT(1)"+groupFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to count groups")
	}

	limit := clampLimit(req.Limit)
	orderBy := "g.member_count DESC, g.created_at DESC"
	if req.UserLat != 0 && req.UserLng != 0 {
		var extra []any
		orderBy, extra = groupNearestOrderBy(req.UserLat, req.UserLng, len(args))
		args = append(args, extra...)
	}
	args = append(args, limit, req.Offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

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
