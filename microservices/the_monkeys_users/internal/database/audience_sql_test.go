package database

import "testing"

func TestCoerceBlogAudienceSQL(t *testing.T) {
	if coerceBlogAudienceSQL == "" {
		t.Fatal("missing SQL")
	}
	want := `UPDATE blog SET audience = 'group_only' WHERE group_id = (SELECT id FROM groups WHERE slug = $1) AND audience = 'public'`
	if coerceBlogAudienceSQL != want {
		t.Fatalf("got %q", coerceBlogAudienceSQL)
	}
}
