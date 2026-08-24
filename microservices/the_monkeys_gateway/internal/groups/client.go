package groups

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// GroupServiceClient adapts the groups gRPC service to REST.
type GroupServiceClient struct {
	Client pb.GroupServiceClient
	log    *zap.SugaredLogger
}

func NewGroupServiceClient(cfg *config.Config, lg *zap.SugaredLogger) pb.GroupServiceClient {
	addr := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysGroups, cfg.Microservices.GroupsPort)

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		lg.Errorw("cannot create gRPC groups client", "err", err, "addr", addr)
		return nil
	}

	lg.Debugw("connected to groups service", "addr", addr)
	return pb.NewGroupServiceClient(conn)
}

// clientInfo projects the gateway's request fingerprint onto the subset the
// groups service records.
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

// grpcToHTTP maps gRPC status codes onto their HTTP equivalents so clients get
// meaningful statuses instead of a blanket 500.
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
func (gsc *GroupServiceClient) fail(ctx *gin.Context, err error, action string) bool {
	if err == nil {
		return false
	}

	st, ok := status.FromError(err)
	if !ok {
		gsc.log.Errorw("groups request failed", "action", action, "err", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return true
	}

	code, mapped := grpcToHTTP[st.Code()]
	if !mapped {
		code = http.StatusInternalServerError
		gsc.log.Errorw("groups request failed", "action", action, "code", st.Code(), "err", st.Message())
	}
	ctx.AbortWithStatusJSON(code, gin.H{"error": st.Message()})
	return true
}
