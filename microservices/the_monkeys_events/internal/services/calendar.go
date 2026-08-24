package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

// icalEscape escapes the characters RFC 5545 reserves in TEXT values.
var icalEscape = strings.NewReplacer(
	"\\", "\\\\",
	";", "\\;",
	",", "\\,",
	"\r\n", "\\n",
	"\n", "\\n",
)

func icalStamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// foldLines wraps content lines at 75 octets as RFC 5545 requires.
func foldLines(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\r\n") {
		for len(line) > 75 {
			b.WriteString(line[:75] + "\r\n ")
			line = line[75:]
		}
		b.WriteString(line + "\r\n")
	}
	return b.String()
}

// buildICS renders an event as a single-VEVENT iCalendar document. Times are
// emitted in UTC, which every calendar client renders in the viewer's local
// zone.
func buildICS(event *pb.Event) []byte {
	description := event.Description
	if event.MeetingLink != "" {
		description = strings.TrimSpace(description + "\nJoin: " + event.MeetingLink)
	}

	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//The Monkeys//Events//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("METHOD:PUBLISH\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(&b, "UID:the-monkeys-event-%d@themonkeys.live\r\n", event.Id)
	fmt.Fprintf(&b, "DTSTAMP:%s\r\n", icalStamp(time.Now()))
	fmt.Fprintf(&b, "DTSTART:%s\r\n", icalStamp(event.StartTime.AsTime()))
	fmt.Fprintf(&b, "DTEND:%s\r\n", icalStamp(event.EndTime.AsTime()))
	fmt.Fprintf(&b, "SUMMARY:%s\r\n", icalEscape.Replace(event.Title))
	if description != "" {
		fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", icalEscape.Replace(description))
	}
	if event.Location != "" {
		fmt.Fprintf(&b, "LOCATION:%s\r\n", icalEscape.Replace(event.Location))
	}
	if event.OrganizerUsername != "" {
		fmt.Fprintf(&b, "ORGANIZER;CN=%s:mailto:noreply@themonkeys.live\r\n",
			icalEscape.Replace(event.OrganizerUsername))
	}
	if event.Status == "cancelled" {
		b.WriteString("STATUS:CANCELLED\r\n")
	} else {
		b.WriteString("STATUS:CONFIRMED\r\n")
	}
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")

	return []byte(foldLines(b.String()))
}
