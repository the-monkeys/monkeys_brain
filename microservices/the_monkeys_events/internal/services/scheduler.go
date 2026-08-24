package services

import (
	"context"
	"fmt"
	"time"

	"github.com/the-monkeys/the_monkeys/constants"
)

// reminderWindows map a label stored in event_reminders_sent to the lead-time
// band it covers. The bands do not overlap, so each attendee gets one nudge a
// day out and one an hour out.
var reminderWindows = []struct {
	label            string
	earliest, latest time.Duration
}{
	{"24h", time.Hour, 24 * time.Hour},
	{"1h", 0, time.Hour},
}

// StartScheduler runs the periodic upkeep: archiving finished events and
// sending attendee reminders. Both operations claim their work in the
// database, so running several replicas does not duplicate notifications.
func (s *EventService) StartScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		s.runUpkeep(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runUpkeep(ctx)
			}
		}
	}()
}

func (s *EventService) runUpkeep(ctx context.Context) {
	if archived, err := s.db.ArchivePastEvents(ctx); err != nil {
		s.log.Errorw("failed to archive past events", "err", err)
	} else if archived > 0 {
		s.log.Debugw("archived finished events", "count", archived)
	}

	for _, window := range reminderWindows {
		reminders, err := s.db.ClaimDueReminders(ctx, window.label, window.earliest, window.latest)
		if err != nil {
			s.log.Errorw("failed to claim reminders", "window", window.label, "err", err)
			continue
		}
		for _, reminder := range reminders {
			s.notify(eventNotification{
				NewUsername:  reminder.Username,
				Action:       constants.EVENT_REMINDER,
				Notification: fmt.Sprintf("%s starts in %s", reminder.Title, reminder.Offset),
				EventSlug:    reminder.Slug,
				EventTitle:   reminder.Title,
			})
		}
		if len(reminders) > 0 {
			s.log.Debugw("event reminders queued", "window", window.label, "count", len(reminders))
		}
	}
}
