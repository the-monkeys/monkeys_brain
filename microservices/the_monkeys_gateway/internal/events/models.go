package events

import (
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// EventBody is the JSON payload for creating and updating an event.
type EventBody struct {
	Title           string     `json:"title" binding:"required"`
	Description     string     `json:"description"`
	StartTime       time.Time  `json:"start_time" binding:"required"`
	EndTime         time.Time  `json:"end_time" binding:"required"`
	Timezone        string     `json:"timezone"`
	EventType       string     `json:"event_type" binding:"required,oneof=virtual in_person hybrid"`
	Location        string     `json:"location"`
	MeetingLink     string     `json:"meeting_link"`
	Capacity        int32      `json:"capacity"`
	CoverImage      string     `json:"cover_image"`
	Tags            []string   `json:"tags"`
	CoHostUsernames []string   `json:"co_host_usernames"`
	TicketTiers     []TierBody `json:"ticket_tiers"`
	// GroupSlug attaches the event to a community the caller organizes; empty
	// means a standalone event. Visibility defaults to public server-side.
	GroupSlug  string          `json:"group_slug"`
	Visibility string          `json:"visibility" binding:"omitempty,oneof=public group_members private unlisted"`
	Recurrence *RecurrenceBody `json:"recurrence"`
}

type RecurrenceBody struct {
	Freq     string     `json:"freq"`
	Interval int32      `json:"interval"`
	ByDay    []string   `json:"by_day"`
	Count    int32      `json:"count"`
	Until    *time.Time `json:"until"`
}

func (r *RecurrenceBody) toProto() *pb.Recurrence {
	if r == nil || r.Freq == "" || r.Freq == "off" {
		return nil
	}
	interval := r.Interval
	if interval < 1 {
		interval = 1
	}
	return &pb.Recurrence{
		Freq:     r.Freq,
		Interval: interval,
		ByDay:    r.ByDay,
		Count:    r.Count,
		Until:    toTimestamp(r.Until),
	}
}

type CloneBody struct {
	StartTime time.Time `json:"start_time" binding:"required"`
	EndTime   time.Time `json:"end_time" binding:"required"`
}

// TierBody is the JSON payload for a ticket tier.
type TierBody struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Capacity    int32   `json:"capacity"`
	SortOrder   int32   `json:"sort_order"`
}

func (t TierBody) toProto() *pb.TicketTierInput {
	return &pb.TicketTierInput{
		Name:        t.Name,
		Description: t.Description,
		Price:       t.Price,
		Currency:    t.Currency,
		Capacity:    t.Capacity,
		SortOrder:   t.SortOrder,
	}
}

func tiersToProto(in []TierBody) []*pb.TicketTierInput {
	out := make([]*pb.TicketTierInput, 0, len(in))
	for _, t := range in {
		out = append(out, t.toProto())
	}
	return out
}

// CouponBody is the JSON payload for creating a coupon.
type CouponBody struct {
	Code            string     `json:"code" binding:"required"`
	DiscountPercent float64    `json:"discount_percent" binding:"required,gt=0,lte=100"`
	MaxUses         int32      `json:"max_uses"`
	ValidFrom       *time.Time `json:"valid_from"`
	ValidTo         *time.Time `json:"valid_to"`
}

// RSVPBody is the JSON payload for responding to an event.
type RSVPBody struct {
	TicketTierID int64  `json:"ticket_tier_id" binding:"required"`
	CouponCode   string `json:"coupon_code"`
}

// CommentBody is the JSON payload for posting a comment.
type CommentBody struct {
	CommentText string `json:"comment_text" binding:"required"`
}

// ReactionBody is the JSON payload for adding or removing a reaction.
type ReactionBody struct {
	ReactionType string `json:"reaction_type" binding:"required"`
}

// ReportBody is the JSON payload for flagging an event.
type ReportBody struct {
	Reason string `json:"reason" binding:"required"`
}

// CoHostBody is the JSON payload for granting hosting rights.
type CoHostBody struct {
	Username string `json:"username" binding:"required"`
}

// ValidateCouponBody is the JSON payload for quoting a discount.
type ValidateCouponBody struct {
	Code         string `json:"code" binding:"required"`
	TicketTierID int64  `json:"ticket_tier_id"`
}

func toTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}
