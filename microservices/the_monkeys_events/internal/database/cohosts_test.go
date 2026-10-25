package database

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRemoveCoHostRecordsVoluntaryLeave(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM events WHERE slug = \$1 FOR UPDATE`).WithArgs("local-event").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery(`SELECT id FROM user_account WHERE account_id`).WithArgs("self").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
	mock.ExpectQuery(`SELECT id, organizer_id FROM events`).WithArgs("local-event").WillReturnRows(sqlmock.NewRows([]string{"id", "organizer_id"}).AddRow(7, 1))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM event_permissions`).WithArgs(int64(7), int64(2), permManageHosts).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id FROM user_account WHERE username`).WithArgs("guest").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
	mock.ExpectExec(`DELETE FROM event_co_hosts`).WithArgs(int64(7), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM event_permissions`).WithArgs(int64(7), int64(2)).WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec(`INSERT INTO event_host_activity_log`).WithArgs(int64(7), int64(2), "left", int64(2)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err = (&eventDB{db: db}).RemoveCoHost(context.Background(), &pb.CoHostReq{EventSlug: "local-event", AccountId: "self", CohostUsername: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCoHostChangesLockEventBeforeCheckingPermissions(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "add", true: "remove"}[remove], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id FROM events WHERE slug = \$1 FOR UPDATE`).WithArgs("local-event").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
			mock.ExpectQuery(`SELECT id FROM user_account WHERE account_id`).WithArgs("revoked").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
			mock.ExpectQuery(`SELECT id, organizer_id FROM events`).WithArgs("local-event").WillReturnRows(sqlmock.NewRows([]string{"id", "organizer_id"}).AddRow(7, 1))
			mock.ExpectQuery(`SELECT COUNT\(1\) FROM event_permissions`).WithArgs(int64(7), int64(2), permManageHosts).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectRollback()
			req := &pb.CoHostReq{EventSlug: "local-event", AccountId: "revoked", CohostUsername: "other"}
			reader := &eventDB{db: db}
			if remove {
				err = reader.RemoveCoHost(context.Background(), req)
			} else {
				err = reader.AddCoHost(context.Background(), req)
			}
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("err=%v", err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAddCoHostDuplicateDoesNotGrantOrAuditAgain(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec(`INSERT INTO event_co_hosts`).WithArgs(int64(7), int64(2), defaultCoHostRole, int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	if err = addCoHost(context.Background(), db, 7, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
