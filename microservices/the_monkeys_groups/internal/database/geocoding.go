package database

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Geocode converts a free-text location into coordinates via Nominatim.
// Returns (0, 0) on empty input, miss, or error so callers can store NULL.
func Geocode(location string) (float64, float64) {
	if strings.TrimSpace(location) == "" {
		return 0, 0
	}

	reqURL := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=1",
		url.QueryEscape(location),
	)

	req, err := http.NewRequest("GET", reqURL, nil)
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
func coordsFromPlace(lat, lng float64, city, region, country string) (float64, float64) {
	if lat != 0 && lng != 0 {
		return lat, lng
	}
	parts := make([]string, 0, 3)
	for _, p := range []string{city, region, country} {
		if s := strings.TrimSpace(p); s != "" {
			parts = append(parts, s)
		}
	}
	return Geocode(strings.Join(parts, ", "))
}
