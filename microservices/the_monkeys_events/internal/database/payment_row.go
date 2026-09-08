package database

import "github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/money"

// PaymentRow is the capture ledger snapshot written on webhook confirm.
type PaymentRow struct {
	EventID         int64
	AttendeeID      int64
	OrganizerUserID int64
	OrderID         string
	PaymentID       string
	Split           money.Split
	Status          string
}

func BuildPaymentRow(eventID, attendeeID, organizerID int64, orderID, paymentID string, grossPaise int64) PaymentRow {
	return PaymentRow{
		EventID:         eventID,
		AttendeeID:      attendeeID,
		OrganizerUserID: organizerID,
		OrderID:         orderID,
		PaymentID:       paymentID,
		Split:           money.SplitGross(grossPaise),
		Status:          "captured",
	}
}
