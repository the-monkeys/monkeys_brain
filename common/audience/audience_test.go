package audience

import "testing"

func TestNormalize(t *testing.T) {
	if Normalize("") != AudiencePublic {
		t.Fatal("empty is public")
	}
	if Normalize("group_only") != AudienceGroupOnly {
		t.Fatal("keep group_only")
	}
	if Normalize("secret") != AudiencePublic {
		t.Fatal("unknown collapses to public")
	}
}

func TestCoercePrivateGroup(t *testing.T) {
	if Coerce("public", "private") != AudienceGroupOnly {
		t.Fatal("private group cannot be public")
	}
	if Coerce("public", "unlisted") != AudienceGroupOnly {
		t.Fatal("unlisted same as private")
	}
	if Coerce("public", "public") != AudiencePublic {
		t.Fatal("public group may be public")
	}
}

func TestIsGroupOnlyMissingField(t *testing.T) {
	if IsGroupOnly(nil) || IsGroupOnly("") {
		t.Fatal("legacy docs are public")
	}
	if !IsGroupOnly("group_only") {
		t.Fatal("explicit group_only")
	}
}

func TestCanRead(t *testing.T) {
	if !CanRead("public", "owner", "", false) {
		t.Fatal("anonymous reads public")
	}
	if CanRead("group_only", "owner", "stranger", false) {
		t.Fatal("stranger cannot read group_only")
	}
	if !CanRead("group_only", "owner", "owner", false) {
		t.Fatal("author can read")
	}
	if !CanRead("group_only", "owner", "member", true) {
		t.Fatal("active member can read")
	}
}

func TestAppendPublicListMustNot(t *testing.T) {
	got := AppendPublicListMustNot(nil)
	if len(got) != 1 {
		t.Fatalf("len %d", len(got))
	}
}

func TestPublicListMustNot(t *testing.T) {
	got := PublicListMustNot()
	term, ok := got["term"].(map[string]interface{})
	if !ok {
		t.Fatal("expected term query")
	}
	if term["audience"] != AudienceGroupOnly {
		t.Fatalf("got %#v", term["audience"])
	}
}

func TestShouldDetachScheduledGroup(t *testing.T) {
	if ShouldDetachScheduledGroup("", true, false) {
		t.Fatal("no group stays attached to nothing")
	}
	if ShouldDetachScheduledGroup("tea-club", true, true) {
		t.Fatal("active member keeps the group")
	}
	if !ShouldDetachScheduledGroup("tea-club", true, false) {
		t.Fatal("kicked author must detach")
	}
	if !ShouldDetachScheduledGroup("tea-club", false, true) {
		t.Fatal("missing group must detach")
	}
}
