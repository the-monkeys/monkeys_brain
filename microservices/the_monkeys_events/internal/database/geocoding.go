package database

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// nominatimQueries builds increasingly generic place queries. A venue string
// like "Ecospace Tech Park, Bellandur" usually misses as a whole, while
// "Ecospace Bellandur" and "Bellandur" resolve.
func nominatimQueries(location string, near ...string) []string {
	loc := strings.TrimSpace(location)
	if loc == "" {
		return nil
	}
	hint := ""
	if len(near) > 0 {
		hint = strings.TrimSpace(near[0])
	}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, e := range out {
			if strings.EqualFold(e, s) {
				return
			}
		}
		out = append(out, s)
	}
	last := ""
	first := loc
	if i := strings.LastIndex(loc, ","); i >= 0 {
		last = strings.TrimSpace(loc[i+1:])
		first = strings.TrimSpace(loc[:i])
	}
	head := significantHead(first)
	if last != "" && !strings.EqualFold(last, loc) && !weakGeocodeToken(last) {
		if head != "" && !strings.EqualFold(head, last) {
			add(head + " " + last)
			if hint != "" {
				add(head + " " + last + ", " + hint)
			}
			if !hasCountryHint(last) {
				add(head + " " + last + ", India")
			}
		}
		add(last)
		if hint != "" {
			add(last + ", " + hint)
		}
		if !hasCountryHint(last) {
			add(last + ", India")
		}
	}
	add(loc)
	add(strings.Join(strings.Fields(strings.ReplaceAll(loc, ",", " ")), " "))
	if hint != "" {
		add(loc + ", " + hint)
	}
	if !hasCountryHint(loc) {
		add(loc + ", India")
	}
	return out
}

func significantHead(segment string) string {
	fields := strings.Fields(strings.TrimSpace(segment))
	keep := make([]string, 0, len(fields))
	for _, w := range fields {
		if weakAddressWord(w) {
			continue
		}
		keep = append(keep, w)
	}
	if len(keep) == 0 {
		return strings.TrimSpace(segment)
	}
	if len(keep) > 2 {
		keep = keep[:2]
	}
	return strings.Join(keep, " ")
}

func weakAddressWord(w string) bool {
	switch strings.ToLower(strings.Trim(w, ".,#")) {
	case "tech", "park", "parks", "campus", "mall", "plaza", "complex",
		"tower", "towers", "limited", "ltd", "pvt", "private", "office",
		"offices", "building", "phase", "sector", "block", "unit":
		return true
	}
	return false
}

func weakGeocodeToken(s string) bool {
	if len(strings.TrimSpace(s)) < 3 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "india", "usa", "uk", "us", "in", "united states", "united kingdom":
		return true
	}
	return false
}

func hasCountryHint(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "india") ||
		strings.Contains(lower, "united states") ||
		strings.Contains(lower, "united kingdom")
}

// Geocode uses OpenStreetMap's Nominatim API to convert a location string into latitude and longitude.
// It returns (0, 0) if the location is empty, not found, or an error occurs (failing gracefully).
func Geocode(ctx context.Context, location string) (float64, float64) {
	queries := nominatimQueries(location)
	if len(queries) == 0 {
		return 0, 0
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, q := range queries {
		lat, lng := geocodeOnce(ctx, q)
		if lat != 0 || lng != 0 {
			return lat, lng
		}
		if ctx.Err() != nil {
			return 0, 0
		}
	}
	return 0, 0
}

func geocodeOnce(ctx context.Context, location string) (float64, float64) {
	reqURL := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=1",
		url.QueryEscape(location),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, 0
	}

	// Nominatim strictly requires a User-Agent.
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
