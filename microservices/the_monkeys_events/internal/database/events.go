package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Event lifecycle statuses.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusLive      = "live"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

// Event type filter values
const (
	EventTypeAll      = "all"
	EventTypeInPerson = "in_person"
	EventTypeOnline   = "virtual"
	EventTypeHybrid   = "hybrid"
)

// Date filter values passed by the discovery UI.
const (
	DateFilterThisWeek  = "this-week"
	DateFilterThisMonth = "this-month"
	DateFilterAll       = "all"
	DateFilterUpcoming  = "upcoming"
	DateFilterPast      = "past"
)

// Sort order values passed by the discovery UI.
const (
	SortBySoonest = "soonest"
	SortByPopular = "popular"
	SortByNewest  = "newest"
)

// publicStatuses are the states an event is visible in to non-hosts.
var publicStatuses = []string{StatusPublished, StatusLive, StatusCompleted}

// liveStatuses are upcoming discovery states; completed events are history.
var liveStatuses = []string{StatusPublished, StatusLive}

// eventVisibilities mirrors the events.visibility CHECK constraint.
var eventVisibilities = map[string]struct{}{
	"public": {}, "group_members": {}, "private": {}, "unlisted": {},
}

// sanitizeVisibility normalises a requested visibility. An empty value defaults
// to 'public'. 'group_members' is only meaningful for events attached to a
// group, so it is rejected for standalone events.
func sanitizeVisibility(v string, groupID int64) (string, error) {
	if v == "" {
		return "public", nil
	}
	if _, ok := eventVisibilities[v]; !ok {
		return "", status.Errorf(codes.InvalidArgument, "invalid visibility %q", v)
	}
	if v == "group_members" && groupID == 0 {
		return "", status.Error(codes.InvalidArgument, "group_members visibility requires a group")
	}
	return v, nil
}

// eventColumns is the single source of truth for the event projection so
// every read path scans an identical row shape.
const eventColumns = `
	e.id, e.title, COALESCE(e.description, ''), e.slug, e.start_time, e.end_time,
	COALESCE(e.timezone, 'UTC'), e.event_type, COALESCE(e.location, ''),
	COALESCE(e.meeting_link, ''), e.capacity, e.status, COALESCE(e.cover_image, ''),
	u.account_id, u.username, e.created_at, e.updated_at,
	(SELECT COUNT(1) FROM event_attendees a WHERE a.event_id = e.id AND a.status = 'confirmed'),
	e.group_id, COALESCE(g.slug, ''), COALESCE(g.name, ''), COALESCE(e.visibility, 'public')`

const eventFrom = ` FROM events e JOIN user_account u ON u.id = e.organizer_id LEFT JOIN groups g ON g.id = e.group_id`

type rowScanner interface{ Scan(dest ...any) error }

func scanEvent(row rowScanner) (*pb.Event, error) {
	var e pb.Event
	var start, end, created, updated time.Time
	var groupID sql.NullInt64
	if err := row.Scan(
		&e.Id, &e.Title, &e.Description, &e.Slug, &start, &end,
		&e.Timezone, &e.EventType, &e.Location, &e.MeetingLink,
		&e.Capacity, &e.Status, &e.CoverImage,
		&e.OrganizerAccountId, &e.OrganizerUsername, &created, &updated,
		&e.AttendeeCount,
		&groupID, &e.GroupSlug, &e.GroupName, &e.Visibility,
	); err != nil {
		return nil, err
	}
	if groupID.Valid {
		e.GroupId = groupID.Int64
	}
	e.StartTime = timestamppb.New(start)
	e.EndTime = timestamppb.New(end)
	e.CreatedAt = timestamppb.New(created)
	e.UpdatedAt = timestamppb.New(updated)
	return &e, nil
}

// -----------------------------------------------------------------------------
// Create / update / delete
// -----------------------------------------------------------------------------

