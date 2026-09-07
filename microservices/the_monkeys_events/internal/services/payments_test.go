package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestOrderPayloadSendsPaise(t *testing.T) {
	body := orderPayload(100000, "INR", "evt-rsvp-1")
	if body["amount"] != int64(100000) {
		t.Fatalf("amount = %v, want 100000 paise", body["amount"])
	}
	if body["currency"] != "INR" {
		t.Fatalf("currency = %v", body["currency"])
	}
}

func TestRazorpayEnabledRequiresKeyAndSecret(t *testing.T) {
	if newRazorpay("", "secret", "").enabled() {
		t.Fatal("razorpay should be disabled without key id")
	}
	if newRazorpay("key", "", "").enabled() {
		t.Fatal("razorpay should be disabled without secret")
	}
	if !newRazorpay("key", "secret", "").enabled() {
		t.Fatal("razorpay should be enabled with key id and secret")
	}
}

func TestVerifyWebhook(t *testing.T) {
	body := []byte(`{"event":"payment.captured"}`)
	secret := "webhook-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	r := newRazorpay("", "", secret)
	if !r.verifyWebhook(body, signature) {
		t.Fatal("valid webhook signature was rejected")
	}
	if r.verifyWebhook(body, "bad-signature") {
		t.Fatal("invalid webhook signature was accepted")
	}
	if newRazorpay("", "", "").verifyWebhook(body, signature) {
		t.Fatal("webhook should be rejected when secret is missing")
	}
}

func TestParseWebhook(t *testing.T) {
	hook, err := parseWebhook([]byte(`{
		"event": "payment.captured",
		"payload": {
			"payment": {
				"entity": {
					"id": "pay_123",
					"order_id": "order_456"
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parseWebhook returned error: %v", err)
	}
	if hook.Event != "payment.captured" {
		t.Fatalf("Event = %q", hook.Event)
	}
	if hook.Payload.Payment.Entity.ID != "pay_123" {
		t.Fatalf("payment id = %q", hook.Payload.Payment.Entity.ID)
	}
	if hook.Payload.Payment.Entity.OrderID != "order_456" {
		t.Fatalf("order id = %q", hook.Payload.Payment.Entity.OrderID)
	}

	if _, err := parseWebhook([]byte(`{`)); err == nil {
		t.Fatal("malformed webhook should fail")
	}
}
