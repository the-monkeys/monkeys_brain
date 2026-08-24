package events

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

// AttendanceBody is the JSON payload for a host updating one attendee's
// standing. attendance_status is validated at the boundary so an unknown state
// never reaches the service.
type AttendanceBody struct {
	AttendanceStatus string `json:"attendance_status" binding:"required,oneof=registered checked_in no_show not_coming"`
	CheckedIn        bool   `json:"checked_in"`
}

// saveAction is the shared shape of the bookmark verbs, which act on the
// caller's own saved list and so need only a visible event.
func (esc *EventServiceClient) saveAction(ctx *gin.Context) *pb.SaveEventReq {
	return &pb.SaveEventReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	}
}

// SaveEvent bookmarks the event for the caller. Idempotent: saving an
// already-saved event is a no-op the service reports as success.
func (esc *EventServiceClient) SaveEvent(ctx *gin.Context) {
	res, err := esc.Client.SaveEvent(ctx, esc.saveAction(ctx))
	if esc.fail(ctx, err, "save event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// UnsaveEvent removes the caller's bookmark. Idempotent in the same way.
func (esc *EventServiceClient) UnsaveEvent(ctx *gin.Context) {
	res, err := esc.Client.UnsaveEvent(ctx, esc.saveAction(ctx))
	if esc.fail(ctx, err, "unsave event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// UpdateAttendance lets a host mark an attendee registered, checked in, a
// no-show, or withdrawn. The attendee is identified by the numeric id in the
// path; the caller's authority is re-checked by the service.
func (esc *EventServiceClient) UpdateAttendance(ctx *gin.Context) {
	attendeeID, ok := pathID(ctx, "id")
	if !ok {
		return
	}
	body, ok := bind[AttendanceBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.UpdateAttendance(ctx, &pb.UpdateAttendanceReq{
		EventSlug:        ctx.Param("slug"),
		AccountId:        accountID(ctx),
		AttendeeId:       attendeeID,
		AttendanceStatus: body.AttendanceStatus,
		CheckedIn:        body.CheckedIn,
		ClientInfo:       clientInfo(ctx),
	})
	if esc.fail(ctx, err, "update attendance") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}
