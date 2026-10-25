package services

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCalendarFoldingPreservesUnicodeAndOctetLimit(t *testing.T) {
	title := strings.Repeat("学习", 50)
	content := "SUMMARY:" + title + "\r\n"
	folded := foldLines(content)
	for _, line := range strings.Split(folded, "\r\n") {
		if len(line) > 75 {
			t.Errorf("line contains %d octets", len(line))
		}
		if !utf8.ValidString(line) {
			t.Fatal("folding split a UTF-8 character")
		}
	}
	if !strings.Contains(strings.ReplaceAll(folded, "\r\n ", ""), "SUMMARY:"+title) {
		t.Fatal("folding changed the title")
	}
}

func TestCalendarOrganizerParameterCannotInjectProperties(t *testing.T) {
	event := &pb.Event{Id: 1, Title: "Safe", OrganizerUsername: "Doe, \"J\"\r\nX-Injected:yes",
		StartTime: timestamppb.New(time.Date(2026, 11, 12, 13, 30, 0, 0, time.UTC)),
		EndTime:   timestamppb.New(time.Date(2026, 11, 12, 14, 30, 0, 0, time.UTC))}
	ics := strings.ReplaceAll(string(buildICS(event)), "\r\n ", "")
	if !strings.Contains(ics, `ORGANIZER;CN="Doe, ^'J^'^nX-Injected:yes":mailto:noreply@themonkeys.live`) {
		t.Fatalf("unsafe or corrupt organizer parameter: %s", ics)
	}
	if strings.Contains(ics, "\r\nX-Injected:") {
		t.Fatal("injected calendar property")
	}
}

func TestIcalStampUsesUTC(t *testing.T) {
	local := time.Date(2026, 8, 22, 15, 30, 45, 0, time.FixedZone("IST", 5*60*60+30*60))

	if got := icalStamp(local); got != "20260822T100045Z" {
		t.Fatalf("icalStamp() = %q, want UTC stamp", got)
	}
}

func TestBuildICSEscapesFieldsAndIncludesJoinLink(t *testing.T) {
	start := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	event := &pb.Event{
		Id:                42,
		Title:             "Build, Test; Ship",
		Description:       "Line one\nLine two",
		MeetingLink:       "https://meet.example.com/abc",
		Location:          "Room 1, HQ",
		OrganizerUsername: "dave",
		Status:            "published",
		StartTime:         timestamppb.New(start),
		EndTime:           timestamppb.New(start.Add(2 * time.Hour)),
	}

	ics := string(buildICS(event))

	mustContain := []string{
		"BEGIN:VCALENDAR\r\n",
		"UID:the-monkeys-event-42@themonkeys.live\r\n",
		"DTSTART:20260822T100000Z\r\n",
		"DTEND:20260822T120000Z\r\n",
		"SUMMARY:Build\\, Test\\; Ship\r\n",
		"DESCRIPTION:Line one\\nLine two\\nJoin: https://meet.example.com/abc\r\n",
		"LOCATION:Room 1\\, HQ\r\n",
		"ORGANIZER;CN=dave:mailto:noreply@themonkeys.live\r\n",
		"STATUS:CONFIRMED\r\n",
		"END:VCALENDAR\r\n",
	}
	for _, want := range mustContain {
		if !strings.Contains(ics, want) {
			t.Fatalf("ICS missing %q\n%s", want, ics)
		}
	}
}

func TestBuildICSWithoutMeetingLinkKeepsDescriptionPrivate(t *testing.T) {
	start := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	event := &pb.Event{
		Id:          7,
		Title:       "Private Link",
		Description: "Visible description",
		Status:      "cancelled",
		StartTime:   timestamppb.New(start),
		EndTime:     timestamppb.New(start.Add(time.Hour)),
	}

	ics := string(buildICS(event))
	if strings.Contains(ics, "Join:") {
		t.Fatalf("ICS leaked join link marker: %s", ics)
	}
	if !strings.Contains(ics, "DESCRIPTION:Visible description\r\n") {
		t.Fatalf("ICS missing visible description: %s", ics)
	}
	if !strings.Contains(ics, "STATUS:CANCELLED\r\n") {
		t.Fatalf("ICS missing cancelled status: %s", ics)
	}
}
