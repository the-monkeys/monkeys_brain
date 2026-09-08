package database

import "testing"

func TestAdminLimit(t *testing.T) {
	if adminLimit(0) != 20 || adminLimit(200) != 20 || adminLimit(10) != 10 {
		t.Fatal("limit clamp")
	}
}
