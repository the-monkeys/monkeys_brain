package groups

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	eventpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GroupEventClient handles the group-scoped slice of the events surface:
// creating an event that belongs to a group. It delegates to the events
// service, stamping the group slug from the path so the event is attached.
//
// Listing a group's events is intentionally absent here: the events service's
// ListEvents RPC carries no group filter yet, so that route is deferred until
// the filter lands rather than faked with a client-side scan.
type GroupEventClient struct {
	client eventpb.EventServiceClient
	log    *zap.SugaredLogger
}

// GroupEventBody is the JSON payload for creating an event inside a group. It
// mirrors the standalone event body's core fields; group membership supplies
// the rest of the context on the service side.
type GroupEventBody struct {
	Title       string    `json:"title" binding:"required"`
	Description string    `json:"description"`
	StartTime   time.Time `json:"start_time" binding:"required"`
	EndTime     time.Time `json:"end_time" binding:"required"`
	Timezone    string    `json:"timezone"`
	EventType   string    `json:"event_type" binding:"required,oneof=virtual in_person hybrid"`
	Location    string    `json:"location"`
	MeetingLink string    `json:"meeting_link"`
	Capacity    int32     `json:"capacity"`
	CoverImage  string    `json:"cover_image"`
	Tags        []string  `json:"tags"`
	Visibility  string    `json:"visibility" binding:"omitempty,oneof=public group_members private unlisted"`
}

// eventClientInfo projects the request fingerprint onto the events service's
// ClientInfo type, distinct from the groups service's own message.
func eventClientInfo(ctx *gin.Context) *eventpb.ClientInfo {
	info := utils.GetClientInfo(ctx)
	return &eventpb.ClientInfo{
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

func toTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// CreateGroupEvent creates an event bound to the group named in the path. The
// events service re-checks that the caller may run events for this group.
func (gec *GroupEventClient) CreateGroupEvent(ctx *gin.Context) {
	body, ok := bind[GroupEventBody](ctx)
	if !ok {
		return
	}

	res, err := gec.client.CreateEvent(ctx, &eventpb.CreateEventReq{
		AccountId:   accountID(ctx),
		Title:       body.Title,
		Description: body.Description,
		StartTime:   toTimestamp(body.StartTime),
		EndTime:     toTimestamp(body.EndTime),
		Timezone:    body.Timezone,
		EventType:   body.EventType,
		Location:    body.Location,
		MeetingLink: body.MeetingLink,
		Capacity:    body.Capacity,
		CoverImage:  body.CoverImage,
		Tags:        body.Tags,
		GroupSlug:   ctx.Param("slug"),
		Visibility:  body.Visibility,
		ClientInfo:  eventClientInfo(ctx),
	})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			gec.log.Errorw("create group event failed", "err", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
			return
		}
		code, mapped := grpcToHTTP[st.Code()]
		if !mapped {
			code = http.StatusInternalServerError
			gec.log.Errorw("create group event failed", "code", st.Code(), "err", st.Message())
		}
		ctx.AbortWithStatusJSON(code, gin.H{"error": st.Message()})
		return
	}
	ctx.JSON(http.StatusCreated, res)
}
