package database

import (
	"strings"
	"testing"
)

func TestResolveAssetReadSQL(t *testing.T) {
	q := resolveAssetReadSQL
	if !strings.Contains(q, "blog.blog_id = r.owner_id") {
		t.Fatal("must join blog on blog.blog_id = r.owner_id")
	}
	if !strings.Contains(q, "r.owner_type = 'blog'") {
		t.Fatal("must filter r.owner_type = 'blog'")
	}
	if !strings.Contains(q, "r.deleted_at IS NULL") {
		t.Fatal("must require live refs")
	}
	if !strings.Contains(q, "verifications/") && !strings.Contains(q, "object_key") {
		t.Fatal("must mention verifications/ or object_key")
	}
}
