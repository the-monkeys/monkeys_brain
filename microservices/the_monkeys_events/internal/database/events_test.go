package database

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateWindow(t *testing.T) {
	start := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		from *timestamppb.Timestamp
		to   *timestamppb.Timestamp
		code codes.Code
	}{
		{
			name: "valid window",
			from: timestamppb.New(start),
			to:   timestamppb.New(start.Add(time.Hour)),
			code: codes.OK,
		},
		{
			name: "missing start",
			to:   timestamppb.New(start.Add(time.Hour)),
			code: codes.InvalidArgument,
		},
		{
			name: "same instant is invalid",
			from: timestamppb.New(start),
			to:   timestamppb.New(start),
			code: codes.InvalidArgument,
		},
		{
			name: "end before start is invalid",
			from: timestamppb.New(start),
			to:   timestamppb.New(start.Add(-time.Minute)),
			code: codes.InvalidArgument,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWindow(tc.from, tc.to)
			if got := status.Code(err); got != tc.code {
				t.Fatalf("status.Code(validateWindow()) = %s, want %s", got, tc.code)
			}
		})
	}
}

func TestDefaultTimezone(t *testing.T) {
	if got := defaultTimezone(""); got != "UTC" {
		t.Fatalf("defaultTimezone(\"\") = %q, want UTC", got)
	}
	if got := defaultTimezone("Asia/Kolkata"); got != "Asia/Kolkata" {
		t.Fatalf("defaultTimezone(non-empty) = %q", got)
	}
}

func TestApplyDiscountRoundsToTwoDecimals(t *testing.T) {
	cases := []struct {
		name     string
		price    float64
		discount float64
		want     float64
	}{
		{name: "zero discount", price: 99.99, discount: 0, want: 99.99},
		{name: "normal discount", price: 100, discount: 12.5, want: 87.5},
		{name: "rounds half up", price: 199.99, discount: 33.333, want: 133.33},
		{name: "caps below zero", price: 100, discount: 150, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyDiscount(tc.price, tc.discount); got != tc.want {
				t.Fatalf("applyDiscount(%v, %v) = %v, want %v", tc.price, tc.discount, got, tc.want)
			}
		})
	}
}
