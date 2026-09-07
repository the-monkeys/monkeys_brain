package database

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/constants"
)

func TestAssignableRoles(t *testing.T) {
	if _, ok := assignableRoles[constants.RoleOwner]; ok {
		t.Fatal("Owner must not be assignable via staff API")
	}
	for _, r := range []string{constants.RoleViewer, constants.RoleSupport, constants.RoleCommunity, constants.RoleAdmin} {
		if _, ok := assignableRoles[r]; !ok {
			t.Fatalf("missing %s", r)
		}
	}
}

func TestAdminLimit(t *testing.T) {
	if adminLimit(0) != 20 || adminLimit(500) != 20 || adminLimit(50) != 50 {
		t.Fatal("limit clamp")
	}
}
