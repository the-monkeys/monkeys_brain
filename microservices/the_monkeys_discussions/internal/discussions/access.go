package discussions

import (
	"fmt"
	"strings"

	"github.com/the-monkeys/the_monkeys/common/audience"
)

// Decision is the access result for a discussion read or write.
// Hide is a 404, the same response as an unknown group, so a private
// discussion never confirms that it exists.
type Decision int

const (
	Allow Decision = iota
	Hide
	Forbid
	NeedLogin
)

// Scope is the viewer's standing. Site is a post with no group.
// Visibility is the group's visibility when Site is false.
// Audience is the stored post audience. Private and unlisted groups
// are treated as members-only at read time even if the stored value is public.
type Scope struct {
	Site         bool
	Visibility   string
	LoggedIn     bool
	ActiveMember bool
	Audience     string
	Author       bool
}

// CanRead reports whether this viewer may see the discussion.
// A site post and a public-group public post are readable by anyone.
// A members-only post is readable by an active member or the author.
// After a group is deleted, a members-only post is readable by its author only.
func CanRead(s Scope) Decision {
	if s.Site {
		if audience.Normalize(s.Audience) == audience.AudienceGroupOnly {
			if s.Author {
				return Allow
			}
			return Hide
		}
		return Allow
	}
	if audience.Coerce(s.Audience, s.Visibility) == audience.AudienceGroupOnly {
		if s.Author || s.ActiveMember {
			return Allow
		}
		return Hide
	}
	if s.Visibility == "public" {
		return Allow
	}
	return Hide
}

// CanWrite reports whether this viewer may create a post, reply, or like.
// Site posts require a logged-in user. Public groups require an active member.
// Private and unlisted groups hide the surface from anyone who is not an active member.
func CanWrite(s Scope) Decision {
	if s.Site {
		if s.LoggedIn {
			return Allow
		}
		return NeedLogin
	}
	if s.Visibility == "private" || s.Visibility == "unlisted" {
		if s.ActiveMember {
			return Allow
		}
		return Hide
	}
	if s.Visibility == "public" {
		if !s.LoggedIn {
			return NeedLogin
		}
		if s.ActiveMember {
			return Allow
		}
		return Forbid
	}
	return Hide
}

// FilesAllowed rejects images on members-only posts. Private and unlisted
// groups are always members-only, so they stay text only. Site posts and
// public posts on public groups may attach images.
func FilesAllowed(visibility, postAudience string, fileCount int) error {
	if fileCount == 0 {
		return nil
	}
	if audience.Coerce(postAudience, visibility) == audience.AudienceGroupOnly {
		return fmt.Errorf("members only discussions are text only")
	}
	return nil
}

// CreateAudience is the stored audience for a new post. Site posts are
// always public. Group posts default to members-only. Private and unlisted
// groups cannot store public.
func CreateAudience(groupSlug, visibility, requested string) string {
	if strings.TrimSpace(groupSlug) == "" {
		return audience.AudiencePublic
	}
	if strings.TrimSpace(requested) == "" {
		requested = audience.AudienceGroupOnly
	}
	return audience.Coerce(requested, visibility)
}

// DiscussionVisible reports whether a post's group is published.
// A site post has an empty slug and stays visible. Draft and unpublished
// groups stay hidden from everyone, including staff.
func DiscussionVisible(groupSlug, groupStatus string) bool {
	if groupSlug == "" {
		return true
	}
	return groupStatus == "published"
}

// StatusChange decides a soft status update.
// The author or group staff may delete. Only group staff may hide.
func StatusChange(author, staff bool, next string) error {
	switch next {
	case "hidden":
		if staff {
			return nil
		}
		return fmt.Errorf("You cannot hide this discussion.")
	case "deleted":
		if author || staff {
			return nil
		}
		return fmt.Errorf("You cannot delete this discussion.")
	default:
		return fmt.Errorf("You cannot change this discussion.")
	}
}
