package freerangenotify

import (
	"testing"
)

func TestEnrichDataAddsProfileEventGroupAndBlogURLs(t *testing.T) {
	t.Setenv("APP_PUBLIC_URL", "https://monkeys.com.co")
	t.Setenv("SEO_BASE_URL", "")

	got := EnrichData(map[string]interface{}{
		"actor_name":  "innovation_hub",
		"event_slug":  "chai-meeting",
		"event_title": "Chai meeting",
		"group_slug":  "writers",
		"group_name":  "Writers",
		"blog_id":     "p44nn",
		"blog_title":  "Getting Started with Mini",
		"liker_name":  "dave",
	})

	want := map[string]string{
		"home_url":          "https://monkeys.com.co",
		"settings_url":      "https://monkeys.com.co/settings",
		"events_home_url":   "https://monkeys.com.co/events",
		"groups_home_url":   "https://monkeys.com.co/groups",
		"notifications_url": "https://monkeys.com.co/notifications",
		"actor_url":         "https://monkeys.com.co/innovation_hub",
		"liker_url":         "https://monkeys.com.co/dave",
		"event_url":         "https://monkeys.com.co/events/chai-meeting",
		"group_url":         "https://monkeys.com.co/groups/writers",
		"blog_url":          "https://monkeys.com.co/blog/getting-started-with-mini-p44nn",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s=%v want %q", k, got[k], v)
		}
	}
}

func TestEnrichDataLeavesExistingValues(t *testing.T) {
	t.Setenv("APP_PUBLIC_URL", "https://monkeys.com.co")
	got := EnrichData(map[string]interface{}{
		"event_title": "Tea Talk",
		"reason":      "full",
	})
	if got["event_title"] != "Tea Talk" || got["reason"] != "full" {
		t.Fatalf("lost payload: %+v", got)
	}
	if got["event_url"] != nil {
		t.Fatalf("empty slug should not invent an event_url: %v", got["event_url"])
	}
}

func TestAppPublicURLPrefersOverride(t *testing.T) {
	t.Setenv("APP_PUBLIC_URL", "http://localhost:3000/")
	t.Setenv("SEO_BASE_URL", "https://monkeys.com.co")
	if got := AppPublicURL(); got != "http://localhost:3000" {
		t.Fatalf("got %q", got)
	}
}
