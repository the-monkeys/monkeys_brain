package services

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/db"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// The old email belongs to a different account. Email-based authentication must
// not borrow that account's username (and hence private profile) or staff role.
type identityLookupDB struct {
	db.AuthDBHandler
	accounts     map[string]*models.TheMonkeysUser
	emails       map[string]*models.TheMonkeysUser
	emailLookups int
}

func (d *identityLookupDB) CheckIfAccountIdExist(id string) (*models.TheMonkeysUser, error) {
	if user := d.accounts[id]; user != nil {
		return user, nil
	}
	return nil, sql.ErrNoRows
}

func (d *identityLookupDB) CheckIfEmailExist(email string) (*models.TheMonkeysUser, error) {
	d.emailLookups++
	if user := d.emails[email]; user != nil {
		return user, nil
	}
	return nil, sql.ErrNoRows
}

func identityTestJWT() utils.JwtWrapper {
	return utils.JwtWrapper{SecretKey: "synthetic-identity-test-key", Issuer: "identity-test", ExpirationHours: 1}
}

func originalIdentity() *models.TheMonkeysUser {
	return &models.TheMonkeysUser{
		Id: 11, AccountId: "account-a", Username: "original-a",
		Email: "old-a@example.invalid", FirstName: "Original", LastName: "A",
		EmailVerificationStatus: "pending", UserStatus: "active", Role: "Admin",
	}
}

func currentIdentity() *models.TheMonkeysUser {
	return &models.TheMonkeysUser{
		Id: 11, AccountId: "account-a", Username: "renamed-a",
		Email: "new-a@example.invalid", FirstName: "Current", LastName: "A",
		EmailVerificationStatus: "verified", UserStatus: "active", Role: "Viewer",
	}
}

func identityService(user *models.TheMonkeysUser, reassignEmail bool) (*AuthzSvc, *identityLookupDB) {
	database := &identityLookupDB{
		accounts: map[string]*models.TheMonkeysUser{},
		emails:   map[string]*models.TheMonkeysUser{},
	}
	if user != nil {
		database.accounts[user.AccountId] = user
		database.emails[user.Email] = user
	}
	if reassignEmail {
		database.emails["old-a@example.invalid"] = &models.TheMonkeysUser{
			Id: 22, AccountId: "account-b", Username: "private-b", Email: "old-a@example.invalid",
			FirstName: "Private", LastName: "B", EmailVerificationStatus: "verified",
			UserStatus: "active", Role: "Owner",
		}
	}
	return &AuthzSvc{dbConn: database, jwt: identityTestJWT(), logger: zap.NewNop().Sugar()}, database
}

func checkSignedIdentity(t *testing.T, svc *AuthzSvc, token string, want *models.TheMonkeysUser, wantCode codes.Code) {
	t.Helper()
	t.Run("Validate", func(t *testing.T) {
		response, err := svc.Validate(context.Background(), &pb.ValidateRequest{Token: token})
		if status.Code(err) != wantCode {
			t.Fatalf("status = %s, want %s", status.Code(err), wantCode)
		}
		if wantCode != codes.OK {
			if response != nil {
				t.Fatal("denied identity returned account data")
			}
			return
		}
		expected := &pb.ValidateResponse{
			StatusCode: 200, UserId: want.Id, Email: want.Email,
			UserName: want.Username, AccountId: want.AccountId, Role: want.Role,
		}
		if !proto.Equal(response, expected) {
			t.Fatal("validation returned identity fields outside the current signed account")
		}
	})
	t.Run("DecodeSignedJWT", func(t *testing.T) {
		response, err := svc.DecodeSignedJWT(context.Background(), &pb.DecodeSignedJWTRequest{Token: token})
		if status.Code(err) != wantCode {
			t.Fatalf("status = %s, want %s", status.Code(err), wantCode)
		}
		if wantCode != codes.OK {
			if response != nil {
				t.Fatal("denied identity returned account data")
			}
			return
		}
		expected := &pb.DecodeSignedJWTResponse{
			StatusCode: 200, Email: want.Email, FirstName: want.FirstName,
			LastName: want.LastName, Username: want.Username,
			EmailVerificationStatus: want.EmailVerificationStatus, AccountId: want.AccountId,
		}
		if !proto.Equal(response, expected) {
			t.Fatal("session decoding returned identity fields outside the current signed account")
		}
	})
}

