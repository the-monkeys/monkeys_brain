package database

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/common/geo"
)

// Geocode converts a free-text location into coordinates via Nominatim.
// Returns (0, 0) on empty input, miss, or error so callers can store NULL.
func Geocode(ctx context.Context, location string) (float64, float64) {
	if strings.TrimSpace(location) == "" {
		return 0, 0
	}
	if ctx == nil {
		ctx = context.Background()
	}

	reqURL := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=1",
		url.QueryEscape(location),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, 0
	}
	req.Header.Set("User-Agent", "TheMonkeysApp/1.0 (contact@monkeys.com.co)")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0
	}

	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil || len(results) == 0 {
		return 0, 0
	}

	var lat, lon float64
	fmt.Sscanf(results[0].Lat, "%f", &lat)
	fmt.Sscanf(results[0].Lon, "%f", &lon)
	return lat, lon
}

// coordsFromPlace uses client-supplied coordinates when present, otherwise
// geocodes city, region, country.
func coordsFromPlace(ctx context.Context, lat, lng float64, city, region, country string) (float64, float64) {
	if geo.UseClientPin(lat, lng) {
		return lat, lng
	}
	parts := make([]string, 0, 3)
	for _, p := range []string{city, region, country} {
		if s := strings.TrimSpace(p); s != "" {
			parts = append(parts, s)
		}
	}
	return Geocode(ctx, strings.Join(parts, ", "))
}
