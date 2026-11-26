package services

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type birthdayProfileDB struct {
	database.UserDb
	profile models.UserProfileRes
	updated *models.UserProfileRes
}

func (db *birthdayProfileDB) CheckIfUsernameExist(string) (*models.TheMonkeysUser, error) {
	return &models.TheMonkeysUser{AccountId: "fixture"}, nil
}
func (db *birthdayProfileDB) GetMyProfile(string) (*models.UserProfileRes, error) {
	copy := db.profile
	return &copy, nil
}
func (db *birthdayProfileDB) UpdateUserProfile(_ string, p *models.UserProfileRes) error {
	db.updated = p
	return nil
}

func TestProfileBirthdayUpdateCompatibility(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, input := range []string{"1992-02-29", "1992-02-29 00:00:00 +0000 UTC", "1992-02-29 00:00:00 +0530 IST", "1992-02-29 00:00:00 +0530 +0530", "1992-02-29T00:00:00+05:30"} {
			t.Run(input+map[bool]string{false: " PUT", true: " PATCH"}[partial], func(t *testing.T) {
				db := &birthdayProfileDB{}
				svc := &UserSvc{dbConn: db, log: zap.NewNop().Sugar()}
				_, err := svc.UpdateUserProfile(context.Background(), &pb.UpdateUserProfileReq{Username: "fixture", DateOfBirth: input, Partial: partial})
				if err != nil {
					t.Fatal(err)
				}
				if db.updated == nil || !db.updated.DateOfBirth.Valid || db.updated.DateOfBirth.Time.Format(time.DateOnly) != "1992-02-29" {
					t.Fatal("valid birthday must survive update with calendar date and nullable validity intact")
				}
			})
		}
	}
}
func TestProfileBirthdayRejectsInvalidBeforeWriting(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, input := range []string{"not-a-date", "1991-02-29", "1990-01-02 extra", "1990-01-02Tbad", "0000-01-01", "0001-02-01", "1992-02-29T00:00:00+24:00", "1992-02-29T00:00:00+05:60"} {
			db := &birthdayProfileDB{}
			svc := &UserSvc{dbConn: db, log: zap.NewNop().Sugar()}
			_, err := svc.UpdateUserProfile(context.Background(), &pb.UpdateUserProfileReq{Username: "fixture", DateOfBirth: input, FirstName: "Must not save", Partial: partial})
			if status.Code(err) != codes.InvalidArgument || db.updated != nil {
				t.Errorf("invalid date partial=%v must reject the entire update before writing", partial)
			}
		}
	}
}
func TestProfileBirthdayMissingUpdateDoesNotReplaceSavedValue(t *testing.T) {
	saved := sql.NullTime{Time: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true}
	for _, partial := range []bool{false, true} {
		for _, input := range []string{"", "0001-01-01", "0001-01-01 00:00:00 +0000 UTC", "0001-01-01T00:00:00Z"} {
			db := &birthdayProfileDB{profile: models.UserProfileRes{DateOfBirth: saved}}
			svc := &UserSvc{dbConn: db, log: zap.NewNop().Sugar()}
			_, err := svc.UpdateUserProfile(context.Background(), &pb.UpdateUserProfileReq{Username: "fixture", DateOfBirth: input, Partial: partial})
			if err != nil {
				t.Fatal(err)
			}
			if db.updated == nil || db.updated.DateOfBirth.Valid {
				t.Fatal("missing birthday must pass no replacement to the database")
			}
		}
	}
}
func TestPrivateProfileBirthdaySerialization(t *testing.T) {
	valid := sql.NullTime{Time: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true}
	for _, tc := range []struct {
		name string
		dob  sql.NullTime
		want string
	}{
		{"null", sql.NullTime{}, ""},
		{"legacy zero", sql.NullTime{Valid: true}, ""},
		{"existing format", valid, valid.Time.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &UserSvc{dbConn: &birthdayProfileDB{profile: models.UserProfileRes{DateOfBirth: tc.dob}}, log: zap.NewNop().Sugar()}
			res, err := svc.GetUserProfile(context.Background(), &pb.UserProfileReq{Username: "fixture", IsPrivate: true})
			if err != nil {
				t.Fatal(err)
			}
			if res.DateOfBirth != tc.want {
				t.Fatal("private birthday must preserve existing format and leave missing dates empty")
			}
		})
	}
}
