package database

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublishedBlogsByGroupSlugQueryHidesGroupOnlyForStrangers(t *testing.T) {
	q := publishedBlogsByGroupSlugQuery("tea-club", false, 10, 0)
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"group_slug.keyword":"tea-club"`) {
		t.Fatalf("must term-query group_slug.keyword (text field tokenizes hyphens): %s", s)
	}
	if !strings.Contains(s, `"group_only"`) {
		t.Fatalf("strangers must hide group_only: %s", s)
	}
}

func TestDetachBlogsFromGroupQueryRemovesSlugKeepsAudience(t *testing.T) {
	q := detachBlogsFromGroupQuery("tea-club")
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"group_slug.keyword":"tea-club"`) {
		t.Fatalf("must term-query group_slug.keyword: %s", s)
	}
	if !strings.Contains(s, "remove('group_slug')") || !strings.Contains(s, "remove('group_id')") {
		t.Fatalf("must strip group fields: %s", s)
	}
	if strings.Contains(s, "audience") {
		t.Fatalf("must not change audience: %s", s)
	}
}

func TestCoerceGroupBlogsToGroupOnlyQuerySetsAudienceKeepsSlug(t *testing.T) {
	q := coerceGroupBlogsToGroupOnlyQuery("tea-club")
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"group_slug.keyword":"tea-club"`) {
		t.Fatalf("must term-query group_slug.keyword: %s", s)
	}
	if !strings.Contains(s, `"audience":"group_only"`) {
		t.Fatalf("must set group_only: %s", s)
	}
	if strings.Contains(s, "remove('group_slug')") {
		t.Fatalf("must keep group_slug: %s", s)
	}
}

func TestPublishedBlogsByGroupSlugQueryIncludesGroupOnlyForMembers(t *testing.T) {
	q := publishedBlogsByGroupSlugQuery("tea-club", true, 10, 0)
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"group_only"`) {
		t.Fatalf("members should not filter group_only: %s", raw)
	}
}
