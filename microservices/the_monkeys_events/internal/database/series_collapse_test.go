package database

import (
	"strings"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

func TestCollapseDiscoverySeries(t *testing.T) {
	if collapseDiscoverySeries(&pb.ListEventsReq{DateFilter: DateFilterPast}) {
		t.Fatal("past discovery lists must keep every occurred date")
	}
	for _, date := range []string{"", DateFilterUpcoming, DateFilterThisWeek, DateFilterThisMonth, DateFilterAll} {
		if !collapseDiscoverySeries(&pb.ListEventsReq{DateFilter: date}) {
			t.Fatalf("discovery date=%q should collapse series", date)
		}
	}
}

func TestCollapseProfileSeries(t *testing.T) {
	if collapseProfileSeries(&pb.ListEventsReq{}) {
		t.Fatal("hosting lists with no date filter must stay uncollapsed")
	}
	if collapseProfileSeries(&pb.ListEventsReq{DateFilter: DateFilterPast}) {
		t.Fatal("profile past must keep every occurred date")
	}
	for _, date := range []string{DateFilterUpcoming, DateFilterThisWeek, DateFilterThisMonth, DateFilterAll} {
		if !collapseProfileSeries(&pb.ListEventsReq{DateFilter: date}) {
			t.Fatalf("profile date=%q should collapse series", date)
		}
	}
}

func TestEventColumnsMapsSeriesCover(t *testing.T) {
	if !strings.Contains(eventColumns, "es.cover_image") {
		t.Fatal("list/detail must map event_series.cover_image onto occurrences")
	}
}

func TestEventColumnsMapsRsvpClose(t *testing.T) {
	if !strings.Contains(eventColumns, "e.rsvp_closes_at") {
		t.Fatal("list/detail must project rsvp_closes_at")
	}
	if !strings.Contains(eventColumns, "es.rsvp_close_hours_before") {
		t.Fatal("list/detail must project series rsvp close offset")
	}
}

func TestEventColumnsAliasesAttendeeCount(t *testing.T) {
	if !strings.Contains(eventColumns, "AS attendee_count") {
		t.Fatal("popular sort must reuse the selected attendee_count alias")
	}
}

func TestUpcomingDatesQueryCapsPerSeries(t *testing.T) {
	if !strings.Contains(upcomingDatesQuery, "ROW_NUMBER()") {
		t.Fatal("upcoming dates must rank per series instead of loading every occurrence")
	}
	if !strings.Contains(upcomingDatesQuery, "rn <= $3") {
		t.Fatal("upcoming dates must cap at maxUpcomingDates in SQL")
	}
}
