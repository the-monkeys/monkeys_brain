package services

import "testing"

func TestEditorialChangesAffectAutosaveFingerprint(t *testing.T) {
	base := map[string]interface{}{"blog_id": "post1", "blog": map[string]interface{}{}, "owner_account_id": "owner1"}
	original := computeContentFingerprint(base)
	for _, field := range []string{"canonical_slug", "seo_description", "content_modified_at", "rss_guid"} {
		base[field] = "explicit edit"
		if computeContentFingerprint(base) == original {
			t.Errorf("%s edit skipped", field)
		}
		delete(base, field)
	}
}
