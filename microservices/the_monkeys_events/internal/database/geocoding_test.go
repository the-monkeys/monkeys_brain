package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNominatimQueriesPrefersVenuePlusLocality(t *testing.T) {
	got := nominatimQueries("Ecospace Tech Park, Bellandur")
	if !containsFold(got, "Ecospace Bellandur") {
		t.Fatalf("must try distinctive venue + locality, got %v", got)
	}
	if !containsFold(got, "Bellandur") {
		t.Fatalf("must still try the trailing locality, got %v", got)
	}
	if !containsFold(got, "Ecospace Tech Park, Bellandur") {
		t.Fatalf("full address must stay a candidate, got %v", got)
	}
	if got[0] != "Ecospace Bellandur" {
		t.Fatalf("building query must come before neighborhood centroid, got %v", got)
	}
}

func TestNominatimQueriesUsesNearCity(t *testing.T) {
	got := nominatimQueries("Ecospace Tech Park, Bellandur", "Bengaluru")
	if !containsFold(got, "Ecospace Bellandur, Bengaluru") {
		t.Fatalf("must bias with the host city, got %v", got)
	}
}

func TestNominatimQueriesSkipsCountryAsLocality(t *testing.T) {
	got := nominatimQueries("Bengaluru, India")
	if containsFold(got, "India") {
		t.Fatalf("country token must not be a query of its own, got %v", got)
	}
}

func TestGeocodeEmptyLocation(t *testing.T) {
	lat, lng := Geocode(context.Background(), "  ")
	if lat != 0 || lng != 0 {
		t.Fatalf("empty location must skip Nominatim, got %v,%v", lat, lng)
	}
}

func containsFold(items []string, want string) bool {
	for _, s := range items {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func TestGeocodeHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		Geocode(ctx, "Bengaluru, India")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Geocode must return when the context is already canceled")
	}
}
