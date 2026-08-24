package authx

import (
	"hash/maphash"
	"sync"
	"time"
)

// Grant is what the groups service knows about one caller's standing on one
// group. The zero value is a safe deny: the group does not exist, the caller
// holds no role and no permissions.
type Grant struct {
	Exists       bool
	Status       string // draft|published|archived|suspended
	Visibility   string // public|private|unlisted
	Role         string // organizer|co_organizer|moderator|member|''
	MemberStatus string // active|pending|left|removed|banned|''
	Perms        Perm
	IsMember     bool // active membership; the service sets this only for status='active'
	IsBanned     bool
}

// IsOrganizer reports whether the caller owns the group.
func (g Grant) IsOrganizer() bool { return g.Role == "organizer" }

// IsStaff reports whether the caller holds a role that runs the group. Staff
// see the group even while it is a draft.
func (g Grant) IsStaff() bool {
	return g.Role == "organizer" || g.Role == "co_organizer" || g.Role == "moderator"
}

// Public reports whether the group is visible to people who are not members.
// Private groups never are; an unlisted group is reachable by direct link, so
// a caller who already has the slug may see it.
func (g Grant) Public() bool {
	return g.Exists && g.Status == "published" &&
		(g.Visibility == "public" || g.Visibility == "unlisted")
}

// Visible reports whether this caller may see the group at all. Staff always
// can; everyone else needs it published and either public/unlisted or their
// own active membership.
func (g Grant) Visible() bool {
	if g.IsStaff() {
		return true
	}
	if g.Public() {
		return true
	}
	return g.Exists && g.Status == "published" && g.IsMember
}

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
// invalidation API: the groups service re-checks every mutation, so the worst
// a stale entry can do is delay a newly promoted moderator by one TTL, or let
// a request through to a service call that then refuses it.
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
