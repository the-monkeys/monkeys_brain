package utils

import (
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
	"testing"
)

func TestSuspendedAccountCannotReceiveSessionTokens(t *testing.T) {
	w := JwtWrapper{SecretKey: "local-test-secret", Issuer: "test", ExpirationHours: 1}
	user := &models.TheMonkeysUser{UserStatus: "suspended"}
	access, refresh, err := w.GenerateToken(user)
	if err == nil || access != "" || refresh != "" {
		t.Fatal("suspended user received session tokens")
	}
	if token, err := w.GenerateRefreshToken(user); err == nil || token != "" {
		t.Fatal("suspended user received refresh token")
	}
}

func TestLegacyAccountStatesRemainTokenCompatible(t *testing.T) {
	w := JwtWrapper{SecretKey: "local-test-secret", Issuer: "test", ExpirationHours: 1}
	for _, state := range []string{"", "active", "Active", "hidden", "inactive"} {
		if token, _, err := w.GenerateToken(&models.TheMonkeysUser{UserStatus: state}); err != nil || token == "" {
			t.Fatalf("legacy state %q no longer works: %v", state, err)
		}
	}
}