func (db *eventDB) CreateEvent(ctx context.Context, req *pb.CreateEventReq) (*pb.Event, error) {
	var slug string

	err := db.inTx(ctx, func(tx *sql.Tx) error {
		organizerID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}
		if err := validateWindow(req.StartTime, req.EndTime); err != nil {
			return err
		}
		if err := requireVerifiedForPaidTiers(ctx, tx, organizerID, req.TicketTiers); err != nil {
			return err
		}

		// A group_slug attaches the event to a community; the organizer must run
		// that group. Empty slug yields groupID 0 (a standalone event).
		groupID, err := resolveGroupForOrganizer(ctx, tx, req.GroupSlug, organizerID)
		if err != nil {
			return err
		}
		visibility, err := sanitizeVisibility(req.Visibility, groupID)
		if err != nil {
			return err
		}
		var groupCol any
		if groupID > 0 {
			groupCol = groupID
		}

		slug = slugify(req.Title)
		lat, lng := Geocode(req.Location)

		var eventID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO events (
				title, description, slug, start_time, end_time, timezone,
				event_type, location, meeting_link, capacity, cover_image, organizer_id,
				group_id, visibility, latitude, longitude
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			RETURNING id`,
			req.Title, req.Description, slug, req.StartTime.AsTime(), req.EndTime.AsTime(),
			defaultTimezone(req.Timezone), req.EventType, req.Location, req.MeetingLink,
			req.Capacity, req.CoverImage, organizerID, groupCol, visibility, nullCoord(lat), nullCoord(lng),
		).Scan(&eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to create event: %v", err)
		}

		// The organizer owns the event outright; the explicit permission rows
		// keep authorization checks uniform across owner and co-hosts.
		if err := grantPermissions(ctx, tx, eventID, organizerID); err != nil {
			return err
		}
		if err := replaceTags(ctx, tx, eventID, req.Tags); err != nil {
			return err
		}

		for _, username := range req.CoHostUsernames {
			hostID, err := resolveUsername(ctx, tx, username)
			if err != nil {
				return err
			}
			if hostID == organizerID {
				continue
			}
			if err := addCoHost(ctx, tx, eventID, hostID, organizerID); err != nil {
				return err
			}
		}

		for i, tier := range req.TicketTiers {
			if _, err := insertTier(ctx, tx, eventID, tier, int32(i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	event, _, err := db.GetEvent(ctx, slug, req.AccountId)
	return event, err
}

func (db *eventDB) UpdateEvent(ctx context.Context, req *pb.UpdateEventReq) (*pb.Event, error) {
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, req.Slug, req.AccountId, permEditEvent)
		if err != nil {
			return err
		}
		if err := validateWindow(req.StartTime, req.EndTime); err != nil {
			return err
		}

		var (
			curStatus, curType, curLoc, curLink, curVis string
			curStart, curEnd                            time.Time
			curCap                                      int32
		)
		if err := tx.QueryRowContext(ctx, `
			SELECT status, start_time, end_time, event_type, COALESCE(location, ''),
			       COALESCE(meeting_link, ''), capacity, COALESCE(visibility, 'public')
			FROM events WHERE id = $1`, eventID,
		).Scan(&curStatus, &curStart, &curEnd, &curType, &curLoc, &curLink, &curCap, &curVis); err != nil {
			return status.Error(codes.Internal, "failed to load event")
		}
		if eventHasEnded(curStatus, curEnd) {
			vis := req.Visibility
			if vis == "" {
				vis = curVis
			}
			frozen := !sameInstant(req.StartTime.AsTime(), curStart) ||
				!sameInstant(req.EndTime.AsTime(), curEnd) ||
				req.EventType != curType ||
				req.Location != curLoc ||
				req.MeetingLink != curLink ||
				req.Capacity != curCap ||
				vis != curVis
			if frozen {
				return status.Error(codes.FailedPrecondition,
					"ended events can only update title, description, cover, and tags")
			}
		}

		// Visibility is optional on update; when supplied it must be consistent
		// with the event's current group attachment. NULLIF preserves the stored
		// value when the caller omits it.
		if req.Visibility != "" {
			var gid sql.NullInt64
			if err := tx.QueryRowContext(ctx,
				"SELECT group_id FROM events WHERE id = $1", eventID).Scan(&gid); err != nil {
				return status.Error(codes.Internal, "failed to load event")
			}
			var groupID int64
			if gid.Valid {
				groupID = gid.Int64
			}
			if _, err := sanitizeVisibility(req.Visibility, groupID); err != nil {
				return err
			}
		}

		lat, lng := Geocode(req.Location)

		if _, err := tx.ExecContext(ctx, `
			UPDATE events SET
				title = $1, description = $2, start_time = $3, end_time = $4, timezone = $5,
				event_type = $6, location = $7, meeting_link = $8, capacity = $9,
				cover_image = $10, visibility = COALESCE(NULLIF($11, ''), visibility),
				latitude = $12, longitude = $13,
				updated_at = NOW()
			WHERE id = $14`,
			req.Title, req.Description, req.StartTime.AsTime(), req.EndTime.AsTime(),
			defaultTimezone(req.Timezone), req.EventType, req.Location, req.MeetingLink,
			req.Capacity, req.CoverImage, req.Visibility, nullCoord(lat), nullCoord(lng), eventID,
		); err != nil {
			return status.Errorf(codes.Internal, "failed to update event: %v", err)
		}
		return replaceTags(ctx, tx, eventID, req.Tags)
	})
	if err != nil {
		return nil, err
	}

	event, _, err := db.GetEvent(ctx, req.Slug, req.AccountId)
	return event, err
}

func (db *eventDB) DeleteEvent(ctx context.Context, req *pb.EventActionReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		// Deleting is owner-only: co-hosts may edit but never destroy.
		actorID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}
		eventID, organizerID, err := resolveEvent(ctx, tx, req.Slug)
		if err != nil {
			return err
		}
		if organizerID != actorID {
			return status.Error(codes.PermissionDenied, "only the organizer can delete an event")
		}

		var paid int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM event_attendees
			WHERE event_id = $1 AND status = 'confirmed' AND payment_id IS NOT NULL`, eventID).Scan(&paid); err != nil {
			return status.Error(codes.Internal, "failed to check paid attendees")
		}
		if paid > 0 {
			return status.Error(codes.FailedPrecondition,
				"event has paid attendees; cancel it instead so refunds are issued")
		}

		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE id = $1", eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to delete event: %v", err)
		}
		return nil
	})
}

