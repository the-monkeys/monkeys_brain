package staffaccess

import "testing"

func TestExistingStaffPermissions(t *testing.T) {
	for _, tc := range []struct {
		role, permission string
		want             bool
	}{
		{"Admin", "payments.read", true}, {"Admin", "payments.manage", true},
		{"Community", "payments.read", false}, {"Community", "payments.manage", false},
		{"Community", "posts.manage", true}, {"Support", "verification.review", true},
		{"Support", "users.read", false}, {"Viewer", "overview.read", false},
		{"Owner", "payments.manage", false}, {"unknown", "overview.read", false},
		{"", "overview.read", false}, {"Admin", "invented.permission", false},
	} {
		if got := Has(tc.role, tc.permission); got != tc.want {
			t.Errorf("%s %s = %v, want %v", tc.role, tc.permission, got, tc.want)
		}
	}
}

func TestPermissionsCannotMutatePolicy(t *testing.T) {
	permissions := Permissions("Admin")
	permissions[0] = "invented.permission"
	if Has("Admin", "invented.permission") || !Has("Admin", "overview.read") {
		t.Fatal("caller mutated server policy")
	}
}