func TestAuthIdentityStaysWithSignedAccount(t *testing.T) {
	signer := identityTestJWT()
	token, _, err := signer.GenerateToken(originalIdentity())
	if err != nil {
		t.Fatal("could not sign synthetic original account token")
	}
	for _, tc := range []struct {
		name     string
		user     *models.TheMonkeysUser
		reassign bool
		wantCode codes.Code
	}{
		{"unchanged account", originalIdentity(), false, codes.OK},
		{"legitimate email and username changes", currentIdentity(), false, codes.OK},
		{"old email reassigned to B", currentIdentity(), true, codes.OK},
		{"removed A cannot become B", nil, true, codes.NotFound},
		{"suspended A cannot become B", func() *models.TheMonkeysUser {
			user := currentIdentity()
			user.UserStatus = "suspended"
			return user
		}(), true, codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, database := identityService(tc.user, tc.reassign)
			checkSignedIdentity(t, svc, token, tc.user, tc.wantCode)
			if database.emailLookups != 0 {
				t.Error("identity resolution used mutable email")
			}
		})
	}
}

// Historical supported access tokens omit token_type, but include account_id.
// Sign that actual legacy shape without changing production claims or minting.
func signIdentityClaims(t *testing.T, accountID *string, tokenType string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"email": "old-a@example.invalid", "username": "original-a",
		"iss": "identity-test", "exp": time.Now().Add(time.Hour).Unix(),
	}
	if accountID != nil {
		claims["account_id"] = *accountID
	}
	if tokenType != "" {
		claims["token_type"] = tokenType
	}
	signer := identityTestJWT()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(signer.SecretKey))
	if err != nil {
		t.Fatal("could not sign synthetic identity claims")
	}
	return token
}

func TestAuthIdentityLegacyTokensRequireAccountID(t *testing.T) {
	accountID, emptyID := "account-a", ""
	for _, tc := range []struct {
		name      string
		id        *string
		tokenType string
		wantCode  codes.Code
	}{
		{"supported legacy token", &accountID, "", codes.OK},
		{"legacy missing account ID", nil, "", codes.Unauthenticated},
		{"access missing account ID", nil, "access", codes.Unauthenticated},
		{"access empty account ID", &emptyID, "access", codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := currentIdentity()
			svc, database := identityService(user, true)
			token := signIdentityClaims(t, tc.id, tc.tokenType)
			checkSignedIdentity(t, svc, token, user, tc.wantCode)
			if database.emailLookups != 0 {
				t.Error("identity resolution fell back to email")
			}
		})
	}
}

func TestAuthIdentityPreservesTokenTypeRestrictions(t *testing.T) {
	id := "account-a"
	for _, tokenType := range []string{"password_reset", "refresh", "unknown"} {
		t.Run(tokenType, func(t *testing.T) {
			user := currentIdentity()
			svc, _ := identityService(user, true)
			token := signIdentityClaims(t, &id, tokenType)
			validation, err := svc.Validate(context.Background(), &pb.ValidateRequest{Token: token})
			if tokenType == "password_reset" {
				if err != nil || validation == nil || validation.AccountId != id || validation.UserName != user.Username || validation.Role != user.Role || validation.Email != user.Email {
					t.Error("password reset validation did not preserve its current account identity")
				}
			} else if status.Code(err) != codes.Unauthenticated || validation != nil {
				t.Error("non-access token accepted for validation")
			}
			session, err := svc.DecodeSignedJWT(context.Background(), &pb.DecodeSignedJWTRequest{Token: token})
			if status.Code(err) != codes.Unauthenticated || session != nil {
				t.Error("non-access token accepted for session decoding")
			}
		})
	}
}
