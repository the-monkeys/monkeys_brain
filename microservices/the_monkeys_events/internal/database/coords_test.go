package database

import (
	"context"
	"testing"
)

func TestResolveEventCoordsOnlineDropsPin(t *testing.T) {
	lat, lng := resolveEventCoords(context.Background(), EventTypeOnline, 12.97, 77.59, "ignored")
	if lat != 0 || lng != 0 {
		t.Fatalf("virtual must persist NULL coords, got %v,%v", lat, lng)
	}
}

func TestResolveEventCoordsUsesPin(t *testing.T) {
	lat, lng := resolveEventCoords(context.Background(), EventTypeInPerson, 12.97, 77.59, "should-not-geocode")
	if lat != 12.97 || lng != 77.59 {
		t.Fatalf("in-person pin must win, got %v,%v", lat, lng)
	}
}

func TestResolveEventCoordsPartialPinFallsThrough(t *testing.T) {
	// Empty location: Geocode returns 0,0 without a network call.
	lat, lng := resolveEventCoords(context.Background(), EventTypeHybrid, 12.97, 0, "  ")
	if lat != 0 || lng != 0 {
		t.Fatalf("partial pin must geocode (empty → 0,0), got %v,%v", lat, lng)
	}
}

func TestNeedsGeocode(t *testing.T) {
	if needsGeocode(EventTypeOnline, 0, 0, "Bengaluru") {
		t.Fatal("online meetups must not geocode")
	}
	if needsGeocode(EventTypeInPerson, 12.97, 77.59, "Bengaluru") {
		t.Fatal("pinned meetups must not geocode again")
	}
	if needsGeocode(EventTypeInPerson, 0, 0, "  ") {
		t.Fatal("empty location must not geocode")
	}
	if !needsGeocode(EventTypeInPerson, 0, 0, "Ecospace Tech Park, Bellandur") {
		t.Fatal("unpinned in-person with a place must geocode")
	}
	if !needsGeocode(EventTypeHybrid, 0, 0, "Bellandur") {
		t.Fatal("unpinned hybrid with a place must geocode")
	}
}

func TestNullCoordZeroIsNil(t *testing.T) {
	if nullCoord(0) != nil {
		t.Fatal("0 must become SQL NULL")
	}
	if nullCoord(12.97) == nil {
		t.Fatal("real lat must be stored")
	}
}
