package database

import (
	"math"
	"strings"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

func TestEventColumnsUsesJoinedAttendeeCount(t *testing.T) {
	if strings.Contains(eventColumns, "SELECT COUNT(1) FROM event_attendees") {
		t.Fatal("correlated attendee COUNT must not live in eventColumns")
	}
	if !strings.Contains(eventColumns, "COALESCE(ac.attendee_count, 0) AS attendee_count") {
		t.Fatal("attendee_count must come from the joined aggregate")
	}
}

func TestEventFromJoinsAttendeeAggregate(t *testing.T) {
	if !strings.Contains(eventFrom, "GROUP BY event_id") {
		t.Fatal("eventFrom must join pre-aggregated confirmed attendee counts")
	}
	if !strings.Contains(eventFrom, "ac.event_id = e.id") {
		t.Fatal("attendee aggregate must join on event id")
	}
}

func TestListSQLDistinctOnWhenCollapsed(t *testing.T) {
	countSQL, selectSQL := listSQL(true, "", " WHERE e.status = 'published'", "listed.start_time ASC", 1)
	for _, q := range []string{countSQL, selectSQL} {
		if !strings.Contains(q, "DISTINCT ON (COALESCE(e.series_id, e.id))") {
			t.Fatalf("collapsed SQL must use DISTINCT ON, got %s", q)
		}
		if strings.Contains(q, "SELECT e2.id FROM events e2") {
			t.Fatal("correlated seriesCollapseCond must not appear")
		}
	}
	if !strings.Contains(countSQL, "SELECT COUNT(*) FROM (") {
		t.Fatalf("collapsed total must count DISTINCT ON rows, got %s", countSQL)
	}
}

func TestListSQLNoDistinctOnWhenUncollapsed(t *testing.T) {
	countSQL, selectSQL := listSQL(false, "", " WHERE e.group_id = $1", "e.start_time ASC", 1)
	if strings.Contains(countSQL, "DISTINCT ON") || strings.Contains(selectSQL, "DISTINCT ON") {
		t.Fatal("group agenda / attending lists must stay uncollapsed")
	}
}

func TestGeoBoxRadius(t *testing.T) {
	minLat, maxLat, minLng, maxLng := geoBox(12.97, 77.59, 25)
	dlat := 25.0 / 111.0
	if math.Abs((maxLat-minLat)-2*dlat) > 1e-9 {
		t.Fatalf("lat span = %v, want %v", maxLat-minLat, 2*dlat)
	}
	if minLng >= maxLng || minLat >= maxLat {
		t.Fatal("box inverted")
	}
	if 12.97 < minLat || 12.97 > maxLat || 77.59 < minLng || 77.59 > maxLng {
		t.Fatal("center must sit inside the box")
	}
}

func TestCommonFiltersAddsBoundingBoxAndHaversine(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat:   12.97,
		UserLng:   77.59,
		Radius:    25,
		EventType: EventTypeInPerson,
	})
	where := f.where()
	if !strings.Contains(where, "BETWEEN") {
		t.Fatalf("radius filter must apply a bounding box, got %s", where)
	}
	if !strings.Contains(where, "acos") {
		t.Fatalf("radius filter must keep the haversine predicate, got %s", where)
	}
}

func TestCommonFiltersNoGeoWhenRadiusZero(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat:   12.97,
		UserLng:   77.59,
		Radius:    0,
		EventType: EventTypeInPerson,
	})
	where := f.where()
	if strings.Contains(where, "latitude IS NOT NULL") || strings.Contains(where, "acos") {
		t.Fatalf("radius 0 must not geo-filter, got %s", where)
	}
}

func TestCommonFiltersNoGeoWithoutPin(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{Radius: 40, EventType: EventTypeInPerson})
	if strings.Contains(f.where(), "acos") {
		t.Fatal("radius without pin must not geo-filter")
	}
}

func TestCommonFiltersClampsRadiusOneToTwo(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 1, EventType: EventTypeInPerson,
	})
	if !strings.Contains(f.where(), "acos") {
		t.Fatal("radius 1 with pin must still geo-filter")
	}
	radius := geoRadiusBound(f)
	if radius != 2 {
		t.Fatalf("radius 1 must clamp to 2, bound %v", radius)
	}
}

func TestCommonFiltersClampsRadius250To100(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 250, EventType: EventTypeInPerson,
	})
	if geoRadiusBound(f) != 100 {
		t.Fatalf("radius 250 must clamp to 100, bound %v", geoRadiusBound(f))
	}
	span := geoLatSpan(f)
	want := 2 * (100.0 / 111.0)
	if math.Abs(span-want) > 1e-9 {
		t.Fatalf("geoBox must use clamped 100 km, span %v want %v", span, want)
	}
}

func TestCommonFiltersLocationIlikeWhenRadiusZero(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 0, Location: "Bengaluru",
	})
	if !strings.Contains(f.where(), "e.location ILIKE") {
		t.Fatalf("radius 0 must keep location ILIKE, got %s", f.where())
	}
}

func TestCommonFiltersRadiusOrUnpinnedLocation(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat:   12.97,
		UserLng:   77.59,
		Radius:    25,
		Location:  "Bengaluru",
		EventType: EventTypeInPerson,
	})
	where := f.where()
	if !strings.Contains(where, "acos") {
		t.Fatalf("near-me must keep haversine, got %s", where)
	}
	if !strings.Contains(where, "e.location ILIKE") {
		t.Fatalf("near-me + city must keep unpinned location match, got %s", where)
	}
	if !strings.Contains(where, " OR ") {
		t.Fatalf("unpinned city match must be OR'd with radius, got %s", where)
	}
}

func geoRadiusBound(f *filter) int32 {
	// last 8 args: lat, lng, lat, radius, minLat, maxLat, minLng, maxLng
	if len(f.args) < 8 {
		return 0
	}
	switch v := f.args[len(f.args)-5].(type) {
	case int32:
		return v
	default:
		return 0
	}
}

func geoLatSpan(f *filter) float64 {
	minLat, _ := f.args[len(f.args)-4].(float64)
	maxLat, _ := f.args[len(f.args)-3].(float64)
	return maxLat - minLat
}

func TestListOrderByNearestUsesPlaceholders(t *testing.T) {
	args := []any{}
	order := listOrderBy(&pb.ListEventsReq{SortBy: "nearest", UserLat: 12.97, UserLng: 77.59}, false, &args)
	if strings.Contains(order, "12.97") || strings.Contains(order, "77.59") {
		t.Fatalf("nearest ORDER BY must not interpolate floats, got %s", order)
	}
	if !strings.Contains(order, "$") {
		t.Fatalf("nearest ORDER BY must bind coordinates, got %s", order)
	}
	if len(args) != 3 {
		t.Fatalf("nearest binds lat,lng,lat; got %d args", len(args))
	}
}