func (db *eventDB) CloneEvent(ctx context.Context, req *pb.CloneEventReq) (*pb.Event, error) {
	if err := validateWindow(req.StartTime, req.EndTime); err != nil {
		return nil, err
	}

	var slug string
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		srcID, organizerID, err := authorize(ctx, tx, req.Slug, req.AccountId, permEditEvent)
		if err != nil {
			return err
		}

		var (
			title, desc, tz, eventType, loc, link, cover, vis string
			capacity                                          int32
			groupID                                           sql.NullInt64
		)
		if err := tx.QueryRowContext(ctx, `
			SELECT title, COALESCE(description, ''), COALESCE(timezone, 'UTC'), event_type,
			       COALESCE(location, ''), COALESCE(meeting_link, ''), capacity,
			       COALESCE(cover_image, ''), COALESCE(visibility, 'public'), group_id
			FROM events WHERE id = $1`, srcID,
		).Scan(&title, &desc, &tz, &eventType, &loc, &link, &capacity, &cover, &vis, &groupID); err != nil {
			return status.Error(codes.Internal, "failed to load event")
		}

		var groupCol any
		if groupID.Valid {
			groupCol = groupID.Int64
		}
		lat, lng := Geocode(loc)
		slug = slugify(title)

		var eventID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO events (
				title, description, slug, start_time, end_time, timezone,
				event_type, location, meeting_link, capacity, cover_image, organizer_id,
				group_id, visibility, latitude, longitude
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			RETURNING id`,
			title, desc, slug, req.StartTime.AsTime(), req.EndTime.AsTime(), tz,
			eventType, loc, link, capacity, cover, organizerID, groupCol, vis,
			nullCoord(lat), nullCoord(lng),
		).Scan(&eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to clone event: %v", err)
		}
		if err := grantPermissions(ctx, tx, eventID, organizerID); err != nil {
			return err
		}

		rows, err := tx.QueryContext(ctx,
			"SELECT tag_name FROM event_tags WHERE event_id = $1", srcID)
		if err != nil {
			return status.Error(codes.Internal, "failed to copy tags")
		}
		var tags []string
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				rows.Close()
				return err
			}
			tags = append(tags, t)
		}
		rows.Close()
		if err := replaceTags(ctx, tx, eventID, tags); err != nil {
			return err
		}

		trows, err := tx.QueryContext(ctx, `
			SELECT name, COALESCE(description, ''), price, COALESCE(currency, $2), capacity, sort_order
			FROM event_ticket_tiers WHERE event_id = $1 ORDER BY sort_order`, srcID, defaultCurrency)
		if err != nil {
			return status.Error(codes.Internal, "failed to copy ticket tiers")
		}
		defer trows.Close()
		i := int32(0)
		copied := false
		for trows.Next() {
			in := &pb.TicketTierInput{}
			if err := trows.Scan(&in.Name, &in.Description, &in.Price, &in.Currency, &in.Capacity, &in.SortOrder); err != nil {
				return err
			}
			if _, err := insertTier(ctx, tx, eventID, in, i); err != nil {
				return err
			}
			copied = true
			i++
		}
		if !copied {
			if _, err := insertTier(ctx, tx, eventID,
				&pb.TicketTierInput{Name: "General", Currency: defaultCurrency}, 0); err != nil {
				return err
			}
		}
		return trows.Err()
	})
	if err != nil {
		return nil, err
	}
	event, _, err := db.GetEvent(ctx, slug, req.AccountId)
	return event, err
}

// SetEventStatus performs a lifecycle transition. Cancelling also releases
// every outstanding RSVP; the caller notifies the affected attendees.
func (db *eventDB) SetEventStatus(ctx context.Context, req *pb.EventActionReq, newStatus string) (*pb.Event, error) {
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, req.Slug, req.AccountId, permEditEvent)
		if err != nil {
			return err
		}

		var current string
		if err := tx.QueryRowContext(ctx,
			"SELECT status FROM events WHERE id = $1", eventID).Scan(&current); err != nil {
			return status.Error(codes.Internal, "failed to read event status")
		}
		if current == newStatus {
			return status.Errorf(codes.FailedPrecondition, "event is already %s", newStatus)
		}
		if current == StatusCancelled {
			return status.Error(codes.FailedPrecondition, "a cancelled event cannot change status")
		}

		if _, err := tx.ExecContext(ctx,
			"UPDATE events SET status = $1, updated_at = NOW() WHERE id = $2",
			newStatus, eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to update status: %v", err)
		}

		switch newStatus {
		case StatusCancelled:
			if _, err := tx.ExecContext(ctx, `
				UPDATE event_attendees SET status = 'cancelled', updated_at = NOW()
				WHERE event_id = $1 AND status IN ('confirmed', 'waitlisted', 'pending_payment')`,
				eventID); err != nil {
				return status.Errorf(codes.Internal, "failed to release rsvps: %v", err)
			}
		case StatusPublished:
			// Every RSVP is tied to a tier, so a free event still needs one.
			var tiers int
			if err := tx.QueryRowContext(ctx,
				"SELECT COUNT(1) FROM event_ticket_tiers WHERE event_id = $1", eventID).Scan(&tiers); err != nil {
				return status.Error(codes.Internal, "failed to count ticket tiers")
			}
			if tiers == 0 {
				if _, err := insertTier(ctx, tx, eventID,
					&pb.TicketTierInput{Name: "General", Currency: defaultCurrency}, 0); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	event, _, err := db.GetEvent(ctx, req.Slug, req.AccountId)
	return event, err
}

// -----------------------------------------------------------------------------
// Reads
// -----------------------------------------------------------------------------

// GetEvent returns the fully hydrated event plus the viewer's RSVP status
// ("" when the viewer is anonymous or has not responded).
func (db *eventDB) GetEvent(ctx context.Context, slug, viewerAccountID string) (*pb.Event, string, error) {
	event, err := scanEvent(db.db.QueryRowContext(ctx,
		"SELECT"+eventColumns+eventFrom+" WHERE e.slug = $1", slug))
	if err == sql.ErrNoRows {
		return nil, "", status.Error(codes.NotFound, "event not found")
	}
	if err != nil {
		return nil, "", status.Errorf(codes.Internal, "failed to load event: %v", err)
	}

	if err := db.hydrate(ctx, []*pb.Event{event}, true); err != nil {
		return nil, "", err
	}

	var viewerStatus string
	if viewerAccountID != "" {
		if err := db.db.QueryRowContext(ctx, `
			SELECT a.status FROM event_attendees a
			JOIN user_account u ON u.id = a.user_id
			WHERE a.event_id = $1 AND u.account_id = $2`,
			event.Id, viewerAccountID).Scan(&viewerStatus); err != nil && err != sql.ErrNoRows {
			db.log.Warnw("failed to read viewer rsvp", "slug", slug, "err", err)
		}

		// Which reactions the viewer already applied, so the client can restore
		// the highlighted state. Best-effort: a read failure must not fail the
		// whole detail fetch.
		rows, err := db.db.QueryContext(ctx, `
			SELECT r.reaction_type FROM event_reactions r
			JOIN user_account u ON u.id = r.user_id
			WHERE r.event_id = $1 AND u.account_id = $2`,
			event.Id, viewerAccountID)
		if err != nil {
			db.log.Warnw("failed to read viewer reactions", "slug", slug, "err", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var rt string
				if err := rows.Scan(&rt); err != nil {
					db.log.Warnw("failed to scan viewer reaction", "slug", slug, "err", err)
					break
				}
				event.ViewerReactions = append(event.ViewerReactions, rt)
			}
		}
	}
	return event, viewerStatus, nil
}

// filter incrementally builds a parameterised WHERE clause.
type filter struct {
	conds []string
	args  []any
}

// add appends a condition; cond must contain a single $%d placeholder.
func (f *filter) add(cond string, arg any) {
	f.args = append(f.args, arg)
	f.conds = append(f.conds, fmt.Sprintf(cond, len(f.args)))
}

func (f *filter) addRaw(cond string) { f.conds = append(f.conds, cond) }

func (f *filter) where() string {
	if len(f.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(f.conds, " AND ")
}

// commonFilters applies the discovery filters shared by every listing RPC.
func commonFilters(f *filter, req *pb.ListEventsReq) {
	if req.EventType != "" && req.EventType != EventTypeAll {
		f.add("e.event_type = $%d", req.EventType)
	}
	if req.Query != "" {
		f.add("e.title ILIKE '%%' || $%d || '%%'", req.Query)
	}
	// Legacy string search fallback, if no radius is given
	if req.Location != "" && req.Radius == 0 {
		f.add("e.location ILIKE '%%' || $%d || '%%'", req.Location)
	}

	if req.UserLat != 0 && req.UserLng != 0 && req.Radius > 0 &&
		req.EventType != EventTypeOnline && req.EventType != EventTypeHybrid {
		// Virtual/hybrid skip the radius: they are reachable from anywhere.
		// In-person must sit inside the requested radius; the UI expands that
		// from city up to country, never worldwide.
		f.args = append(f.args, req.UserLat, req.UserLng, req.UserLat, req.Radius)
		latPos := len(f.args) - 3
		lngPos := len(f.args) - 2
		lat2Pos := len(f.args) - 1
		radiusPos := len(f.args)
		inRange := fmt.Sprintf(
			"e.latitude IS NOT NULL AND e.longitude IS NOT NULL AND "+
				"(6371 * acos(LEAST(GREATEST(cos(radians($%d)) * cos(radians(e.latitude)) * cos(radians(e.longitude) - radians($%d)) + sin(radians($%d)) * sin(radians(e.latitude)), -1), 1))) <= $%d",
			latPos, lngPos, lat2Pos, radiusPos)
		if req.EventType == EventTypeInPerson {
			f.conds = append(f.conds, inRange)
		} else {
			f.conds = append(f.conds, fmt.Sprintf(
				"(e.event_type IN ('%s', '%s') OR (%s))",
				EventTypeOnline, EventTypeHybrid, inRange))
		}
	}

	if len(req.Tags) > 0 {
		f.add("EXISTS (SELECT 1 FROM event_tags t WHERE t.event_id = e.id AND t.tag_name = ANY($%d))", req.Tags)
	}
	switch req.DateFilter {
	case DateFilterThisWeek:
		f.addRaw("e.end_time >= NOW() AND e.start_time >= CURRENT_DATE AND e.start_time < date_trunc('week', CURRENT_DATE + interval '1 week')")
	case DateFilterThisMonth:
		f.addRaw("e.end_time >= NOW() AND e.start_time >= CURRENT_DATE AND e.start_time < date_trunc('month', CURRENT_DATE + interval '1 month')")
	case DateFilterAll, DateFilterUpcoming:
		f.addRaw("e.end_time >= NOW()")
	case DateFilterPast:
		f.addRaw("e.end_time < NOW()")
	}
}

// list executes the count + page queries for a built filter.
func (db *eventDB) list(ctx context.Context, req *pb.ListEventsReq, f *filter, join string) ([]*pb.Event, int32, error) {
	where := f.where()

	var total int32
	if err := db.db.QueryRowContext(ctx,
		"SELECT COUNT(1)"+eventFrom+join+where, f.args...).Scan(&total); err != nil {
		return nil, 0, status.Errorf(codes.Internal, "failed to count events: %v", err)
	}

	limit := int32(20)
	if req.Limit > 0 && req.Limit <= 100 {
		limit = req.Limit
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	args := append(append([]any{}, f.args...), limit, offset)

	orderBy := "e.start_time ASC" // default: SortBySoonest
	switch req.SortBy {
	case SortByNewest:
		orderBy = "e.created_at DESC"
	case SortByPopular:
		orderBy = "(SELECT COUNT(1) FROM event_attendees a WHERE a.event_id = e.id AND a.status = 'confirmed') DESC, e.start_time ASC"
	case "nearest":
		if req.UserLat != 0 && req.UserLng != 0 {
			// Haversine sorting
			orderBy = fmt.Sprintf(`(6371 * acos(cos(radians(%f)) * cos(radians(e.latitude)) * cos(radians(e.longitude) - radians(%f)) + sin(radians(%f)) * sin(radians(e.latitude)))) ASC, e.start_time ASC`, req.UserLat, req.UserLng, req.UserLat)
		}
	}

	query := fmt.Sprintf("SELECT%s%s%s%s ORDER BY %s LIMIT $%d OFFSET $%d",
		eventColumns, eventFrom, join, where, orderBy, len(args)-1, len(args))

	db.log.Debugw("[DEBUG] list query", "sql", query, "args", args)

	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, status.Errorf(codes.Internal, "failed to list events: %v", err)
	}
	defer rows.Close()

	events := make([]*pb.Event, 0, limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, 0, status.Errorf(codes.Internal, "failed to scan event: %v", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, status.Errorf(codes.Internal, "failed to read events: %v", err)
	}

	// Listings only need tags; tiers and reactions belong to the detail view.
	if err := db.hydrate(ctx, events, false); err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

// ListEvents is the public discovery feed; drafts are never exposed.
func (db *eventDB) ListEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	if req.DateFilter == "" {
		req.DateFilter = DateFilterUpcoming
	}

	f := &filter{}
	if req.Status != "" && req.Status != StatusDraft {
		f.add("e.status = $%d", req.Status)
	} else if req.DateFilter == DateFilterPast {
		f.add("e.status = ANY($%d)", publicStatuses)
	} else {
		f.add("e.status = ANY($%d)", liveStatuses)
	}
	f.addRaw("e.visibility = 'public'")
	commonFilters(f, req)
	return db.list(ctx, req, f, "")
}

// GetGroupEvents lists the events attached to a community. Members (any active
// role) see the group's full agenda; everyone else only ever sees public events
// of a public group — private and unlisted groups keep their agenda members-only.
func (db *eventDB) GetGroupEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	slug := strings.TrimSpace(req.GroupSlug)
	if slug == "" {
		return nil, 0, status.Error(codes.InvalidArgument, "group slug is required")
	}

	var groupID int64
	var groupVisibility string
	err := db.db.QueryRowContext(ctx,
		"SELECT id, visibility FROM groups WHERE slug = $1", slug).Scan(&groupID, &groupVisibility)
	if err == sql.ErrNoRows {
		return nil, 0, status.Error(codes.NotFound, "group not found")
	}
	if err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to resolve group")
	}

	// Resolve the viewer's membership; anonymous callers are never members.
	isMember := false
	if req.AccountId != "" {
		if viewerID, e := resolveAccount(ctx, db.db, req.AccountId); e == nil {
			if err := db.db.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM group_members
					WHERE group_id = $1 AND user_id = $2 AND status = 'active')`,
				groupID, viewerID).Scan(&isMember); err != nil {
				return nil, 0, status.Error(codes.Internal, "failed to resolve membership")
			}
		}
	}

	f := &filter{}
	f.add("e.group_id = $%d", groupID)
	f.add("e.status = ANY($%d)", publicStatuses)
	if !isMember {
		// Non-members get nothing from a non-public group, and only the public
		// events of a public one.
		if groupVisibility != "public" {
			return []*pb.Event{}, 0, nil
		}
		f.addRaw("e.visibility = 'public'")
	}

	commonFilters(f, req)
	return db.list(ctx, req, f, "")
}

