package services

import (
	"context"

	"github.com/google/uuid"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
)

// Staff diagnostics are scoped to gateway-marked validation calls, not every
// public-page validation. Metadata never affects the authorization decision.
func (as *AuthzSvc) staffValidationLogger(ctx context.Context) *zap.SugaredLogger {
	if as.logger == nil {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	ids := md.Get(staffaccess.RequestIDMetadataKey)
	if len(ids) != 1 {
		return nil
	}
	id, err := uuid.Parse(ids[0])
	if err != nil || id.String() != ids[0] {
		return nil
	}
	return as.logger.With("staff_request_id", ids[0], "rpc", "Validate")
}
