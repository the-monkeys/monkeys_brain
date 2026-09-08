package database

import "math"

// geoBox is a cheap pre-filter for Haversine: ~111 km per degree of latitude,
// longitude scaled by cos(lat). The acos predicate still runs after this.
func geoBox(lat, lng float64, radiusKm int32) (minLat, maxLat, minLng, maxLng float64) {
	r := float64(radiusKm)
	dlat := r / 111.0
	cos := math.Cos(lat * math.Pi / 180)
	if math.Abs(cos) < 0.01 {
		cos = 0.01
	}
	dlng := r / (111.0 * cos)
	return lat - dlat, lat + dlat, lng - dlng, lng + dlng
}
