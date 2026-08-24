package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const razorpayBaseURL = "https://api.razorpay.com/v1"

// razorpay is a minimal client for the three calls the events service makes:
// create order, refund payment and verify webhook signatures. Talking to the
// REST API directly keeps the dependency tree (and the image) small.
type razorpay struct {
	keyID         string
	secret        string
	webhookSecret string
	http          *http.Client
}

func newRazorpay(keyID, secret, webhookSecret string) *razorpay {
	return &razorpay{
		keyID:         keyID,
		secret:        secret,
		webhookSecret: webhookSecret,
		http:          &http.Client{Timeout: 15 * time.Second},
	}
}

// enabled reports whether credentials are configured. When false the service
// still runs, but paid tiers are rejected instead of silently failing.
func (r *razorpay) enabled() bool { return r.keyID != "" && r.secret != "" }

// toPaise converts a rupee amount to the integer minor unit Razorpay expects.
func toPaise(amount float64) int64 { return int64(amount*100 + 0.5) }

func (r *razorpay) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, razorpayBaseURL+path, payload)
	if err != nil {
		return err
	}
	req.SetBasicAuth(r.keyID, r.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("razorpay request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("razorpay response unreadable: %w", err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("razorpay returned %d: %s", resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// createOrder opens a Razorpay order the frontend checkout widget settles.
func (r *razorpay) createOrder(ctx context.Context, amount float64, currency, receipt string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"amount":   toPaise(amount),
		"currency": currency,
		"receipt":  receipt,
	}
	if err := r.do(ctx, http.MethodPost, "/orders", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("razorpay returned an empty order id")
	}
	return out.ID, nil
}

// refund issues a full refund for a captured payment.
func (r *razorpay) refund(ctx context.Context, paymentID string, amount float64) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{"amount": toPaise(amount)}
	if err := r.do(ctx, http.MethodPost, "/payments/"+paymentID+"/refund", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// verifyWebhook checks the HMAC-SHA256 signature Razorpay sends alongside the
// payload. Comparison is constant time.
func (r *razorpay) verifyWebhook(body []byte, signature string) bool {
	if r.webhookSecret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(r.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// webhookEvent is the slice of the Razorpay webhook payload we act on.
type webhookEvent struct {
	Event   string `json:"event"`
	Payload struct {
		Payment struct {
			Entity struct {
				ID      string `json:"id"`
				OrderID string `json:"order_id"`
			} `json:"entity"`
		} `json:"payment"`
	} `json:"payload"`
}

func parseWebhook(body []byte) (*webhookEvent, error) {
	var e webhookEvent
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, fmt.Errorf("malformed webhook payload: %w", err)
	}
	return &e, nil
}
