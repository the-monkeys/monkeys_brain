package services

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// icalEscape escapes the characters RFC 5545 reserves in TEXT values.
var icalEscape = strings.NewReplacer(
	"\\", "\\\\",
	";", "\\;",
	",", "\\,",
	"\r\n", "\\n",
	"\n", "\\n",
	"\r", "\\n",
)

func icalStamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// RFC 6868 parameter escaping is distinct from RFC 5545 TEXT escaping.
func calendarParameter(value string) string {
	if !strings.ContainsAny(value, ":;,\"\r\n^") {
		return value
	}
	value = strings.NewReplacer("^", "^^", "\r\n", "^n", "\n", "^n", "\r", "^n", "\"", "^'").Replace(value)
	return "\"" + value + "\""
}

// foldLines wraps content lines at 75 octets as RFC 5545 requires.
func foldLines(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\r\n") {
		for len(line) > 75 {
			cut := 75
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			b.WriteString(line[:cut] + "\r\n")
			line = " " + line[cut:]
		}
		b.WriteString(line + "\r\n")
	}
	return b.String()
}

// buildICS renders an event as a single-VEVENT iCalendar document. Times are
// emitted in UTC, which every calendar client renders in the viewer's local
// zone.
func buildICS(event *pb.Event) []byte {
	data, _ := buildCalendarFile([]*pb.Event{event})
	return data
}

func buildCalendarFile(events []*pb.Event) ([]byte, error) {
	if len(events) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "no available calendar dates")
	}
	for _, event := range events {
		if event == nil || event.StartTime == nil || event.EndTime == nil || event.StartTime.CheckValid() != nil || event.EndTime.CheckValid() != nil || !event.EndTime.AsTime().After(event.StartTime.AsTime()) {
			return nil, status.Error(codes.FailedPrecondition, "event start and end times are unavailable")
		}
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//The Monkeys//Events//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("METHOD:PUBLISH\r\n")
	for _, event := range events {
		writeCalendarEvent(&b, event)
	}
	b.WriteString("END:VCALENDAR\r\n")
	return []byte(foldLines(strings.TrimSuffix(b.String(), "\r\n"))), nil
}

func writeCalendarEvent(b *strings.Builder, event *pb.Event) {
	description := event.Description
	if event.MeetingLink != "" {
		description = strings.TrimSpace(description + "\nJoin: " + event.MeetingLink)
	}
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(b, "UID:the-monkeys-event-%d@themonkeys.live\r\n", event.Id)
	fmt.Fprintf(b, "DTSTAMP:%s\r\n", icalStamp(time.Now()))
	if event.UpdatedAt != nil && event.UpdatedAt.CheckValid() == nil {
		fmt.Fprintf(b, "LAST-MODIFIED:%s\r\n", icalStamp(event.UpdatedAt.AsTime()))
	}
	fmt.Fprintf(b, "DTSTART:%s\r\n", icalStamp(event.StartTime.AsTime()))
	fmt.Fprintf(b, "DTEND:%s\r\n", icalStamp(event.EndTime.AsTime()))
	fmt.Fprintf(b, "SUMMARY:%s\r\n", icalEscape.Replace(event.Title))
	if description != "" {
		fmt.Fprintf(b, "DESCRIPTION:%s\r\n", icalEscape.Replace(description))
	}
	if event.Location != "" {
		fmt.Fprintf(b, "LOCATION:%s\r\n", icalEscape.Replace(event.Location))
	}
	if event.OrganizerUsername != "" {
		fmt.Fprintf(b, "ORGANIZER;CN=%s:mailto:noreply@themonkeys.live\r\n",
			calendarParameter(event.OrganizerUsername))
	}
	if event.Status == "cancelled" {
		b.WriteString("STATUS:CANCELLED\r\n")
	} else {
		b.WriteString("STATUS:CONFIRMED\r\n")
	}
	b.WriteString("END:VEVENT\r\n")
}
