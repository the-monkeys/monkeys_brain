package storage_v2

import (
	"net/http"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
	"github.com/the-monkeys/the_monkeys/constants"
)

func TestBlogStatusAllowsPublic(t *testing.T) {
	if !blogStatusAllowsPublic(constants.BlogStatusPublished) {
		t.Fatal("published must be public")
	}
	for _, s := range []string{"", constants.BlogStatusDraft, constants.BlogStatusScheduled, constants.BlogStatusArchived} {
		if blogStatusAllowsPublic(s) {
			t.Fatalf("%q must not be public", s)
		}
	}
}

func TestChecksumFromAssetObjectName(t *testing.T) {
	// assets/sha256/ab/cd/{64hex}.jpg
	sum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key := "assets/sha256/aa/aa/" + sum + ".jpg"
	got, ok := checksumFromAssetObjectName(key)
	if !ok || got != sum {
		t.Fatalf("got %q", got)
	}
}

func TestDecideAssetRead(t *testing.T) {
	if !decideAssetRead(true, false, false, false) {
		t.Fatal("published must be readable without JWT")
	}
	if decideAssetRead(false, true, true, true) {
		t.Fatal("verification keys must never stream on public CAS GET")
	}
	if decideAssetRead(false, false, false, false) {
		t.Fatal("draft-only without JWT must deny")
	}
	if !decideAssetRead(false, false, true, true) {
		t.Fatal("draft-only with JWT and blog access must allow")
	}
	if decideAssetRead(false, false, true, false) {
		t.Fatal("logged-in stranger must not read another user's draft")
	}
}

func TestCacheControlForPublishedBlog(t *testing.T) {
	if got := cacheControlForPublishedBlog(true); got != "public, max-age=31536000" {
		t.Fatalf("published cache: %q", got)
	}
	if got := cacheControlForPublishedBlog(false); got != "private, no-store" {
		t.Fatalf("unpublished cache: %q", got)
	}
}

func TestAllowPostFileRead(t *testing.T) {
	if allowPostFileRead(nil, true, true) {
		t.Fatal("nil resolve must deny")
	}
	pub := &pb.ResolveAssetReadResp{AllowPublic: true}
	if !allowPostFileRead(pub, false, false) {
		t.Fatal("published must allow anonymous")
	}
	draft := &pb.ResolveAssetReadResp{AllowPublic: false}
	if allowPostFileRead(draft, false, false) {
		t.Fatal("anonymous unpublished must deny")
	}
	if allowPostFileRead(draft, true, false) {
		t.Fatal("JWT without blog access must deny")
	}
	if !allowPostFileRead(draft, true, true) {
		t.Fatal("JWT + HasBlogAccess must allow unpublished")
	}
	ver := &pb.ResolveAssetReadResp{VerificationOnly: true}
	if allowPostFileRead(ver, true, true) {
		t.Fatal("verification-only must deny on post routes")
	}
}

func TestUnpublishedPostReadStatusIs404Not403(t *testing.T) {
	if unpublishedPostReadStatus(true, false, false) != http.StatusOK {
		t.Fatal("published anonymous must be 200")
	}
	for _, tc := range []struct {
		jwt, access bool
	}{{false, false}, {true, false}} {
		got := unpublishedPostReadStatus(false, tc.jwt, tc.access)
		if got != http.StatusNotFound {
			t.Fatalf("unpublished jwt=%v access=%v got %d want 404 (not 403)", tc.jwt, tc.access, got)
		}
		if got == http.StatusForbidden {
			t.Fatal("must not leak drafts with 403")
		}
	}
	if unpublishedPostReadStatus(false, true, true) != http.StatusOK {
		t.Fatal("owner JWT must read unpublished")
	}
}
