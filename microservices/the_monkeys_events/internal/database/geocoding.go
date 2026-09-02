package database

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Geocode uses OpenStreetMap's Nominatim API to convert a location string into latitude and longitude.
// It returns (0, 0) if the location is empty, not found, or an error occurs (failing gracefully).
func Geocode(location string) (float64, float64) {
	if strings.TrimSpace(location) == "" {
		return 0, 0
	}

	query := url.QueryEscape(location)
	reqURL := fmt.Sprintf("https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=1", query)

	client := http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return 0, 0
	}
	
	// Nominatim strictly requires a User-Agent.
	req.Header.Set("User-Agent", "TheMonkeysApp/1.0 (contact@monkeys.com.co)")

	resp, err := client.Do(req)
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

	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return 0, 0
	}

	if len(results) == 0 {
		return 0, 0
	}

	var lat, lon float64
	fmt.Sscanf(results[0].Lat, "%f", &lat)
	fmt.Sscanf(results[0].Lon, "%f", &lon)

	return lat, lon
}
