package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/utils"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/metadata"
)

func TestStaffValidationLogsDatabaseRoleRatherThanTrustingTokenRole(t *testing.T) {
	for _, dbRole := range []string{"Admin", "Viewer"} {
		t.Run(dbRole, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)
			jwt := utils.JwtWrapper{SecretKey: "synthetic-signing-secret-fixture", Issuer: "fixture", ExpirationHours: 1}
			user := &models.TheMonkeysUser{AccountId: "fixture-account", Username: "fixture-user", Email: "fixture@example.invalid", Role: "Viewer", UserStatus: "active"}
			token, refresh, err := jwt.GenerateToken(user)
			if err != nil {
				t.Fatal(err)
			}
			user.Role = dbRole
			svc := &AuthzSvc{dbConn: &staffAccessDB{user: user}, jwt: jwt, logger: zap.New(core).Sugar()}
			id := "84b224ac-48bb-4e1e-9dc2-03e158f30566"
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-monkeys-staff-request-id", id))
			response, err := svc.Validate(ctx, &pb.ValidateRequest{Token: token})
			if err != nil {
				t.Fatal(err)
			}
			if response.Role != dbRole {
				t.Fatalf("role=%q, want current database role %q", response.Role, dbRole)
			}
			entries := logs.FilterLevelExact(zapcore.InfoLevel).All()
			if len(entries) != 1 {
				t.Fatalf("expected one correlated staff identity record, got %d", len(entries))
			}
			fields := entries[0].ContextMap()
			for key, want := range map[string]any{"staff_request_id": id, "account_id": "fixture-account", "db_role": dbRole, "token_role": "Viewer", "role_source": "database"} {
				if fields[key] != want {
					t.Errorf("%s=%v, want %v", key, fields[key], want)
				}
			}
			encoded, _ := json.Marshal(logs.All())
			for _, secret := range []string{token, refresh, user.Email, "synthetic-signing-secret-fixture"} {
				if strings.Contains(string(encoded), secret) {
					t.Error("sensitive identity data logged")
				}
			}
		})
	}
}

func TestOrdinaryValidationDoesNotEmitStaffDiagnostics(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	jwt := utils.JwtWrapper{SecretKey: "synthetic-signing-secret-fixture", Issuer: "fixture", ExpirationHours: 1}
	user := &models.TheMonkeysUser{AccountId: "fixture-account", Email: "fixture@example.invalid", Role: "Viewer", UserStatus: "active"}
	token, _, err := jwt.GenerateToken(user)
	if err != nil {
		t.Fatal(err)
	}
	svc := &AuthzSvc{dbConn: &staffAccessDB{user: user}, jwt: jwt, logger: zap.New(core).Sugar()}
	for _, id := range []string{"", "invalid-request-secret-fixture"} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-monkeys-staff-request-id", id))
		if _, err := svc.Validate(ctx, &pb.ValidateRequest{Token: token}); err != nil {
			t.Fatal(err)
		}
	}
	if logs.Len() != 0 {
		t.Fatal("ordinary traffic or invalid metadata emitted staff diagnostics")
	}
}
