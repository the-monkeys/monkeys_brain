package discussions

import "testing"

func TestReadSiteAllowsStranger(t *testing.T) {
	if d := CanRead(Scope{Site: true}); d != Allow {
		t.Fatalf("site read = %v", d)
	}
}

func TestReadPublicGroupAllowsStranger(t *testing.T) {
	if d := CanRead(Scope{Visibility: "public"}); d != Allow {
		t.Fatalf("public group read = %v", d)
	}
}

func TestReadMembersOnlyHidesFromStranger(t *testing.T) {
	d := CanRead(Scope{Visibility: "public", Audience: "group_only"})
	if d != Hide {
		t.Fatalf("members-only stranger read = %v, want hide", d)
	}
}

func TestReadMembersOnlyAllowsMemberAndAuthor(t *testing.T) {
	if d := CanRead(Scope{Visibility: "public", Audience: "group_only", ActiveMember: true}); d != Allow {
		t.Fatalf("member read = %v", d)
	}
	if d := CanRead(Scope{Visibility: "public", Audience: "group_only", Author: true}); d != Allow {
		t.Fatalf("author read = %v", d)
	}
}

func TestReadOrphanMembersOnlyAllowsAuthorOnly(t *testing.T) {
	if d := CanRead(Scope{Site: true, Audience: "group_only"}); d != Hide {
		t.Fatalf("orphan stranger = %v, want hide", d)
	}
	if d := CanRead(Scope{Site: true, Audience: "group_only", Author: true}); d != Allow {
		t.Fatalf("orphan author = %v", d)
	}
}

func TestReadPrivateGroupStoredPublicIsMembersOnly(t *testing.T) {
	if d := CanRead(Scope{Visibility: "private", Audience: "public"}); d != Hide {
		t.Fatalf("private stored-public stranger = %v, want hide", d)
	}
	if d := CanRead(Scope{Visibility: "private", Audience: "public", ActiveMember: true}); d != Allow {
		t.Fatalf("private stored-public member = %v", d)
	}
}

func TestReadPrivateGroupHidesFromStranger(t *testing.T) {
	for _, vis := range []string{"private", "unlisted"} {
		if d := CanRead(Scope{Visibility: vis}); d != Hide {
			t.Fatalf("%s read = %v, want hide", vis, d)
		}
		if d := CanRead(Scope{Visibility: vis, ActiveMember: true}); d != Allow {
			t.Fatalf("%s member read = %v", vis, d)
		}
	}
}

func TestWriteSiteRequiresLogin(t *testing.T) {
	if d := CanWrite(Scope{Site: true}); d != NeedLogin {
		t.Fatalf("anon site write = %v", d)
	}
	if d := CanWrite(Scope{Site: true, LoggedIn: true}); d != Allow {
		t.Fatalf("member site write = %v", d)
	}
}

func TestWritePublicGroupRequiresActiveMember(t *testing.T) {
	if d := CanWrite(Scope{Visibility: "public", LoggedIn: true}); d != Forbid {
		t.Fatalf("stranger post = %v", d)
	}
	if d := CanWrite(Scope{Visibility: "public", LoggedIn: true, ActiveMember: true}); d != Allow {
		t.Fatalf("member post = %v", d)
	}
}

func TestWritePrivateGroupHidesFromNonMember(t *testing.T) {
	d := CanWrite(Scope{Visibility: "private", LoggedIn: true})
	if d != Hide {
		t.Fatalf("private post by non-member = %v, want hide", d)
	}
}

func TestPrivateGroupDiscussionsAreTextOnly(t *testing.T) {
	for _, vis := range []string{"private", "unlisted"} {
		if err := FilesAllowed(vis, "public", 1); err == nil {
			t.Fatalf("%s group accepted an image", vis)
		}
		if err := FilesAllowed(vis, "public", 0); err != nil {
			t.Fatalf("%s group rejected text: %v", vis, err)
		}
	}
	if err := FilesAllowed("public", "public", 1); err != nil {
		t.Fatal(err)
	}
}

func TestMembersOnlyPostsAreTextOnly(t *testing.T) {
	if err := FilesAllowed("public", "group_only", 1); err == nil {
		t.Fatal("members-only post accepted an image")
	}
	if err := FilesAllowed("public", "group_only", 0); err != nil {
		t.Fatalf("members-only text rejected: %v", err)
	}
}

func TestCreateAudienceDefaultsGroupPostsToMembersOnly(t *testing.T) {
	if got := CreateAudience("", "public", "group_only"); got != "public" {
		t.Fatalf("site audience = %s", got)
	}
	if got := CreateAudience("tea", "public", ""); got != "group_only" {
		t.Fatalf("empty group audience = %s", got)
	}
	if got := CreateAudience("tea", "public", "public"); got != "public" {
		t.Fatalf("public group audience = %s", got)
	}
	if got := CreateAudience("tea", "private", "public"); got != "group_only" {
		t.Fatalf("private group audience = %s", got)
	}
}

func TestUnpublishedGroupIsHiddenFromStaff(t *testing.T) {
	if !DiscussionVisible("", "") {
		t.Fatal("a site post stays readable")
	}
	if !DiscussionVisible("tea", "published") {
		t.Fatal("published group should be visible")
	}
	if DiscussionVisible("tea", "draft") {
		t.Fatal("draft group discussions must stay hidden, including from staff")
	}
}

func TestStaffHideAndAuthorDelete(t *testing.T) {
	if err := StatusChange(false, true, "hidden"); err != nil {
		t.Fatal(err)
	}
	if err := StatusChange(true, false, "hidden"); err == nil {
		t.Fatal("author cannot hide their own discussion")
	}
	if err := StatusChange(true, false, "deleted"); err != nil {
		t.Fatal(err)
	}
	if err := StatusChange(false, false, "deleted"); err == nil {
		t.Fatal("stranger cannot delete")
	}
	if err := StatusChange(false, true, "deleted"); err != nil {
		t.Fatal(err)
	}
}
