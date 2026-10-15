package utils

import (
	"strings"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EnsureAccountEnabled preserves existing account states while enforcing staff suspension.
func EnsureAccountEnabled(user *models.TheMonkeysUser) error {
	if user == nil {
		return status.Error(codes.Unauthenticated, "account unavailable")
	}
	if strings.EqualFold(strings.TrimSpace(user.UserStatus), "suspended") {
		return status.Error(codes.PermissionDenied, "account is suspended")
	}
	return nil
}
