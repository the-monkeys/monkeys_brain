package services

import (
	"strings"
	"time"
	_ "time/tzdata" // Scheduling must work even without host OS zoneinfo files.

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// validateSchedule validates metadata, never reinterprets the submitted instants.
func validateSchedule(start, end *timestamppb.Timestamp, timezone string) (*time.Location, error) {
	zone := strings.TrimSpace(timezone)
	if zone == "" {
		zone = "UTC"
	} // Backward compatible with clients omitting timezone.
	if zone != "UTC" && !strings.Contains(zone, "/") {
		return nil, status.Error(codes.InvalidArgument, "timezone must be an IANA identifier, for example Asia/Kolkata")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "timezone must be a valid IANA identifier, for example Asia/Kolkata")
	}
	if start.CheckValid() != nil || end.CheckValid() != nil || !end.AsTime().After(start.AsTime()) {
		return nil, status.Error(codes.InvalidArgument, "valid start_time and end_time are required, with end_time after start_time")
	}
	return location, nil
}
