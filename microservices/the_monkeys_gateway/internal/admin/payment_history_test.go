package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type paymentHistoryClient struct{ pb.EventServiceClient }

func (*paymentHistoryClient) AdminGetEventPayments(context.Context, *pb.AdminGetEventPaymentsReq, ...grpc.CallOption) (*pb.AdminGetEventPaymentsResp, error) {
	at := timestamppb.New(time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC))
	return &pb.AdminGetEventPaymentsResp{
		Event:       &pb.AdminEventPaymentSummary{Slug: "event", Settled: &pb.MoneyINR{Paise: 94100}, PendingSettlements: &pb.MoneyINR{Paise: 0}, PaidSettlements: &pb.MoneyINR{Paise: 94100}, HostBalance: &pb.MoneyINR{Paise: -94100}},
		Payments:    []*pb.AdminPaymentLine{{Id: 33, CapturedAt: at, RefundGross: &pb.MoneyINR{Paise: 100000}, RazorpayRefundId: "refund_test", RefundedAt: at, Currency: "INR"}, {}},
		Settlements: []*pb.AdminSettlementLine{{Id: 12, CreatedAt: at, MarkedPaidAt: at, MarkedPaidBy: "staff", Currency: "INR"}, {}},
	}, nil
}

func TestPaymentDetailJSONIncludesBalancesAndEvidence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	asc := &AdminServiceClient{Events: &paymentHistoryClient{}, logger: zap.NewNop().Sugar()}
	r.GET("/payments/:slug", asc.GetEventPayments)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/payments/event", nil))
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Payments    []map[string]any `json:"payments"`
		Settlements []map[string]any `json:"settlements"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	event := body["event"].(map[string]any)
	for name, want := range map[string]float64{"settled": 94100, "pending_settlements": 0, "paid_settlements": 94100, "host_balance": -94100} {
		m, ok := event[name].(map[string]any)
		if !ok || m["paise"] != want {
			t.Errorf("%s = %v, want paise %v", name, event[name], want)
		}
	}
	p := got.Payments[0]
	for name, want := range map[string]any{"id": float64(33), "captured_at": "2026-10-15T10:00:00Z", "refunded_at": "2026-10-15T10:00:00Z", "razorpay_refund_id": "refund_test", "currency": "INR"} {
		if p[name] != want {
			t.Errorf("payment %s = %v, want %v", name, p[name], want)
		}
	}
	if m, ok := p["refund_gross"].(map[string]any); !ok || m["paise"] != float64(100000) {
		t.Errorf("missing refund amount: %v", p)
	}
	s := got.Settlements[0]
	for name, want := range map[string]any{"created_at": "2026-10-15T10:00:00Z", "marked_paid_at": "2026-10-15T10:00:00Z", "marked_paid_by": "staff", "currency": "INR"} {
		if s[name] != want {
			t.Errorf("settlement %s = %v, want %v", name, s[name], want)
		}
	}
	if got.Payments[1]["refunded_at"] != nil || got.Settlements[1]["marked_paid_at"] != nil {
		t.Fatal("absent dates must remain null")
	}
	if got.Payments[1]["refund_gross"] != nil {
		t.Fatal("absent refund evidence from older event services must remain unavailable")
	}
}

func TestOlderEventServiceDoesNotInventPaidBalances(t *testing.T) {
	got := summaryJSON(&pb.AdminEventPaymentSummary{Settled: &pb.MoneyINR{Paise: 94100}})
	for _, name := range []string{"pending_settlements", "paid_settlements", "host_balance"} {
		if got[name] != nil {
			t.Errorf("%s must be null when absent from an older event service; got %v", name, got[name])
		}
	}
}
