package services

import (
	"context"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetActivityAnalytics retrieves user behavior analytics
func (s *ActivityServiceServer) GetActivityAnalytics(ctx context.Context, req *pb.GetActivityAnalyticsRequest) (*pb.GetActivityAnalyticsResponse, error) {
	s.logger.Debugw("GetActivityAnalytics called",
		"user_id", req.GetUserId(),
		"account_id", req.GetAccountId(),
		"time_range", req.GetTimeRange(),
	)

	// Validate required fields
	if req.GetUserId() == "" {
		return &pb.GetActivityAnalyticsResponse{
			StatusCode: 400,
			Error: &pb.Error{
				Status:  400,
				Error:   "validation_error",
				Message: "user_id is required",
			},
		}, nil
	}

	// Mock analytics response
	mockAnalytics := &pb.UserBehaviorAnalytics{
		UserId:               req.GetUserId(),
		AccountId:            req.GetAccountId(),
		TimeRange:            req.GetTimeRange(),
		TotalActivities:      150,
		UniqueActions:        25,
		SessionCount:         10,
		AvgSessionDurationMs: 300000, // 5 minutes
		HourlyPattern: []*pb.HourlyActivity{
			{Hour: 9, Count: 20},
			{Hour: 14, Count: 35},
			{Hour: 19, Count: 15},
		},
		LocationPattern: []*pb.LocationActivity{
			{Country: "US", Count: 120},
			{Country: "CA", Count: 30},
		},
		EngagementScore: 0.85,
		RiskScore:       5,
		LastActivity:    timestamppb.Now(),
		CreatedAt:       timestamppb.Now(),
		UpdatedAt:       timestamppb.Now(),
	}

	return &pb.GetActivityAnalyticsResponse{
		StatusCode: 200,
		Analytics:  mockAnalytics,
	}, nil
}

// TrackSecurityEvent records a security event
func (s *ActivityServiceServer) TrackSecurityEvent(ctx context.Context, req *pb.TrackSecurityEventRequest) (*pb.TrackSecurityEventResponse, error) {
	s.logger.Debugw("TrackSecurityEvent called",
		"user_id", req.GetUserId(),
		"event_type", req.GetEventType(),
		"severity", req.GetSeverity(),
	)

	// Validate required fields
	if req.GetUserId() == "" {
		return &pb.TrackSecurityEventResponse{
			StatusCode: 400,
			Message:    "user_id is required",
			Error: &pb.Error{
				Status:  400,
				Error:   "validation_error",
				Message: "user_id is required",
			},
		}, nil
	}

	if req.GetEventType() == "" {
		return &pb.TrackSecurityEventResponse{
			StatusCode: 400,
			Message:    "event_type is required",
			Error: &pb.Error{
				Status:  400,
				Error:   "validation_error",
				Message: "event_type is required",
			},
		}, nil
	}

	// Save to Elasticsearch
	eventID, err := s.db.SaveSecurityEvent(ctx, req)
	if err != nil {
		s.logger.Errorw("failed to save security event", "error", err)
		return &pb.TrackSecurityEventResponse{
			StatusCode: 500,
			Message:    "internal server error",
			Error: &pb.Error{
				Status:  500,
				Error:   "database_error",
				Message: "failed to save security event",
			},
		}, nil
	}

	s.logger.Warnw("security event tracked",
		"event_id", eventID,
		"user_id", req.GetUserId(),
		"event_type", req.GetEventType(),
		"severity", req.GetSeverity(),
		"risk_score", req.GetRiskScore(),
	)

	return &pb.TrackSecurityEventResponse{
		StatusCode: 200,
		Message:    "security event tracked successfully",
		EventId:    eventID,
	}, nil
}

// GetSecurityEvents retrieves security events
func (s *ActivityServiceServer) GetSecurityEvents(ctx context.Context, req *pb.GetSecurityEventsRequest) (*pb.GetSecurityEventsResponse, error) {
	s.logger.Debugw("GetSecurityEvents called",
		"user_id", req.GetUserId(),
		"min_severity", req.GetMinSeverity(),
		"resolved", req.GetResolved(),
	)

	// Mock response for now
	return &pb.GetSecurityEventsResponse{
		StatusCode: 200,
		Events:     []*pb.SecurityEvent{},
		TotalCount: 0,
	}, nil
}

// GetUserBehaviorAnalytics retrieves detailed user behavior analytics
func (s *ActivityServiceServer) GetUserBehaviorAnalytics(ctx context.Context, req *pb.GetUserBehaviorAnalyticsRequest) (*pb.GetUserBehaviorAnalyticsResponse, error) {
	s.logger.Debugw("GetUserBehaviorAnalytics called",
		"user_id", req.GetUserId(),
		"time_range", req.GetTimeRange(),
	)

	// Validate required fields
	if req.GetUserId() == "" {
		return &pb.GetUserBehaviorAnalyticsResponse{
			StatusCode: 400,
			Error: &pb.Error{
				Status:  400,
				Error:   "validation_error",
				Message: "user_id is required",
			},
		}, nil
	}

	// Mock response
	mockAnalytics := &pb.UserBehaviorAnalytics{
		UserId:               req.GetUserId(),
		AccountId:            req.GetAccountId(),
		TimeRange:            req.GetTimeRange(),
		TotalActivities:      250,
		UniqueActions:        35,
		SessionCount:         15,
		AvgSessionDurationMs: 450000, // 7.5 minutes
		EngagementScore:      0.92,
		RiskScore:            2,
		LastActivity:         timestamppb.Now(),
		CreatedAt:            timestamppb.Now(),
		UpdatedAt:            timestamppb.Now(),
	}

	return &pb.GetUserBehaviorAnalyticsResponse{
		StatusCode: 200,
		Analytics:  mockAnalytics,
	}, nil
}

// Placeholder implementations for remaining methods
func (s *ActivityServiceServer) GetReadingBehavior(ctx context.Context, req *pb.GetReadingBehaviorRequest) (*pb.GetReadingBehaviorResponse, error) {
	return &pb.GetReadingBehaviorResponse{StatusCode: 200, Behavior: []*pb.ReadingBehaviorAnalytics{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackRecommendationInteraction(ctx context.Context, req *pb.TrackRecommendationInteractionRequest) (*pb.TrackRecommendationInteractionResponse, error) {
	return &pb.TrackRecommendationInteractionResponse{StatusCode: 200, Message: "tracked", InteractionId: "rec_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetRecommendationAnalytics(ctx context.Context, req *pb.GetRecommendationAnalyticsRequest) (*pb.GetRecommendationAnalyticsResponse, error) {
	return &pb.GetRecommendationAnalyticsResponse{StatusCode: 200, Interactions: []*pb.RecommendationInteraction{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) GetContentAnalytics(ctx context.Context, req *pb.GetContentAnalyticsRequest) (*pb.GetContentAnalyticsResponse, error) {
	s.logger.Debugw("GetContentAnalytics called",
		"content_id", req.GetContentId(),
		"content_type", req.GetContentType(),
	)

	if req.GetContentType() == "blog" && req.GetContentId() != "" {
		analytics, err := s.db.GetBlogAnalytics(ctx, req.GetContentId(), req.GetTimeRange())
		if err != nil {
			s.logger.Errorw("failed to get blog analytics", "error", err)
			return &pb.GetContentAnalyticsResponse{
				StatusCode: 500,
				Error: &pb.Error{
					Status:  500,
					Error:   "database_error",
					Message: "failed to get blog analytics",
				},
			}, nil
		}

		engagementRate := 0.0
		if analytics.ValidViews > 0 {
			engagementRate = float64(analytics.TotalLikes) / float64(analytics.ValidViews)
		}

		summary, err := structpb.NewStruct(map[string]interface{}{
			"unique_readers":   analytics.UniqueReaders,
			"total_likes":      analytics.TotalLikes,
			"valid_views":      analytics.ValidViews,
			"bounces":          analytics.Bounces,
			"engagement_rate":  engagementRate,
			"avg_read_time_ms": analytics.AvgReadTimeMs,
			"countries":        convertMap(analytics.Countries),
			"referrers":        convertMap(analytics.Referrers),
			"platforms":        convertMap(analytics.Platforms),
			"cities":           convertMap(analytics.Cities),
			// "isps":                   convertMap(analytics.ISPs),
			"daily_activity":         convertMap(analytics.DailyActivity),
			"hourly_activity":        convertMap(analytics.HourlyActivity),
			"monthly_activity":       convertMap(analytics.MonthlyActivity),
			"read_time_distribution": convertMap(analytics.ReadTimeDistribution),
			"realtime_views":         convertMap(analytics.RealtimeViews),
		})
		if err != nil {
			s.logger.Errorw("failed to create analytics summary struct", "error", err)
			// Return partial response or error? Let's return what we have with empty summary if it fails,
			// but logging it is important.
		}

		return &pb.GetContentAnalyticsResponse{
			StatusCode:       200,
			TotalCount:       analytics.TotalReads,
			AnalyticsSummary: summary,
		}, nil
	}

	return &pb.GetContentAnalyticsResponse{StatusCode: 200, Interactions: []*pb.ContentInteraction{}, TotalCount: 0}, nil
}

func convertMap(m map[string]int64) map[string]interface{} {
	res := make(map[string]interface{})
	for k, v := range m {
		res[k] = v
	}
	return res
}

func (s *ActivityServiceServer) TrackContentInteraction(ctx context.Context, req *pb.TrackContentInteractionRequest) (*pb.TrackContentInteractionResponse, error) {
	return &pb.TrackContentInteractionResponse{StatusCode: 200, Message: "tracked", InteractionId: "content_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) TrackUserJourney(ctx context.Context, req *pb.TrackUserJourneyRequest) (*pb.TrackUserJourneyResponse, error) {
	return &pb.TrackUserJourneyResponse{StatusCode: 200, Message: "tracked", JourneyId: "journey_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetUserJourneyAnalytics(ctx context.Context, req *pb.GetUserJourneyAnalyticsRequest) (*pb.GetUserJourneyAnalyticsResponse, error) {
	return &pb.GetUserJourneyAnalyticsResponse{StatusCode: 200, Journeys: []*pb.UserJourneyEvent{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackNotificationEvent(ctx context.Context, req *pb.TrackNotificationEventRequest) (*pb.TrackNotificationEventResponse, error) {
	return &pb.TrackNotificationEventResponse{StatusCode: 200, Message: "tracked", NotificationId: "notif_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetNotificationAnalytics(ctx context.Context, req *pb.GetNotificationAnalyticsRequest) (*pb.GetNotificationAnalyticsResponse, error) {
	return &pb.GetNotificationAnalyticsResponse{StatusCode: 200, Notifications: []*pb.NotificationEvent{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackFinancialEvent(ctx context.Context, req *pb.TrackFinancialEventRequest) (*pb.TrackFinancialEventResponse, error) {
	return &pb.TrackFinancialEventResponse{StatusCode: 200, Message: "tracked", FinancialEventId: "finance_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetFinancialEvents(ctx context.Context, req *pb.GetFinancialEventsRequest) (*pb.GetFinancialEventsResponse, error) {
	return &pb.GetFinancialEventsResponse{StatusCode: 200, Events: []*pb.FinancialEvent{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackIntegrationEvent(ctx context.Context, req *pb.TrackIntegrationEventRequest) (*pb.TrackIntegrationEventResponse, error) {
	return &pb.TrackIntegrationEventResponse{StatusCode: 200, Message: "tracked", IntegrationEventId: "integration_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetIntegrationEvents(ctx context.Context, req *pb.GetIntegrationEventsRequest) (*pb.GetIntegrationEventsResponse, error) {
	return &pb.GetIntegrationEventsResponse{StatusCode: 200, Events: []*pb.IntegrationEvent{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackIncidentEvent(ctx context.Context, req *pb.TrackIncidentEventRequest) (*pb.TrackIncidentEventResponse, error) {
	return &pb.TrackIncidentEventResponse{StatusCode: 200, Message: "tracked", IncidentEventId: "incident_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetIncidentEvents(ctx context.Context, req *pb.GetIncidentEventsRequest) (*pb.GetIncidentEventsResponse, error) {
	return &pb.GetIncidentEventsResponse{StatusCode: 200, Events: []*pb.IncidentEvent{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackComplianceEvent(ctx context.Context, req *pb.TrackComplianceEventRequest) (*pb.TrackComplianceEventResponse, error) {
	return &pb.TrackComplianceEventResponse{StatusCode: 200, Message: "tracked", ComplianceEventId: "compliance_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetComplianceEvents(ctx context.Context, req *pb.GetComplianceEventsRequest) (*pb.GetComplianceEventsResponse, error) {
	return &pb.GetComplianceEventsResponse{StatusCode: 200, Events: []*pb.ComplianceEvent{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackSearchActivity(ctx context.Context, req *pb.TrackSearchActivityRequest) (*pb.TrackSearchActivityResponse, error) {
	return &pb.TrackSearchActivityResponse{StatusCode: 200, Message: "tracked", SearchActivityId: "search_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetSearchAnalytics(ctx context.Context, req *pb.GetSearchAnalyticsRequest) (*pb.GetSearchAnalyticsResponse, error) {
	return &pb.GetSearchAnalyticsResponse{StatusCode: 200, Searches: []*pb.SearchActivity{}, TotalCount: 0}, nil
}

func (s *ActivityServiceServer) TrackPerformanceEvent(ctx context.Context, req *pb.TrackPerformanceEventRequest) (*pb.TrackPerformanceEventResponse, error) {
	return &pb.TrackPerformanceEventResponse{StatusCode: 200, Message: "tracked", PerformanceEventId: "perf_" + time.Now().Format("20060102150405")}, nil
}

func (s *ActivityServiceServer) GetPerformanceAnalytics(ctx context.Context, req *pb.GetPerformanceAnalyticsRequest) (*pb.GetPerformanceAnalyticsResponse, error) {
	return &pb.GetPerformanceAnalyticsResponse{StatusCode: 200, Events: []*pb.PerformanceEvent{}, TotalCount: 0}, nil
}
func (s *ActivityServiceServer) GetTrendingBlogs(ctx context.Context, req *pb.GetTrendingBlogsRequest) (*pb.GetTrendingBlogsResponse, error) {
	// If it's a global request and we have a cache, use it
	if !homepageTrendingRequested(ctx) && req.GetAccountId() == "" && req.GetTimeRange() == "24h" {
		s.cacheMutex.RLock()
		cached := s.trendingCache
		s.cacheMutex.RUnlock()
		if cached != nil {
			return &pb.GetTrendingBlogsResponse{StatusCode: 200, Blogs: cached}, nil
		}
	}

	// Otherwise, query the DB
	trending, err := s.db.GetTrendingBlogs(ctx, req)
	if err != nil {
		s.logger.Errorw("failed to get trending blogs", "error", err)
		return &pb.GetTrendingBlogsResponse{
			StatusCode: 500,
			Error:      &pb.Error{Status: 500, Error: "database_error", Message: err.Error()},
		}, nil
	}

	return &pb.GetTrendingBlogsResponse{StatusCode: 200, Blogs: trending}, nil
}

func (s *ActivityServiceServer) GetActiveUsers(ctx context.Context, req *pb.GetActiveUsersRequest) (*pb.GetActiveUsersResponse, error) {
	// If it's a global request and we have a cache, use it
	// Cache logic disabled to support listing active users
	// if req.GetAccountId() == "" && req.GetTimeRange() == "3h" {
	// 	s.cacheMutex.RLock()
	// 	count := s.activeUsersCache
	// 	s.cacheMutex.RUnlock()
	// 	return &pb.GetActiveUsersResponse{StatusCode: 200, ActiveUsers: count}, nil
	// }

	count, userList, err := s.db.GetActiveUsers(ctx, req)
	if err != nil {
		s.logger.Errorw("failed to get active users", "error", err)
		return &pb.GetActiveUsersResponse{
			StatusCode: 500,
			Error:      &pb.Error{Status: 500, Error: "database_error", Message: err.Error()},
		}, nil
	}
	s.logger.Infow("GetActiveUsers DB Result", "count", count, "userListSize", len(userList))
	if len(userList) > 0 {
		s.logger.Infow("First user in list", "user_id", userList[0].UserId, "last_active", userList[0].LastActive)
	}

	return &pb.GetActiveUsersResponse{StatusCode: 200, ActiveUsers: count, UserList: userList}, nil
}

func (s *ActivityServiceServer) GetAccountActivities(ctx context.Context, req *pb.GetAccountActivitiesRequest) (*pb.GetAccountActivitiesResponse, error) {
	if req.GetAccountId() == "" {
		return &pb.GetAccountActivitiesResponse{
			StatusCode: 400,
			Error:      &pb.Error{Status: 400, Error: "validation_error", Message: "account_id is required"},
		}, nil
	}

	activities, total, err := s.db.GetAccountActivities(ctx, req)
	if err != nil {
		s.logger.Errorw("failed to get account activities", "error", err)
		return &pb.GetAccountActivitiesResponse{
			StatusCode: 500,
			Error:      &pb.Error{Status: 500, Error: "database_error", Message: err.Error()},
		}, nil
	}

	return &pb.GetAccountActivitiesResponse{
		StatusCode: 200,
		Activities: activities,
		TotalCount: total,
	}, nil
}

func (s *ActivityServiceServer) GetAdvancedAnalytics(ctx context.Context, req *pb.GetAdvancedAnalyticsRequest) (*pb.GetAdvancedAnalyticsResponse, error) {
	// If it's a global request and we have a cache, use it
	if req.GetAccountId() == "" && req.GetTimeRange() == "7d" {
		s.cacheMutex.RLock()
		cached := s.advancedCache
		s.cacheMutex.RUnlock()
		if cached != nil {
			return &pb.GetAdvancedAnalyticsResponse{StatusCode: 200, Analytics: cached}, nil
		}
	}

	analytics, err := s.db.GetAdvancedAnalytics(ctx, req)
	if err != nil {
		s.logger.Errorw("failed to get advanced analytics", "error", err)
		return &pb.GetAdvancedAnalyticsResponse{
			StatusCode: 500,
			Error:      &pb.Error{Status: 500, Error: "database_error", Message: err.Error()},
		}, nil
	}

	return &pb.GetAdvancedAnalyticsResponse{StatusCode: 200, Analytics: analytics}, nil
}
