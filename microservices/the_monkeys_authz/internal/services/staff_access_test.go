package services

import (
	"context"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/db"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type staffAccessDB struct {
	db.AuthDBHandler
	user   *models.TheMonkeysUser
	writes int
}

func (d *staffAccessDB) CheckIfEmailExist(string) (*models.TheMonkeysUser, error) { return d.user, nil }
func (d *staffAccessDB) CheckIfAccountIdExist(string) (*models.TheMonkeysUser, error) {
	return d.user, nil
}
func (d *staffAccessDB) CheckIfUsernameExist(string) (*models.TheMonkeysUser, error) {
	return d.user, nil
}
func (d *staffAccessDB) GetVerificationRequestByID(string) (*models.VerificationRequest, error) {
	return &models.VerificationRequest{Status: "pending", Username: "subject"}, nil
}
func (d *staffAccessDB) SetUserVerified(string, bool) error { d.writes++; return nil }
func (d *staffAccessDB) UpdateVerificationRequest(*models.VerificationRequest) error {
	d.writes++
	return nil
}

func TestVerificationServiceRejectsNonstaffAndSuspendedReviewer(t *testing.T) {
	for _, user := range []*models.TheMonkeysUser{
		{Role: "Viewer", UserStatus: "active"}, {Role: "Admin", UserStatus: "suspended"}, {Role: "Owner", UserStatus: "active"},
	} {
		db := &staffAccessDB{user: user}
		svc := &AuthzSvc{dbConn: db, logger: zap.NewNop().Sugar()}
		_, err := svc.ReviewUserVerification(context.Background(), &pb.ReviewUserVerificationReq{RequestId: "request-1", ReviewerUsername: "reviewer", Approved: true})
		if status.Code(err) != codes.PermissionDenied || db.writes != 0 {
			t.Fatalf("role %s status %s: err=%v writes=%d", user.Role, user.UserStatus, err, db.writes)
		}
	}
}

func TestCurrentSuspensionInvalidatesExistingTokens(t *testing.T) {
	jwt := utils.JwtWrapper{SecretKey: "local-test-secret", Issuer: "test", ExpirationHours: 1}
	user := &models.TheMonkeysUser{AccountId: "account-1", Username: "user", Email: "user@example.test", Role: "Admin", UserStatus: "active"}
	access, refresh, err := jwt.GenerateToken(user)
	if err != nil {
		t.Fatal(err)
	}
	user.UserStatus = "suspended"
	svc := &AuthzSvc{dbConn: &staffAccessDB{user: user}, jwt: jwt, logger: zap.NewNop().Sugar()}
	_, err = svc.Validate(context.Background(), &pb.ValidateRequest{Token: access})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("suspended access accepted: %v", err)
	}
	_, err = svc.DecodeSignedJWT(context.Background(), &pb.DecodeSignedJWTRequest{Token: access})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("suspended identity decoded: %v", err)
	}
	_, err = svc.RefreshToken(context.Background(), &pb.RefreshTokenReq{RefreshToken: refresh})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("suspended refresh accepted: %v", err)
	}
}

func TestSuspendedLoginDeniedBeforePasswordOrNotificationWork(t *testing.T) {
	user := &models.TheMonkeysUser{UserStatus: "suspended"}
	svc := &AuthzSvc{dbConn: &staffAccessDB{user: user}, logger: zap.NewNop().Sugar()}
	_, err := svc.Login(context.Background(), &pb.LoginUserRequest{Email: "user@example.test", Password: "irrelevant"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got %v", err)
	}
}
