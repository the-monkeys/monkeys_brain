package authx

import (
	"hash/maphash"
	"sync"
	"time"
)

// Grant is what the events service knows about one caller's standing on one
// event. The zero value is a safe deny: no permissions, not an attendee.
type Grant struct {
	Exists     bool
	Status     string // draft|published|live|completed|cancelled
	Role       string // organizer|co_host|attendee|viewer
	Perms      Perm
	IsAttendee bool
}

// IsHost reports whether the caller runs the event, by any route.
func (g Grant) IsHost() bool { return g.Role == "organizer" || g.Role == "co_host" }

// Public reports whether the event is visible to people who are not hosts.
// Drafts are not; a cancelled event stays readable so ticket holders can see
// why it vanished from their calendar.
func (g Grant) Public() bool { return g.Exists && g.Status != "draft" }

// Visible reports whether this caller may see the event at all.
func (g Grant) Visible() bool { return g.Public() || g.IsHost() }

// MaySeeMeetingLink gates the join URL. Handing it to anyone who loads the
// detail page would let them skip the ticket.
func (g Grant) MaySeeMeetingLink() bool { return g.IsAttendee || g.IsHost() }

const (
	numShards    = 32
	maxPerShard  = 512
	shardMaskBit = numShards - 1
)

type entry struct {
	grant   Grant
	expires int64 // unix nanos
}

type shard struct {
	mu sync.RWMutex
	m  map[string]entry
}

// cache is a sharded, TTL-only store of grants. There is deliberately no
// invalidation API: the events service re-checks every mutation, so the worst
// a stale entry can do is delay a newly granted co-host by one TTL, or let a
// request through to a service call that then refuses it.
type cache struct {
	seed   maphash.Seed
	ttl    time.Duration
	shards [numShards]shard
}

func newCache(ttl time.Duration) *cache {
	c := &cache{seed: maphash.MakeSeed(), ttl: ttl}
	for i := range c.shards {
		c.shards[i].m = make(map[string]entry)
	}
	return c
}

func (c *cache) shardFor(key string) *shard {
	return &c.shards[maphash.String(c.seed, key)&shardMaskBit]
}

func (c *cache) get(key string) (Grant, bool) {
	s := c.shardFor(key)
	s.mu.RLock()
	e, ok := s.m[key]
	s.mu.RUnlock()
	if !ok || time.Now().UnixNano() > e.expires {
		return Grant{}, false
	}
	return e.grant, true
}

func (c *cache) put(key string, g Grant) {
	s := c.shardFor(key)
	now := time.Now().UnixNano()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Bounded without a sweeper goroutine: drop what has expired, and if the
	// shard is still full it is churning faster than the TTL, so reset it.
	if len(s.m) >= maxPerShard {
		for k, e := range s.m {
			if now > e.expires {
				delete(s.m, k)
			}
		}
		if len(s.m) >= maxPerShard {
			s.m = make(map[string]entry, maxPerShard)
		}
	}

	s.m[key] = entry{grant: g, expires: now + int64(c.ttl)}
}
