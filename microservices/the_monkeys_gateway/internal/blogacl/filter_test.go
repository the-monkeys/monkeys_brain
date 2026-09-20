package blogacl

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/common/audience"
)

func TestFilterReadableBlogsDropsGroupOnlyForStranger(t *testing.T) {
	blogs := []map[string]interface{}{
		{"blog_id": "pub", "audience": "public", "owner_account_id": "a"},
		{"blog_id": "priv", "audience": "group_only", "owner_account_id": "a", "group_slug": "tea-club"},
	}
	got := FilterReadableBlogs(blogs, "stranger", func(doc map[string]interface{}, viewer string) bool {
		return audience.CanRead(audience.DocAudience(doc), audience.OwnerAccountID(doc), viewer, false)
	})
	if len(got) != 1 || got[0]["blog_id"] != "pub" {
		t.Fatalf("got %#v", got)
	}
}

func TestFilterReadableBlogsKeepsAuthorGroupOnly(t *testing.T) {
	blogs := []map[string]interface{}{
		{"blog_id": "priv", "audience": "group_only", "owner_account_id": "owner"},
	}
	got := FilterReadableBlogs(blogs, "owner", func(doc map[string]interface{}, viewer string) bool {
		return audience.CanRead(audience.DocAudience(doc), audience.OwnerAccountID(doc), viewer, false)
	})
	if len(got) != 1 {
		t.Fatalf("author should keep own group_only, got %#v", got)
	}
}
