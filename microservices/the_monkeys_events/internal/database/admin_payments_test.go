package database

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var financeSummaryColumns = []string{"id", "slug", "title", "username", "start_time", "gross", "fee", "gst", "host", "refunded", "settled", "pending", "paid"}

func TestAdminListSeparatesReservedAndPaidAndSearchesHost(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	db := &eventDB{db: sqldb}
	// Exercise the real query and scanner; both queries must search the host.
	mock.ExpectQuery(`SELECT COUNT\(1\).*u.username ILIKE`).WithArgs("host", "").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT e.id.*FILTER \(WHERE status = 'pending'\).*FILTER \(WHERE status = 'paid'\).*u.username ILIKE`).
		WithArgs("host", "", int32(20), int32(0)).WillReturnRows(sqlmock.NewRows(financeSummaryColumns).
		AddRow(7, "event", "Event", "host", nil, 100000, 5000, 900, 94100, 0, 30000, 10000, 20000))
	got, err := db.AdminListEventPayments(context.Background(), &pb.AdminListEventPaymentsReq{Query: "host"})
	if err != nil {
		t.Fatal(err)
	}
	s := got.Events[0]
	if s.GetSettled().GetPaise() != 30000 || s.GetPendingSettlements().GetPaise() != 10000 || s.GetPaidSettlements().GetPaise() != 20000 || s.GetHostBalance().GetPaise() != 74100 || s.GetOpenPayable().GetPaise() != 64100 {
		t.Fatalf("incorrect finance summary: %v", s)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminPaymentHistoryPreservesDebtAndTransactionEvidence(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	db := &eventDB{db: sqldb}
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT e.id.*FILTER \(WHERE status = 'pending'\).*FILTER \(WHERE status = 'paid'\)`).WithArgs("event").
		WillReturnRows(sqlmock.NewRows(financeSummaryColumns).AddRow(7, "event", "Event", "host", at, 0, 0, 0, 0, 100000, 94100, 0, 94100))
	mock.ExpectQuery(`FROM event_payments p`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{
		"username", "order_id", "payment_id", "status", "gross", "fee", "gst", "host", "id", "captured_at", "refund", "refund_id", "refunded_at", "currency",
	}).AddRow("guest", "order_test", "pay_test", "refunded", 100000, 5000, 900, 94100, 33, at, 100000, "rfnd_test", at.Add(time.Hour), "INR").
		AddRow("guest2", "order_2", "pay_2", "captured", 100000, 5000, 900, 94100, 34, at, 0, "", nil, "INR"))
	mock.ExpectQuery(`FROM event_host_settlements`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{
		"id", "payable", "status", "note", "created_at", "marked_paid_at", "marked_paid_by", "currency",
	}).AddRow(12, 94100, "paid", "NEFT test reference", at, at.Add(time.Hour), "admin account Admin", "INR").
		AddRow(13, 1, "cancelled", "Cancelled", at, nil, "", "INR"))
	got, err := db.AdminGetEventPayments(context.Background(), &pb.AdminGetEventPaymentsReq{Slug: "event"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Event.GetHostBalance().GetPaise() != -94100 || got.Event.GetOpenPayable().GetPaise() != 0 {
		t.Fatalf("refund after payout must expose debt: %v", got.Event)
	}
	p := got.Payments[0]
	if p.GetId() != 33 || p.GetRefundGross().GetPaise() != 100000 || p.GetRazorpayRefundId() != "rfnd_test" || !p.GetCapturedAt().AsTime().Equal(at) || !p.GetRefundedAt().AsTime().Equal(at.Add(time.Hour)) || p.GetCurrency() != "INR" {
		t.Fatalf("missing payment evidence: %v", p)
	}
	s := got.Settlements[0]
	if s.GetMarkedPaidBy() != "admin account Admin" || !s.GetCreatedAt().AsTime().Equal(at) || !s.GetMarkedPaidAt().AsTime().Equal(at.Add(time.Hour)) || s.GetCurrency() != "INR" {
		t.Fatalf("missing payout evidence: %v", s)
	}
	if got.Payments[1].RefundedAt != nil || got.Settlements[1].MarkedPaidAt != nil {
		t.Fatal("missing dates must remain missing")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCannotUnpublishWithPaid(t *testing.T) {
	if !cannotUnpublishWithPaid(1) {
		t.Fatal("paid attendees must block unpublish")
	}
	if cannotUnpublishWithPaid(0) {
		t.Fatal("zero paid should allow unpublish")
	}
}

func TestErrNothingToSettle(t *testing.T) {
	if err := errNothingToSettle(0); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("open 0: %v", err)
	}
	if err := errNothingToSettle(-1); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("open negative: %v", err)
	}
	if err := errNothingToSettle(1); err != nil {
		t.Fatal(err)
	}
}
