package moderation

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func scannerMockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db), mock
}

func expectScannerBlog(mock sqlmock.Sqlmock, id, publication string, found bool) {
	rows := sqlmock.NewRows([]string{"id", "user_id", "account_id", "status", "group_id", "audience", "visibility", "group_status"})
	if found {
		rows.AddRow(1, 2, "owner", publication, 0, "public", "", "")
	}
	mock.ExpectQuery("SELECT b.id,b.user_id").WithArgs(id).WillReturnRows(rows).RowsWillBeClosed()
}

func expectScannerAccess(mock sqlmock.Sqlmock, kind, id string) {
	mock.ExpectQuery("SELECT author_nsfw,staff_nsfw,scanner_nsfw").WithArgs(kind, id).
		WillReturnRows(sqlmock.NewRows([]string{"author_nsfw", "staff_nsfw", "scanner_nsfw", "revision", "scan_status"})).RowsWillBeClosed()
	if kind == "blog" {
		mock.ExpectQuery("SELECT EXISTS.*blog_admin_flags").WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false)).RowsWillBeClosed()
	}
}

const scannerBatchPattern = `(?s)WITH wanted AS.*unnest.*content_media_owners.*is_nsfw IS TRUE.*GROUP BY`

func TestStoreCheckBatchesScannerVisibleTargets(t *testing.T) {
	s, mock := scannerMockStore(t)
	mock.ExpectBegin()
	expectScannerBlog(mock, "same", "Published", true)
	mock.ExpectQuery("SELECT p.id,COALESCE").WithArgs("same").WillReturnRows(
		sqlmock.NewRows([]string{"id", "author", "account", "status", "group", "audience", "visibility", "group_status"}).
			AddRow(3, 2, "owner", "visible", 0, "public", "", "")).RowsWillBeClosed()
	expectScannerBlog(mock, "missing", "", false)
	expectScannerBlog(mock, "draft", "Draft", true)
	expectScannerBlog(mock, "same", "Published", true)
	mock.ExpectQuery(scannerBatchPattern).WithArgs(`{"blog","discussion"}`, `{"same","same"}`).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "target_id"}).AddRow("discussion", "same")).RowsWillBeClosed()
	expectScannerAccess(mock, "blog", "same")
	expectScannerAccess(mock, "discussion", "same")
	expectScannerAccess(mock, "blog", "same")
	mock.ExpectCommit()
	targets := []*pb.Target{{Kind: "blog", Id: "same"}, {Kind: "discussion", Id: "same"}, {Kind: "blog", Id: "missing"}, {Kind: "blog", Id: "draft"}, {Kind: "blog", Id: "same"}}
	got, err := s.Check(context.Background(), &pb.CheckRequest{Targets: targets})
	if err != nil {
		t.Fatalf("visible targets must share one scanner query: %v", err)
	}
	if len(got.Items) != 5 {
		t.Fatalf("lost ordered targets: %v", got)
	}
	for i, item := range got.Items {
		if item.Target.Kind != targets[i].Kind || item.Target.Id != targets[i].Id {
			t.Fatalf("target %d reordered: %v", i, item)
		}
		if i == 2 || i == 3 {
			if item.Reason != "not_found" || item.IsNsfw || item.Revision != 0 || item.ScanStatus != "" {
				t.Fatalf("inaccessible metadata leaked: %v", item)
			}
		} else if i == 1 {
			if !item.IsNsfw || item.Reason != "login_required" || item.CanReveal || item.Revealed {
				t.Fatalf("scanner flag crossed kind or bypassed eligibility: %v", item)
			}
		} else if item.IsNsfw || item.Revision != 1 || item.ScanStatus != "unknown" {
			t.Fatalf("safe missing rating changed: %v", item)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCheckScannerBatchFailsClosed(t *testing.T) {
	for _, failure := range []string{"query", "scan", "rows", "canceled", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			s, mock := scannerMockStore(t)
			mock.ExpectBegin()
			expectScannerBlog(mock, "one", "Published", true)
			expectScannerBlog(mock, "two", "Published", true)
			query := mock.ExpectQuery(scannerBatchPattern).WithArgs(`{"blog","blog"}`, `{"one","two"}`)
			want := codes.Internal
			switch failure {
			case "query":
				query.WillReturnError(errors.New("scanner database unavailable"))
			case "scan":
				query.WillReturnRows(sqlmock.NewRows([]string{"kind", "target_id"}).AddRow(nil, "one")).RowsWillBeClosed()
			case "rows":
				query.WillReturnRows(sqlmock.NewRows([]string{"kind", "target_id"}).AddRow("blog", "one").AddRow("blog", "two").RowError(1, errors.New("scanner rows failed"))).RowsWillBeClosed()
			case "canceled":
				query.WillReturnError(context.Canceled)
				want = codes.Canceled
			case "deadline":
				query.WillReturnError(context.DeadlineExceeded)
				want = codes.DeadlineExceeded
			}
			mock.ExpectRollback()
			got, err := s.Check(context.Background(), &pb.CheckRequest{Targets: []*pb.Target{{Kind: "blog", Id: "one"}, {Kind: "blog", Id: "two"}}})
			if got != nil || status.Code(err) != want {
				t.Fatalf("partial scanner results must fail closed: response=%v code=%v want=%v", got, status.Code(err), want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStoreCheckScannerSingleUniqueUsesScalar(t *testing.T) {
	s, mock := scannerMockStore(t)
	mock.ExpectBegin()
	expectScannerBlog(mock, "same", "Published", true)
	expectScannerBlog(mock, "same", "Published", true)
	mock.ExpectQuery("SELECT EXISTS.*content_media_owners").WithArgs("blog", "same").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true)).RowsWillBeClosed()
	expectScannerAccess(mock, "blog", "same")
	expectScannerAccess(mock, "blog", "same")
	mock.ExpectCommit()
	got, err := s.Check(context.Background(), &pb.CheckRequest{Targets: []*pb.Target{{Kind: "blog", Id: "same"}, {Kind: "blog", Id: "same"}}})
	if err != nil || len(got.GetItems()) != 2 || !got.Items[0].IsNsfw || !got.Items[1].IsNsfw {
		t.Fatalf("single unique scanner result not preserved: %v %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCheckScannerSkipsInaccessibleAndEmpty(t *testing.T) {
	for _, empty := range []bool{false, true} {
		s, mock := scannerMockStore(t)
		mock.ExpectBegin()
		var targets []*pb.Target
		if !empty {
			expectScannerBlog(mock, "draft", "Draft", true)
			expectScannerBlog(mock, "missing", "", false)
			targets = []*pb.Target{{Kind: "blog", Id: "draft"}, {Kind: "blog", Id: "missing"}}
		}
		mock.ExpectCommit()
		got, err := s.Check(context.Background(), &pb.CheckRequest{Targets: targets})
		if err != nil || len(got.GetItems()) != len(targets) {
			t.Fatalf("inaccessible/empty scanner work changed: %v %v", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}