// GetUserEvents lists events organized by req.Username. Drafts are included
// only when the requester is that same user.
func (db *eventDB) GetUserEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	f := &filter{}
	f.add("u.username = $%d", req.Username)

	self, err := db.isSelf(ctx, req.AccountId, req.Username)
	if err != nil {
		return nil, 0, err
	}
	switch {
	case req.Status != "":
		if req.Status == StatusDraft && !self {
			return nil, 0, status.Error(codes.PermissionDenied, "drafts are private")
		}
		f.add("e.status = $%d", req.Status)
	case req.DateFilter == DateFilterPast:
		f.add("e.status = ANY($%d)", publicStatuses)
	case req.DateFilter == DateFilterUpcoming:
		f.add("e.status = ANY($%d)", liveStatuses)
	case !self:
		f.add("e.status = ANY($%d)", publicStatuses)
	}

	commonFilters(f, req)
	return db.list(ctx, req, f, "")
}

// GetUserAttendingEvents lists events the user holds an active RSVP for.
func (db *eventDB) GetUserAttendingEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	f := &filter{}
	f.add("au.account_id = $%d", req.AccountId)
	f.addRaw("ea.status IN ('confirmed', 'waitlisted', 'pending_payment')")
	f.add("e.status = ANY($%d)", publicStatuses)
	commonFilters(f, req)

	join := ` JOIN event_attendees ea ON ea.event_id = e.id
			  JOIN user_account au ON au.id = ea.user_id`
	return db.list(ctx, req, f, join)
}

