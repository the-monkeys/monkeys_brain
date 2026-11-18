package user_service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/idempotency"
)

// Store only the server-created resource id. Verification proof, checksums,
// object keys, and additional_info never enter the Redis replay record.
func verificationReference(body []byte) ([]byte, error) {
	var reference struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &reference); err != nil {
		return nil, err
	}
	if reference.ID == "" || len(reference.ID) > 128 {
		return nil, errors.New("invalid verification reference")
	}
	return json.Marshal(reference)
}

func (usc *UserServiceClient) verificationIdempotency(guard *idempotency.Guard) gin.HandlerFunc {
	return guard.PrivateJSON(verificationReference, usc.resolveVerificationReplay)
}

func (usc *UserServiceClient) resolveVerificationReplay(c *gin.Context, reference []byte) ([]byte, error) {
	var stored struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(reference, &stored); err != nil {
		return nil, err
	}
	username := c.GetString("userName")
	if stored.ID == "" || len(stored.ID) > 128 || username == "" {
		return nil, errors.New("invalid verification reference")
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	res, err := usc.Client.GetMyVerification(ctx, &pb.GetVerificationReq{Username: username, RequestId: stored.ID})
	if err != nil {
		return nil, err
	}
	// The users service's request-id lookup also serves admins and does not check
	// ownership. Enforce both identity and resource id here before replaying any
	// private response; never fall back to the caller's latest verification.
	if res == nil || res.Id != stored.ID || res.Username != username {
		return nil, errors.New("verification replay ownership mismatch")
	}
	return json.Marshal(res)
}
