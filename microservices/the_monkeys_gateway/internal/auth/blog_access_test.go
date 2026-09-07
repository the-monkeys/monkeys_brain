package auth

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/constants"
)

func TestBlogAccessAllowsDraftRead(t *testing.T) {
	t.Parallel()

	allow := [][]string{
		{constants.PermissionRead},
		{constants.PermissionEdit},
		{"Read"},
		{"read"},
		{"EDIT"},
		{"Edit", constants.PermissionCreate},
		{"  read  "},
	}
	for _, access := range allow {
		if !blogAccessAllowsDraftRead(access) {
			t.Fatalf("expected allow for %v", access)
		}
	}

	deny := [][]string{
		nil,
		{},
		{constants.PermissionCreate},
		{"Create"},
		{"create"},
		{constants.PermissionDelete},
		{"Owner"},
	}
	for _, access := range deny {
		if blogAccessAllowsDraftRead(access) {
			t.Fatalf("expected deny for %v", access)
		}
	}
}

func TestHasBlogAccessNilConfigDenies(t *testing.T) {
	var c *AuthMiddlewareConfig
	if c.HasBlogAccess(nil, "blog") {
		t.Fatal("nil auth config must not grant draft access")
	}
}
