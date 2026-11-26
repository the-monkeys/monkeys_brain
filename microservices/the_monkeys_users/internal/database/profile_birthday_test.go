package database

import (
	"database/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"go.uber.org/zap"
	"regexp"
	"testing"
	"time"
)

func TestProfileBirthdaySQLPreservesMissingAndBindsValidDate(t *testing.T) {
	for _, valid := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		birthday := sql.NullTime{Time: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), Valid: valid}
		var want interface{}
		if valid {
			want = birthday.Time
		}
		mock.ExpectBegin()
		mock.ExpectPrepare(`(?s)UPDATE user_account.*`+regexp.QuoteMeta("date_of_birth = COALESCE($3, date_of_birth)")+`.*WHERE username = \$11`).
			ExpectQuery().WithArgs("Local", "Fixture", want, "", "", "", "", "", "", "", "fixture").
			WillReturnRows(sqlmock.NewRows([]string{}))
		mock.ExpectCommit()
		handler := &uDBHandler{db: db, log: zap.NewNop().Sugar()}
		err = handler.UpdateUserProfile("fixture", &models.UserProfileRes{FirstName: "Local", LastName: "Fixture", DateOfBirth: birthday})
		if err != nil {
			t.Fatal(err)
		}
		if err = mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}
