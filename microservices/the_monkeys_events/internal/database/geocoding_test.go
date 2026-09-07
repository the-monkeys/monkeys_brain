package database

import (
	"context"
	"testing"
	"time"
)

func TestGeocodeEmptyLocation(t *testing.T) {
	lat, lng := Geocode(context.Background(), "  ")
	if lat != 0 || lng != 0 {
		t.Fatalf("empty location must skip Nominatim, got %v,%v", lat, lng)
	}
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
