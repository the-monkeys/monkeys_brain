package utils

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
)

func TestGenerateTokenIncludesRole(t *testing.T) {
	w := JwtWrapper{SecretKey: "test-secret-key-for-jwt-role", Issuer: "test", ExpirationHours: 1}
	user := &models.TheMonkeysUser{
		AccountId: "acc-1",
		Email:     "a@example.com",
		Username:  "alice",
		Role:      constants.RoleViewer,
	}
	token, _, err := w.GenerateToken(user)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := w.ValidateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Role != constants.RoleViewer {
		t.Fatalf("role = %q, want Viewer", claims.Role)
	}
}
