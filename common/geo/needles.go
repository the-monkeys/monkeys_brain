package geo

import "strings"

// Distinctive Bengaluru localities so an unpinned venue like
// "Ecospace Tech Park, Bellandur" still matches a Bengaluru/Bangalore
// Discover search. Keep names specific enough that they are unlikely
// to match the same string in another metro.
var bengaluruNeedles = []string{
	"Bengaluru",
	"Bangalore",
	"Bengalooru",
	"Bellandur",
	"Whitefield",
	"Koramangala",
	"Indiranagar",
	"Marathahalli",
	"Sarjapur",
	"Electronic City",
	"HSR Layout",
	"Mahadevapura",
	"Yelahanka",
	"Hebbal",
	"Malleshwaram",
	"Jayanagar",
	"Rajajinagar",
	"Kadubeesanahalli",
	"Devarabisanahalli",
	"Manyata",
	"Brookefield",
	"Banashankari",
	"Basavanagudi",
}

var metroNeedles = map[string][]string{
	"bengaluru":  bengaluruNeedles,
	"bangalore":  bengaluruNeedles,
	"bengalooru": bengaluruNeedles,
}

func foldCity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// UnpinnedNeedles returns ILIKE patterns for a Discover city label.
// Unknown cities keep a single "%city%" pattern. Known metros also
// include spelling aliases and well-known localities.
func UnpinnedNeedles(city string) []string {
	raw := strings.TrimSpace(city)
	if raw == "" {
		return nil
	}
	key := raw
	if i := strings.Index(raw, ","); i >= 0 {
		key = strings.TrimSpace(raw[:i])
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		k := strings.ToLower(s)
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, "%"+s+"%")
	}
	add(raw)
	for _, n := range metroNeedles[foldCity(key)] {
		add(n)
	}
	return out
}
