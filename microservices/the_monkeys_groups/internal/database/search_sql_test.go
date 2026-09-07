package database

import (
	"math"
	"strings"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

func TestGeoBoxRadius(t *testing.T) {
	minLat, maxLat, minLng, maxLng := geoBox(12.97, 77.59, 25)
	dlat := 25.0 / 111.0
	if math.Abs((maxLat-minLat)-2*dlat) > 1e-9 {
		t.Fatalf("lat span = %v, want %v", maxLat-minLat, 2*dlat)
	}
	if minLng >= maxLng {
		t.Fatal("lng box inverted")
	}
}

func TestGroupListSQLAddsBoundingBox(t *testing.T) {
	_, where := groupListFilter(&pb.ListGroupsReq{
		UserLat: 12.97,
		UserLng: 77.59,
		Radius:  25,
	})
	if !strings.Contains(where, "BETWEEN") {
		t.Fatalf("group radius must apply a bounding box, got %s", where)
	}
	if !strings.Contains(where, "acos") {
		t.Fatalf("group radius must keep haversine, got %s", where)
	}
}

func TestGroupListFilterNoGeoWhenRadiusZero(t *testing.T) {
	_, where := groupListFilter(&pb.ListGroupsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 0,
	})
	if strings.Contains(where, "latitude IS NOT NULL") || strings.Contains(where, "acos") {
		t.Fatalf("radius 0 must not geo-filter, got %s", where)
	}
}

func TestGroupListFilterClampsRadius250To100(t *testing.T) {
	args, where := groupListFilter(&pb.ListGroupsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 250,
	})
	if !strings.Contains(where, "acos") {
		t.Fatal("pin + radius 250 must geo-filter")
	}
	if len(args) < 8 {
		t.Fatalf("expected geo args, got %d", len(args))
	}
	radius, ok := args[len(args)-5].(int32)
	if !ok || radius != 100 {
		t.Fatalf("clamped radius want 100, got %#v", args[len(args)-5])
	}
}

func TestGroupListFilterRadiusOrUnpinnedCity(t *testing.T) {
	_, where := groupListFilter(&pb.ListGroupsReq{
		UserLat: 12.97,
		UserLng: 77.59,
		Radius:  25,
		City:    "Bengaluru",
	})
	if !strings.Contains(where, "acos") {
		t.Fatalf("near-me must keep haversine, got %s", where)
	}
	if !strings.Contains(where, "g.city ILIKE") {
		t.Fatalf("near-me + city must keep unpinned city match, got %s", where)
	}
	if !strings.Contains(where, " OR ") {
		t.Fatalf("unpinned city match must be OR'd with radius, got %s", where)
	}
	// City must not AND-exclude unpinned local groups.
	if strings.Count(where, "g.city ILIKE") != 1 {
		t.Fatalf("city ILIKE should appear once as a fallback, got %s", where)
	}
}

func TestGroupNearestOrderByUsesPlaceholders(t *testing.T) {
	order, extra := groupNearestOrderBy(12.97, 77.59, 4)
	if strings.Contains(order, "12.97") || strings.Contains(order, "77.59") {
		t.Fatalf("nearest ORDER BY must not interpolate floats, got %s", order)
	}
	if !strings.Contains(order, "$") {
		t.Fatalf("nearest ORDER BY must bind coordinates, got %s", order)
	}
	if len(extra) != 3 {
		t.Fatalf("nearest binds lat,lng,lat; got %d", len(extra))
	}
}
