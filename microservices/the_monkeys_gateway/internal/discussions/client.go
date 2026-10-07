package discussions

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Matches the discussions service. Hide rides on DeleteDiscussion.
const (
	discussionActionHeader = "x-discussion-action"
	discussionActionHide   = "hide"
)

// Client adapts the discussions gRPC service to REST.
type Client struct {
	svc pb.DiscussionServiceClient
	log *zap.SugaredLogger
}

func dial(cfg *config.Config, lg *zap.SugaredLogger) pb.DiscussionServiceClient {
	port := cfg.Microservices.DiscussionsPort
	if port == 0 {
		port = 50064
	}
	addr := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysDiscussions, port)
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		lg.Errorw("cannot create gRPC discussions client", "err", err, "addr", addr)
		return nil
	}
	lg.Debugw("connected to discussions service", "addr", addr)
	return pb.NewDiscussionServiceClient(conn)
}

var grpcToHTTP = map[codes.Code]int{
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.NotFound:           http.StatusNotFound,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.FailedPrecondition: http.StatusConflict,
}

// fail writes the gRPC status as a human JSON error. It returns true when it wrote one.
func (c *Client) fail(ctx *gin.Context, err error, action string) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		c.log.Errorw("discussions request failed", "action", action, "err", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return true
	}
	code, mapped := grpcToHTTP[st.Code()]
	if !mapped {
		code = http.StatusInternalServerError
		c.log.Errorw("discussions request failed", "action", action, "code", st.Code(), "err", st.Message())
	}
	ctx.AbortWithStatusJSON(code, gin.H{"error": st.Message()})
	return true
}
