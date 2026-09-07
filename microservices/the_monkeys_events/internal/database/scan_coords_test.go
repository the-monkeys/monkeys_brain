package database

import (
	"database/sql"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

func TestAttachEventCoordsCopiesNonZeroPairOntoVenue(t *testing.T) {
	e := &pb.Event{}
	attachEventCoords(e, sql.NullFloat64{Float64: 12.97, Valid: true}, sql.NullFloat64{Float64: 77.59, Valid: true})
	if e.Venue == nil || e.Venue.Latitude != 12.97 || e.Venue.Longitude != 77.59 {
		t.Fatalf("venue pin = %+v", e.Venue)
	}
}

func TestAttachEventCoordsSkipsNullOrZero(t *testing.T) {
	e := &pb.Event{}
	attachEventCoords(e, sql.NullFloat64{}, sql.NullFloat64{})
	if e.Venue != nil {
		t.Fatalf("nil coords must not create a venue, got %+v", e.Venue)
	}
	attachEventCoords(e, sql.NullFloat64{Float64: 0, Valid: true}, sql.NullFloat64{Float64: 0, Valid: true})
	if e.Venue != nil {
		t.Fatalf("0,0 is not a pin, got %+v", e.Venue)
	}
}