// isSelf reports whether the account id belongs to the given username.
func (db *eventDB) isSelf(ctx context.Context, accountID, username string) (bool, error) {
	if accountID == "" || username == "" {
		return false, nil
	}
	var n int
	if err := db.db.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM user_account WHERE account_id = $1 AND username = $2",
		accountID, username).Scan(&n); err != nil {
		return false, status.Error(codes.Internal, "failed to verify identity")
	}
	return n > 0, nil
}

// -----------------------------------------------------------------------------
// Hydration
// -----------------------------------------------------------------------------

// hydrate attaches tags to every event and, when detail is set, also the
// ticket tiers, co-hosts and reaction tallies. Each attribute is fetched with
// one query for the whole batch to avoid N+1 round trips.
func (db *eventDB) hydrate(ctx context.Context, events []*pb.Event, detail bool) error {
	if len(events) == 0 {
		return nil
	}
	byID := make(map[int64]*pb.Event, len(events))
	ids := make([]int64, 0, len(events))
	for _, e := range events {
		byID[e.Id] = e
		ids = append(ids, e.Id)
	}

	collect := func(query string, apply func(rows *sql.Rows) error) error {
		rows, err := db.db.QueryContext(ctx, query, ids)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to hydrate event: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			if err := apply(rows); err != nil {
				return status.Errorf(codes.Internal, "failed to hydrate event: %v", err)
			}
		}
		return rows.Err()
	}

	if err := collect(
		"SELECT event_id, tag_name FROM event_tags WHERE event_id = ANY($1)",
		func(rows *sql.Rows) error {
			var id int64
			var tag string
			if err := rows.Scan(&id, &tag); err != nil {
				return err
			}
			if e := byID[id]; e != nil {
				e.Tags = append(e.Tags, tag)
			}
			return nil
		}); err != nil {
		return err
	}

	if !detail {
		return nil
	}

	if err := collect(`
		SELECT t.event_id, t.id, t.name, COALESCE(t.description, ''), t.price,
			COALESCE(t.currency, 'INR'), t.capacity, t.sort_order,
			(SELECT COUNT(1) FROM event_attendees a
			 WHERE a.ticket_tier_id = t.id AND a.status IN ('confirmed', 'pending_payment'))
		FROM event_ticket_tiers t WHERE t.event_id = ANY($1) ORDER BY t.sort_order, t.id`,
		func(rows *sql.Rows) error {
			var id int64
			var t pb.TicketTier
			if err := rows.Scan(&id, &t.Id, &t.Name, &t.Description, &t.Price,
				&t.Currency, &t.Capacity, &t.SortOrder, &t.Booked); err != nil {
				return err
			}
			t.EventId = id
			if e := byID[id]; e != nil {
				e.TicketTiers = append(e.TicketTiers, &t)
			}
			return nil
		}); err != nil {
		return err
	}

	if err := collect(`
		SELECT c.event_id, u.username FROM event_co_hosts c
		JOIN user_account u ON u.id = c.co_host_id WHERE c.event_id = ANY($1)`,
		func(rows *sql.Rows) error {
			var id int64
			var username string
			if err := rows.Scan(&id, &username); err != nil {
				return err
			}
			if e := byID[id]; e != nil {
				e.CoHostUsernames = append(e.CoHostUsernames, username)
			}
			return nil
		}); err != nil {
		return err
	}

	return collect(`
		SELECT event_id, reaction_type, COUNT(1) FROM event_reactions
		WHERE event_id = ANY($1) GROUP BY event_id, reaction_type`,
		func(rows *sql.Rows) error {
			var id int64
			var r pb.ReactionCount
			if err := rows.Scan(&id, &r.ReactionType, &r.Count); err != nil {
				return err
			}
			if e := byID[id]; e != nil {
				e.Reactions = append(e.Reactions, &r)
			}
			return nil
		})
}

