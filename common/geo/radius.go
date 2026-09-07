package geo

const (
	MinSearchRadiusKm int32 = 2
	MaxSearchRadiusKm int32 = 100
)

// ClampSearchRadius returns (0, false) when radiusKm <= 0 (no geo filter).
// Otherwise it clamps to [MinSearchRadiusKm, MaxSearchRadiusKm].
func ClampSearchRadius(radiusKm int32) (clamped int32, apply bool) {
	if radiusKm <= 0 {
		return 0, false
	}
	if radiusKm < MinSearchRadiusKm {
		return MinSearchRadiusKm, true
	}
	if radiusKm > MaxSearchRadiusKm {
		return MaxSearchRadiusKm, true
	}
	return radiusKm, true
}

// UseClientPin is true when both coordinates are a real point (not 0,0 / partial).
func UseClientPin(lat, lng float64) bool {
	return lat != 0 && lng != 0
}
