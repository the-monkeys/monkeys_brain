package events

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// EventServiceClient adapts the events gRPC service to REST.
type EventServiceClient struct {
	Client pb.EventServiceClient
	log    *zap.SugaredLogger
	// baseURL is the canonical public origin (e.g. https://monkeys.com.co) used
	// to build Open Graph / social share links. Sourced from config (SEO_BASE_URL)
	// so it is never hardcoded to a stale domain.
	baseURL string
}

func NewEventServiceClient(cfg *config.Config, lg *zap.SugaredLogger) pb.EventServiceClient {
	addr := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysEvents, cfg.Microservices.EventsPort)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		lg.Errorw("cannot create gRPC events client", "err", err, "addr", addr)
		return nil
	}

	lg.Debugw("connected to events service", "addr", addr)
	return pb.NewEventServiceClient(conn)
}

// clientInfo projects the gateway's request fingerprint onto the subset the
// events service records.
func clientInfo(ctx *gin.Context) *pb.ClientInfo {
	info := utils.GetClientInfo(ctx)
	return &pb.ClientInfo{
		IpAddress:  info.IPAddress,
		Client:     info.ClientType,
		SessionId:  info.SessionID,
		UserAgent:  info.UserAgent,
		Referrer:   info.Referrer,
		Platform:   info.Platform,
		Timezone:   info.Timezone,
		DeviceType: info.DeviceType,
		Os:         info.Os,
		Browser:    info.Browser,
	}
}

// grpcToHTTP maps gRPC status codes onto their HTTP equivalents so clients
// get meaningful statuses instead of a blanket 500.
var grpcToHTTP = map[codes.Code]int{
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.FailedPrecondition: http.StatusConflict,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.DeadlineExceeded:   http.StatusGatewayTimeout,
}

// fail translates a gRPC error into a JSON response. It returns true when an
// error was written, letting handlers read as `if fail(...) { return }`.
func (esc *EventServiceClient) fail(ctx *gin.Context, err error, action string) bool {
	if err == nil {
		return false
	}

	st, ok := status.FromError(err)
	if !ok {
		esc.log.Errorw("events request failed", "action", action, "err", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Something went wrong. Try again."})
		return true
	}

	code, mapped := grpcToHTTP[st.Code()]
	msg := st.Message()
	if !mapped {
		code = http.StatusInternalServerError
		esc.log.Errorw("events request failed", "action", action, "code", st.Code(), "err", st.Message())
		msg = "Something went wrong. Try again."
	}
	ctx.AbortWithStatusJSON(code, gin.H{"error": msg})
	return true
}