// -----------------------------------------------------------------------------
// Notification fan-out helpers
// -----------------------------------------------------------------------------

// AttendeeUsernames returns the usernames holding an active RSVP, used to
// notify everyone when an event is cancelled.
func (db *eventDB) AttendeeUsernames(ctx context.Context, slug string) ([]string, error) {
	return db.usernames(ctx, `
		SELECT u.username FROM event_attendees a
		JOIN user_account u ON u.id = a.user_id
		JOIN events e ON e.id = a.event_id
		WHERE e.slug = $1 AND a.status IN ('confirmed', 'waitlisted', 'pending_payment')`, slug)
}

// FollowerUsernames returns the followers of an event's organizer.
func (db *eventDB) FollowerUsernames(ctx context.Context, slug string) ([]string, error) {
	return db.usernames(ctx, `
		SELECT u.username FROM user_follows f
		JOIN user_account u ON u.id = f.follower_id
		JOIN events e ON e.organizer_id = f.following_id
		WHERE e.slug = $1`, slug)
}

func (db *eventDB) usernames(ctx context.Context, query, slug string) ([]string, error) {
	rows, err := db.db.QueryContext(ctx, query, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// -----------------------------------------------------------------------------
// Small shared helpers
// -----------------------------------------------------------------------------

func defaultTimezone(tz string) string {
	if tz == "" {
		return "UTC"
	}
	return tz
}

func eventHasEnded(status string, end time.Time) bool {
	if status == StatusCompleted || status == StatusCancelled {
		return true
	}
	return !end.IsZero() && !end.After(time.Now())
}

func sameInstant(a, b time.Time) bool {
	return a.UTC().Truncate(time.Second).Equal(b.UTC().Truncate(time.Second))
}

// nullCoord maps a failed geocode (0,0) to NULL so online or unresolvable
// locations stay out of radius filters instead of anchoring to (0,0).
func nullCoord(c float64) any {
	if c == 0 {
		return nil
	}
	return c
}

func validateWindow(start, end *timestamppb.Timestamp) error {
	if start == nil || end == nil {
		return status.Error(codes.InvalidArgument, "start_time and end_time are required")
	}
	if !end.AsTime().After(start.AsTime()) {
		return status.Error(codes.InvalidArgument, "end_time must be after start_time")
	}
	return nil
}

// replaceTags rewrites an event's tag set, lower-casing and de-duplicating.
func replaceTags(ctx context.Context, q querier, eventID int64, tags []string) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM event_tags WHERE event_id = $1", eventID); err != nil {
		return status.Errorf(codes.Internal, "failed to clear tags: %v", err)
	}
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		if _, err := q.ExecContext(ctx,
			"INSERT INTO event_tags (event_id, tag_name) VALUES ($1, $2) ON CONFLICT DO NOTHING",
			eventID, tag); err != nil {
			return status.Errorf(codes.Internal, "failed to add tag: %v", err)
		}
	}
	return nil
}

// requireVerifiedForPaidTiers enforces that only verified organizers can sell
// tickets.
func requireVerifiedForPaidTiers(ctx context.Context, q querier, organizerID int64, tiers []*pb.TicketTierInput) error {
	paid := false
	for _, t := range tiers {
		if t.GetPrice() > 0 {
			paid = true
			break
		}
	}
	if !paid {
		return nil
	}
	return requireVerified(ctx, q, organizerID)
}

func requireVerified(ctx context.Context, q querier, userID int64) error {
	var verified bool
	if err := q.QueryRowContext(ctx,
		"SELECT COALESCE(is_verified, FALSE) FROM user_account WHERE id = $1", userID).Scan(&verified); err != nil {
		return status.Error(codes.Internal, "failed to check verification status")
	}
	if !verified {
		return status.Error(codes.PermissionDenied, "only verified users can create paid tickets")
	}
	return nil
}
