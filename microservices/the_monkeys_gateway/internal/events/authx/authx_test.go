package authx

import (
	"strconv"
	"testing"
	"time"
)

func TestParsePermsAndHas(t *testing.T) {
	p := ParsePerms([]string{"edit_event", "manage_tickets", "not_a_real_grant"})

	if !p.Has(PermEditEvent) {
		t.Error("edit_event should be held")
	}
	if !p.Has(PermEditEvent | PermManageTickets) {
		t.Error("Has must accept a combined mask")
	}
	if p.Has(PermManageHosts) {
		t.Error("manage_hosts was never granted")
	}
	if ParsePerms(nil) != 0 {
		t.Error("no grants must parse to an empty mask")
	}
}

// Grant gating is the part that decides whether a stranger sees an unpublished
// event or a paid event's join link, so each combination is pinned here.
func TestGrantVisibility(t *testing.T) {
	cases := []struct {
		name        string
		grant       Grant
		visible     bool
		meetingLink bool
	}{
		{
			name:  "missing event is invisible",
			grant: Grant{},
		},
		{
			name:    "published event is public",
			grant:   Grant{Exists: true, Status: "published", Role: "viewer"},
			visible: true,
		},
		{
			name:  "draft is hidden from a stranger",
			grant: Grant{Exists: true, Status: "draft", Role: "viewer"},
		},
		{
			name:        "draft is visible to its organizer",
			grant:       Grant{Exists: true, Status: "draft", Role: "organizer"},
			visible:     true,
			meetingLink: true,
		},
		{
			name:        "draft is visible to a co-host",
			grant:       Grant{Exists: true, Status: "draft", Role: "co_host"},
			visible:     true,
			meetingLink: true,
		},
		{
			name:    "cancelled event stays readable for ticket holders",
			grant:   Grant{Exists: true, Status: "cancelled", Role: "viewer"},
			visible: true,
		},
		{
			name:        "confirmed attendee gets the join link",
			grant:       Grant{Exists: true, Status: "live", Role: "attendee", IsAttendee: true},
			visible:     true,
			meetingLink: true,
		},
		{
			name:    "non-attendee does not get the join link",
			grant:   Grant{Exists: true, Status: "live", Role: "viewer"},
			visible: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.grant.Visible(); got != tc.visible {
				t.Errorf("Visible() = %v, want %v", got, tc.visible)
			}
			if got := tc.grant.MaySeeMeetingLink(); got != tc.meetingLink {
				t.Errorf("MaySeeMeetingLink() = %v, want %v", got, tc.meetingLink)
			}
		})
	}
}

func TestCacheRoundTripAndExpiry(t *testing.T) {
	c := newCache(50 * time.Millisecond)
	want := Grant{Exists: true, Status: "published", Role: "organizer", Perms: PermEditEvent}

	c.put("acc\x00slug", want)

	got, ok := c.get("acc\x00slug")
	if !ok || got != want {
		t.Fatalf("get() = %+v, %v; want %+v, true", got, ok, want)
	}
	if _, ok := c.get("other\x00slug"); ok {
		t.Error("a different caller must not share a cache entry")
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.get("acc\x00slug"); ok {
		t.Error("entry should have expired")
	}
}

// A shard must stay bounded without a sweeper goroutine.
func TestCacheEvictsUnderPressure(t *testing.T) {
	c := newCache(time.Hour) // nothing expires, so only the cap can bound it

	for i := 0; i < maxPerShard*numShards*4; i++ {
		c.put(strconv.Itoa(i), Grant{Exists: true})
	}

	for i := range c.shards {
		c.shards[i].mu.RLock()
		n := len(c.shards[i].m)
		c.shards[i].mu.RUnlock()
		if n > maxPerShard {
			t.Fatalf("shard %d holds %d entries, cap is %d", i, n, maxPerShard)
		}
	}
}
