package database

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Venue source values (mirror venues.source CHECK constraint).
const (
	venueSourceManual   = "manual"
	venueSourceImported = "imported"
	venueSourceProvider = "provider"
)

// venueColumns is the single projection every venue read scans, keeping the
// row shape identical across CreateVenue and SearchVenues.
const venueColumns = `
	id, name, COALESCE(address_line1, ''), COALESCE(address_line2, ''),
	COALESCE(city, ''), COALESCE(region, ''), COALESCE(country, ''),
	COALESCE(postal_code, ''), COALESCE(latitude, 0), COALESCE(longitude, 0)`

func scanVenue(row rowScanner) (*pb.Venue, error) {
	var v pb.Venue
	if err := row.Scan(
		&v.Id, &v.Name, &v.AddressLine1, &v.AddressLine2, &v.City, &v.Region,
		&v.Country, &v.PostalCode, &v.Latitude, &v.Longitude,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateVenue inserts a manually-entered venue owned by the caller. Latitude
// and longitude are optional; zero values are stored as SQL NULL so the geo
// index does not treat unset coordinates as (0,0) off the coast of Africa.
func (db *eventDB) CreateVenue(ctx context.Context, v *pb.Venue, createdByAccountID string) (*pb.Venue, error) {
	if v == nil || strings.TrimSpace(v.Name) == "" {
		return nil, status.Error(codes.InvalidArgument, "venue name is required")
	}

	var created *pb.Venue
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		creatorID, err := resolveAccount(ctx, tx, createdByAccountID)
		if err != nil {
			return err
		}

		row := tx.QueryRowContext(ctx, `
			INSERT INTO venues (
				name, address_line1, address_line2, city, region, country,
				postal_code, latitude, longitude, source, created_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			RETURNING`+venueColumns,
			strings.TrimSpace(v.Name), nullifyStr(v.AddressLine1), nullifyStr(v.AddressLine2),
			nullifyStr(v.City), nullifyStr(v.Region), nullifyStr(v.Country),
			nullifyStr(v.PostalCode), nullifyCoord(v.Latitude), nullifyCoord(v.Longitude),
			venueSourceManual, creatorID,
		)
		created, err = scanVenue(row)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to create venue: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// SearchVenues returns venues matching an optional free-text name and/or
// location filters. It relies on the trigram index on venues.name for the
// fuzzy name match and the (country, region, city) btree for locality.
func (db *eventDB) SearchVenues(ctx context.Context, query, city, country string, limit int32) ([]*pb.Venue, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	f := &filter{}
	if q := strings.TrimSpace(query); q != "" {
		f.add("name ILIKE '%%' || $%d || '%%'", q)
	}
	if c := strings.TrimSpace(city); c != "" {
		f.add("city ILIKE $%d", c)
	}
	if c := strings.TrimSpace(country); c != "" {
		f.add("country ILIKE $%d", c)
	}

	f.args = append(f.args, limit)
	rows, err := db.db.QueryContext(ctx,
		"SELECT"+venueColumns+" FROM venues"+f.where()+
			" ORDER BY name ASC LIMIT $"+strconv.Itoa(len(f.args)), f.args...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to search venues: %v", err)
	}
	defer rows.Close()

	var out []*pb.Venue
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan venue: %v", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AttachVenueToEvent points an event at an existing venue. The caller must hold
// edit_event; the FK guarantees the venue exists. Passing venueID <= 0 detaches
// the venue (sets it NULL).
func (db *eventDB) AttachVenueToEvent(ctx context.Context, slug, accountID string, venueID int64) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, slug, accountID, permEditEvent)
		if err != nil {
			return err
		}

		if venueID <= 0 {
			if _, err := tx.ExecContext(ctx,
				"UPDATE events SET venue_id = NULL, updated_at = NOW() WHERE id = $1", eventID); err != nil {
				return status.Errorf(codes.Internal, "failed to detach venue: %v", err)
			}
			return nil
		}

		var exists bool
		if err := tx.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM venues WHERE id = $1)", venueID).Scan(&exists); err != nil {
			return status.Error(codes.Internal, "failed to verify venue")
		}
		if !exists {
			return status.Error(codes.NotFound, "venue not found")
		}

		if _, err := tx.ExecContext(ctx,
			"UPDATE events SET venue_id = $1, updated_at = NOW() WHERE id = $2", venueID, eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to attach venue: %v", err)
		}
		return nil
	})
}
