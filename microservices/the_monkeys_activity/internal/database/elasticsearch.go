package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ActivityDatabase defines the interface for activity database operations
type ActivityDatabase interface {
	SaveActivity(ctx context.Context, req *pb.TrackActivityRequest) (string, error)
	GetUserActivities(ctx context.Context, req *pb.GetUserActivitiesRequest) ([]*pb.ActivityEvent, int64, error)
	SaveSecurityEvent(ctx context.Context, req *pb.TrackSecurityEventRequest) (string, error)
	GetBlogAnalytics(ctx context.Context, blogID string, timeRange string) (*BlogAnalytics, error)
	GetTrendingBlogs(ctx context.Context, req *pb.GetTrendingBlogsRequest) ([]*pb.TrendingBlog, error)
	GetActiveUsers(ctx context.Context, req *pb.GetActiveUsersRequest) (int64, []*pb.ActiveUser, error)
	GetAccountActivities(ctx context.Context, req *pb.GetAccountActivitiesRequest) ([]*pb.ActivityEvent, int64, error)
	GetAdvancedAnalytics(ctx context.Context, req *pb.GetAdvancedAnalyticsRequest) (*pb.AdvancedAnalytics, error)
	Health(ctx context.Context) error
	StartIPResolver(ctx context.Context)
	UpdateIPLocation(ctx context.Context, ip string, location *IPGeoLocation) error
	UpdateTimeSeriesConfig(config TimeSeriesConfig)                   // Configure time-series behavior
	GetIndexInfo(ctx context.Context) (map[string]interface{}, error) // Monitor index usage
}

type IPGeoLocation struct {
	Status      string  `json:"status"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"region"`
	RegionName  string  `json:"regionName"`
	City        string  `json:"city"`
	Zip         string  `json:"zip"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Timezone    string  `json:"timezone"`
	ISP         string  `json:"isp"`
	Org         string  `json:"org"`
	AS          string  `json:"as"`
	Query       string  `json:"query"`
}

type BlogAnalytics struct {
	TotalReads           int64
	UniqueReaders        int64
	TotalLikes           int64
	AvgReadTimeMs        float64
	Countries            map[string]int64
	Referrers            map[string]int64
	Platforms            map[string]int64
	Cities               map[string]int64
	ISPs                 map[string]int64
	DailyActivity        map[string]int64
	HourlyActivity       map[string]int64
	MonthlyActivity      map[string]int64
	ReadTimeDistribution map[string]int64
	RealtimeViews        map[string]int64
	ValidViews           int64
	Bounces              int64
}

const (
	// Elasticsearch indices for different types of events
	ActivityEventIndex      = "activity-events"
	SecurityEventIndex      = "security-events"
	UserBehaviorIndex       = "user-behavior"
	ContentInteractionIndex = "content-interactions"
	RecommendationIndex     = "recommendation-interactions"
	NotificationIndex       = "notification-events"
	FinancialIndex          = "financial-events"
	IntegrationIndex        = "integration-events"
	IncidentIndex           = "incident-events"
	ComplianceIndex         = "compliance-events"
	SearchActivityIndex     = "search-activities"
	PerformanceIndex        = "performance-events"
	UserJourneyIndex        = "user-journey-events"
	ReadingBehaviorIndex    = "reading-behavior"

	// Time-series index patterns for high-volume data
	ActivityTimeSeriesPattern       = "activity-events-%s"             // activity-events-2025-10
	RecommendationTimeSeriesPattern = "recommendation-interactions-%s" // recommendation-interactions-2025-10
	UserBehaviorTimeSeriesPattern   = "user-behavior-%s"               // user-behavior-2025-10
)

// ActivityDB handles Elasticsearch operations for activity tracking
type ActivityDB struct {
	client           *elasticsearch.Client
	logger           *zap.SugaredLogger
	config           *config.Config
	timeSeriesConfig TimeSeriesConfig
}

// TimeSeriesConfig holds configuration for time-series indices
type TimeSeriesConfig struct {
	UseTimeSeries   bool
	IndexRotation   string // "daily", "weekly", "monthly"
	VolumeThreshold int64  // documents per day threshold to trigger time-series
}

const (
	// High-volume threshold: >1M docs/day triggers time-series indexing
	HighVolumeThreshold = 1000000
	// Default rotation for time-series indices
	DefaultRotation = "monthly"
)

// NewActivityDB creates a new ActivityDB instance
func NewActivityDB(cfg *config.Config, logger *zap.SugaredLogger) (*ActivityDB, error) {
	// Elasticsearch configuration
	esConfig := elasticsearch.Config{
		Addresses: []string{
			cfg.Opensearch.Host, // Host already includes protocol
		},
		Username: cfg.Opensearch.Username,
		Password: cfg.Opensearch.Password,
	}

	client, err := elasticsearch.NewClient(esConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Elasticsearch client: %w", err)
	}

	db := &ActivityDB{
		client: client,
		logger: logger,
		config: cfg,
		timeSeriesConfig: TimeSeriesConfig{
			UseTimeSeries:   true,            // Enable time-series for high-volume data
			IndexRotation:   DefaultRotation, // Monthly rotation initially
			VolumeThreshold: HighVolumeThreshold,
		},
	}

	// Initialize indices
	if err := db.initializeIndices(); err != nil {
		return nil, fmt.Errorf("failed to initialize indices: %w", err)
	}

	return db, nil
}

// initializeIndices creates the necessary Elasticsearch indices with proper mappings
func (db *ActivityDB) initializeIndices() error {
	indices := []string{
		ActivityEventIndex,
		SecurityEventIndex,
		UserBehaviorIndex,
		ContentInteractionIndex,
		RecommendationIndex,
		NotificationIndex,
		FinancialIndex,
		IntegrationIndex,
		IncidentIndex,
		ComplianceIndex,
		SearchActivityIndex,
		PerformanceIndex,
		UserJourneyIndex,
		ReadingBehaviorIndex,
	}

	for _, index := range indices {
		if err := db.createIndexIfNotExists(index); err != nil {
			return fmt.Errorf("failed to create index %s: %w", index, err)
		}
	}

	return nil
}

// createIndexIfNotExists creates an index if it doesn't exist
func (db *ActivityDB) createIndexIfNotExists(indexName string) error {
	// Check if index exists
	req := esapi.IndicesExistsRequest{
		Index: []string{indexName},
	}

	res, err := req.Do(context.Background(), db.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// If index exists (200), return
	if res.StatusCode == 200 {
		db.logger.Debugw("Index already exists", "index", indexName)
		return nil
	}

	// Create index with time-series optimized mapping
	mapping := db.getIndexMapping(indexName)

	createReq := esapi.IndicesCreateRequest{
		Index: indexName,
		Body:  strings.NewReader(mapping),
	}

	createRes, err := createReq.Do(context.Background(), db.client)
	if err != nil {
		return err
	}
	defer createRes.Body.Close()

	if createRes.IsError() {
		return fmt.Errorf("failed to create index %s: %s", indexName, createRes.String())
	}

	db.logger.Infow("Created Elasticsearch index", "index", indexName)
	return nil
}

// getIndexMapping returns the mapping configuration for different indices
func (db *ActivityDB) getIndexMapping(indexName string) string {
	// Base mapping for time-series data
	baseMapping := `{
		"settings": {
			"number_of_shards": 1,
			"number_of_replicas": 1,
			"index": {
				"refresh_interval": "5s"
			}
		},
		"mappings": {
			"properties": {
				"@timestamp": {
					"type": "date",
					"format": "strict_date_optional_time||epoch_millis"
				},
				"user_id": {
					"type": "keyword"
				},
				"account_id": {
					"type": "keyword"
				},
				"session_id": {
					"type": "keyword"
				},
				"client_ip": {
					"type": "ip"
				},
				"country": {
					"type": "keyword"
				},
				"user_agent": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						}
					}
				}
			}
		}
	}`

	// You can customize mappings per index type if needed
	switch indexName {
	case ActivityEventIndex:
		return `{
			"settings": {
				"number_of_shards": 2,
				"number_of_replicas": 1,
				"index": {
					"refresh_interval": "1s"
				}
			},
			"mappings": {
				"properties": {
					"@timestamp": {"type": "date"},
					"user_id": {"type": "keyword"},
					"account_id": {"type": "keyword"},
					"session_id": {"type": "keyword"},
					"category": {"type": "keyword"},
					"action": {"type": "keyword"},
					"resource": {"type": "keyword"},
					"resource_id": {"type": "keyword"},
					"client_ip": {"type": "ip"},
					"user_agent": {
						"type": "text",
						"fields": {
							"keyword": {"type": "keyword", "ignore_above": 256}
						}
					},
					"country": {"type": "keyword"},
					"platform": {"type": "keyword"},
					"referrer": {
						"type": "text",
						"fields": {
							"keyword": {"type": "keyword", "ignore_above": 256}
						}
					},
					"client_info": {
						"type": "object",
						"properties": {
							"ip_address": {"type": "keyword"},
							"user_agent": {"type": "keyword"},
							"visitor_id": {"type": "keyword"},
							"x_session_id": {"type": "keyword"},
							"is_bot": {"type": "boolean"},
							"country": {"type": "keyword"},
							"city": {"type": "keyword"},
							"browser": {"type": "keyword"},
							"os": {"type": "keyword"},
							"utm_source": {"type": "keyword"},
							"utm_medium": {"type": "keyword"},
							"utm_campaign": {"type": "keyword"}
						}
					},
					"success": {"type": "boolean"},
					"duration_ms": {"type": "long"},
					"metadata": {"type": "object", "enabled": false}
				}
			}
		}`
	case SecurityEventIndex:
		return `{
			"settings": {
				"number_of_shards": 1,
				"number_of_replicas": 1
			},
			"mappings": {
				"properties": {
					"@timestamp": {"type": "date"},
					"user_id": {"type": "keyword"},
					"account_id": {"type": "keyword"},
					"event_type": {"type": "keyword"},
					"severity": {"type": "keyword"},
					"description": {"type": "text"},
					"risk_score": {"type": "integer"},
					"resolved": {"type": "boolean"},
					"resolved_by": {"type": "keyword"},
					"resolved_at": {"type": "date"},
					"context": {"type": "object"}
				}
			}
		}`
	case RecommendationIndex:
		return `{
			"settings": {
				"number_of_shards": 2,
				"number_of_replicas": 1,
				"index": {
					"refresh_interval": "5s"
				}
			},
			"mappings": {
				"properties": {
					"@timestamp": {"type": "date"},
					"user_id": {"type": "keyword"},
					"account_id": {"type": "keyword"},
					"session_id": {"type": "keyword"},
					"interaction_type": {"type": "keyword"},
					"content_id": {"type": "keyword"},
					"content_type": {"type": "keyword"},
					"content_categories": {"type": "keyword"},
					"content_tags": {"type": "keyword"},
					"engagement_score": {"type": "float"},
					"interaction_duration": {"type": "long"},
					"rating": {"type": "float"},
					"explicit_feedback": {"type": "keyword"},
					"implicit_signals": {
						"type": "object",
						"properties": {
							"scroll_depth": {"type": "float"},
							"clicks": {"type": "integer"},
							"time_spent": {"type": "long"},
							"bounce_rate": {"type": "boolean"}
						}
					},
					"user_context": {
						"type": "object",
						"properties": {
							"device_type": {"type": "keyword"},
							"location": {"type": "geo_point"},
							"time_of_day": {"type": "keyword"},
							"day_of_week": {"type": "keyword"}
						}
					},
					"content_features": {
						"type": "object",
						"properties": {
							"author_id": {"type": "keyword"},
							"publish_date": {"type": "date"},
							"content_length": {"type": "integer"},
							"reading_level": {"type": "keyword"},
							"topic_vector": {"type": "dense_vector", "dims": 128}
						}
					},
					"recommendation_context": {
						"type": "object",
						"properties": {
							"source": {"type": "keyword"},
							"algorithm": {"type": "keyword"},
							"confidence_score": {"type": "float"},
							"ab_test_group": {"type": "keyword"}
						}
					}
				}
			}
		}`
	default:
		return baseMapping
	}
}

// SaveActivity saves an activity event to Elasticsearch with intelligent index selection
func (db *ActivityDB) SaveActivity(ctx context.Context, req *pb.TrackActivityRequest) (string, error) {
	// Determine if this should go to time-series or regular index
	if db.timeSeriesConfig.UseTimeSeries && db.shouldUseTimeSeries(req) {
		return db.saveToTimeSeries(ctx, req)
	}

	// Use regular index for low-volume or critical data
	return db.saveToRegularIndex(ctx, req)
}

// shouldUseTimeSeries determines if the activity should use time-series indexing
func (db *ActivityDB) shouldUseTimeSeries(req *pb.TrackActivityRequest) bool {
	// High-volume activity types that benefit from time-series
	highVolumeActions := map[string]bool{
		"view":       true,
		"scroll":     true,
		"click":      true,
		"search":     true,
		"impression": true,
		"session":    true,
	}

	// Critical activities that should stay in regular index for immediate access
	criticalActions := map[string]bool{
		"register": true,
		"login":    true,
		"purchase": true,
		"payment":  true,
		"error":    true,
		"security": true,
	}

	action := req.GetAction()

	// Critical actions always go to regular index (comprehensive data storage)
	if criticalActions[action] {
		return false
	}

	// High-volume actions go to time-series (optimized storage)
	if highVolumeActions[action] {
		return true
	}

	// Default to regular index for unknown actions
	return false
}

// saveToRegularIndex saves to the standard activity-events index
func (db *ActivityDB) saveToRegularIndex(ctx context.Context, req *pb.TrackActivityRequest) (string, error) {
	activityID := fmt.Sprintf("activity_%d_%s", time.Now().UnixNano(), req.GetUserId())
	// Use deterministic ID for duration tracking to implement UPSERT (update existing instead of create)
	if req.GetAction() == "read_duration" && req.GetSessionId() != "" && req.GetResourceId() != "" {
		activityID = fmt.Sprintf("activity_duration_%s_%s", req.GetSessionId(), req.GetResourceId())
		db.logger.Debugf("UPSERT: Using deterministic ID for duration tracking: %s", activityID)
	}

	// Extract client information
	clientInfo := req.GetClientInfo()

	// Convert protobuf to document (full structure for regular index with comprehensive client info)
	doc := map[string]interface{}{
		"@timestamp":  time.Now().Format(time.RFC3339),
		"id":          activityID,
		"user_id":     req.GetUserId(),
		"account_id":  req.GetAccountId(),
		"session_id":  req.GetSessionId(),
		"category":    req.GetCategory().String(),
		"action":      req.GetAction(),
		"resource":    req.GetResource(),
		"resource_id": req.GetResourceId(),
		"success":     req.GetSuccess(),
		"duration_ms": req.GetDurationMs(),
		"metadata":    req.GetMetadata(),
	}

	// Add comprehensive client information
	if clientInfo != nil {
		doc["client_info"] = map[string]interface{}{
			// Network Information
			"ip_address":        clientInfo.GetIpAddress(),
			"user_agent":        clientInfo.GetUserAgent(),
			"x_forwarded_for":   clientInfo.GetXForwardedFor(),
			"x_real_ip":         clientInfo.GetXRealIp(),
			"x_forwarded_proto": clientInfo.GetXForwardedProto(),
			"x_forwarded_host":  clientInfo.GetXForwardedHost(),

			// Browser Information
			"accept_language": clientInfo.GetAcceptLanguage(),
			"accept_encoding": clientInfo.GetAcceptEncoding(),
			"accept":          clientInfo.GetAccept(),
			"cache_control":   clientInfo.GetCacheControl(),
			"connection":      clientInfo.GetConnection(),
			"dnt":             clientInfo.GetDnt(),

			// Security Headers
			"sec_ch_ua":          clientInfo.GetSecChUa(),
			"sec_ch_ua_mobile":   clientInfo.GetSecChUaMobile(),
			"sec_ch_ua_platform": clientInfo.GetSecChUaPlatform(),
			"sec_fetch_site":     clientInfo.GetSecFetchSite(),
			"sec_fetch_mode":     clientInfo.GetSecFetchMode(),
			"sec_fetch_user":     clientInfo.GetSecFetchUser(),
			"sec_fetch_dest":     clientInfo.GetSecFetchDest(),

			// Referrer Information
			"referer": clientInfo.GetReferer(),
			"origin":  clientInfo.GetOrigin(),

			// Cloudflare Headers
			"cf_connecting_ip": clientInfo.GetCfConnectingIp(),
			"cf_ipcountry":     clientInfo.GetCfIpcountry(),
			"cf_ray":           clientInfo.GetCfRay(),
			"cf_visitor":       clientInfo.GetCfVisitor(),

			// UTM Parameters
			"utm_source":   clientInfo.GetUtmSource(),
			"utm_medium":   clientInfo.GetUtmMedium(),
			"utm_campaign": clientInfo.GetUtmCampaign(),
			"utm_term":     clientInfo.GetUtmTerm(),
			"utm_content":  clientInfo.GetUtmContent(),

			// Custom Tracking Headers
			"x_client_id":      clientInfo.GetXClientId(),
			"x_session_id":     clientInfo.GetXSessionId(),
			"x_request_id":     clientInfo.GetXRequestId(),
			"x_correlation_id": clientInfo.GetXCorrelationId(),
			"visitor_id":       clientInfo.GetVisitorId(),

			// Processed Information
			"platform":        clientInfo.GetPlatform().String(),
			"device_type":     clientInfo.GetDeviceType().String(),
			"browser":         clientInfo.GetBrowser(),
			"browser_version": clientInfo.GetBrowserVersion(),
			"os":              clientInfo.GetOs(),
			"os_version":      clientInfo.GetOsVersion(),
			"country":         clientInfo.GetCountry(),
			"city":            clientInfo.GetCity(),
			"timezone":        clientInfo.GetTimezone(),
			"languages":       clientInfo.GetLanguages(),
			"is_mobile":       clientInfo.GetIsMobile(),
			"is_tablet":       clientInfo.GetIsTablet(),
			"is_bot":          clientInfo.GetIsBot(),
			"trust_score":     clientInfo.GetTrustScore(),

			// Behavioral Indicators
			"has_ad_blocker":     clientInfo.GetHasAdBlocker(),
			"supports_webgl":     clientInfo.GetSupportsWebgl(),
			"supports_touch":     clientInfo.GetSupportsTouch(),
			"screen_width":       clientInfo.GetScreenWidth(),
			"screen_height":      clientInfo.GetScreenHeight(),
			"color_depth":        clientInfo.GetColorDepth(),
			"browser_engine":     clientInfo.GetBrowserEngine(),
			"timezone_offset":    clientInfo.TimezoneOffset,
			"javascript_enabled": clientInfo.JavascriptEnabled,
			"request_count":      clientInfo.RequestCount,
		}

		// Add backward compatibility fields for easy querying
		doc["client_ip"] = clientInfo.GetIpAddress()
		doc["user_agent"] = clientInfo.GetUserAgent()
		doc["country"] = clientInfo.GetCountry()
		doc["platform"] = clientInfo.GetPlatform().String()
		doc["referrer"] = clientInfo.GetReferer()
	} else {
		// Fallback for missing client info
		doc["client_info"] = nil
		doc["client_ip"] = "unknown"
		doc["user_agent"] = "unknown"
		doc["country"] = "unknown"
		doc["platform"] = "unknown"
		doc["referrer"] = ""
	}

	docBytes, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal document: %w", err)
	}

	// Index the document with immediate refresh for critical data
	indexReq := esapi.IndexRequest{
		// Index ex: activity_events_2025-12-22
		Index: fmt.Sprintf(
			"%s_%s",
			ActivityEventIndex,
			time.Now().Format(time.DateOnly),
		),
		DocumentID: activityID,
		Body:       bytes.NewReader(docBytes),
		Refresh:    "true", // Immediate refresh for critical data
	}

	res, err := indexReq.Do(ctx, db.client)
	if err != nil {
		return "", fmt.Errorf("failed to index document: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return "", fmt.Errorf("indexing failed: %s", res.String())
	}

	db.logger.Debugw("Activity saved to regular index",
		"activity_id", activityID,
		"user_id", req.GetUserId(),
		"category", req.GetCategory(),
		"action", req.GetAction(),
		"index", ActivityEventIndex,
	)

	return activityID, nil
}

// saveToTimeSeries saves to time-series optimized index with monthly rotation
func (db *ActivityDB) saveToTimeSeries(ctx context.Context, req *pb.TrackActivityRequest) (string, error) {
	// Generate time-series index name with monthly rotation
	indexName := db.getTimeSeriesIndexName(ActivityTimeSeriesPattern, db.timeSeriesConfig.IndexRotation)

	// Ensure the time-series index exists
	if err := db.createTimeSeriesIndex(ctx, indexName, "activity"); err != nil {
		return "", fmt.Errorf("failed to create time-series index: %w", err)
	}

	activityID := fmt.Sprintf("activity_%d_%s", time.Now().UnixNano(), req.GetUserId())
	// Use deterministic ID for duration tracking to implement UPSERT (update existing instead of create)
	if req.GetAction() == "read_duration" && req.GetSessionId() != "" && req.GetResourceId() != "" {
		activityID = fmt.Sprintf("activity_duration_%s_%s", req.GetSessionId(), req.GetResourceId())
		db.logger.Debugf("UPSERT (Time-Series): Using deterministic ID for duration tracking: %s", activityID)
	}

	// Extract client information
	clientInfo := req.GetClientInfo()

	// Optimized document structure for time-series (performance focused, essential fields only)
	doc := map[string]interface{}{
		"@timestamp":  time.Now().Format(time.RFC3339),
		"user_id":     req.GetUserId(),
		"session_id":  req.GetSessionId(),
		"action":      req.GetAction(),
		"resource":    req.GetResource(),
		"resource_id": req.GetResourceId(),
		"success":     req.GetSuccess(),
		"duration_ms": req.GetDurationMs(),
		"category":    req.GetCategory().String(),
	}

	// Add essential client info for time-series (lightweight for performance)
	if clientInfo != nil {
		doc["client_ip"] = clientInfo.GetIpAddress()
		doc["platform"] = clientInfo.GetPlatform().String()
		doc["country"] = clientInfo.GetCountry()
		doc["is_bot"] = clientInfo.GetIsBot()
		doc["utm_source"] = clientInfo.GetUtmSource()
		doc["utm_campaign"] = clientInfo.GetUtmCampaign()
	} else {
		doc["client_ip"] = "unknown"
		doc["platform"] = "unknown"
		doc["country"] = "unknown"
		doc["is_bot"] = false
		doc["utm_source"] = ""
		doc["utm_campaign"] = ""
	}

	docBytes, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal document: %w", err)
	}

	// Index to time-series index (no immediate refresh for performance)
	indexReq := esapi.IndexRequest{
		Index:      indexName,
		DocumentID: activityID,
		Body:       bytes.NewReader(docBytes),
		Refresh:    "false", // Batch refresh for performance
	}

	res, err := indexReq.Do(ctx, db.client)
	if err != nil {
		return "", fmt.Errorf("failed to index to time-series: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return "", fmt.Errorf("time-series indexing failed: %s", res.String())
	}

	db.logger.Debugw("Activity saved to time-series index",
		"activity_id", activityID,
		"index", indexName,
		"user_id", req.GetUserId(),
		"action", req.GetAction(),
	)

	return activityID, nil
}

// GetUserActivities retrieves user activities from Elasticsearch
func (db *ActivityDB) GetUserActivities(ctx context.Context, req *pb.GetUserActivitiesRequest) ([]*pb.ActivityEvent, int64, error) {
	// Build Elasticsearch query
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"user_id": req.GetUserId(),
						},
					},
				},
			},
		},
		"sort": []map[string]interface{}{
			{
				"@timestamp": map[string]interface{}{
					"order": "desc",
				},
			},
		},
		"size": req.GetLimit(),
		"from": req.GetOffset(),
	}

	// Add optional filters
	if req.GetAccountId() != "" {
		mustFilters := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		mustFilters = append(mustFilters, map[string]interface{}{
			"term": map[string]interface{}{
				"account_id": req.GetAccountId(),
			},
		})
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = mustFilters
	}

	if req.GetCategory() != pb.ActivityCategory_CATEGORY_UNSPECIFIED {
		mustFilters := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		mustFilters = append(mustFilters, map[string]interface{}{
			"term": map[string]interface{}{
				"category": req.GetCategory().String(),
			},
		})
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = mustFilters
	}

	// Add time range filter if provided
	if req.GetStartTime() != nil || req.GetEndTime() != nil {
		rangeFilter := map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": map[string]interface{}{},
			},
		}

		if req.GetStartTime() != nil {
			rangeFilter["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})["gte"] = req.GetStartTime().AsTime().Format(time.RFC3339)
		}

		if req.GetEndTime() != nil {
			rangeFilter["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})["lte"] = req.GetEndTime().AsTime().Format(time.RFC3339)
		}

		mustFilters := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		mustFilters = append(mustFilters, rangeFilter)
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = mustFilters
	}

	queryBytes, err := json.Marshal(query)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Execute search
	searchReq := esapi.SearchRequest{
		Index: []string{ActivityEventIndex},
		Body:  bytes.NewReader(queryBytes),
	}

	res, err := searchReq.Do(ctx, db.client)
	if err != nil {
		return nil, 0, fmt.Errorf("search failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, 0, fmt.Errorf("search error: %s", res.String())
	}

	// Parse response
	var searchResponse struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source map[string]interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&searchResponse); err != nil {
		return nil, 0, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to protobuf
	activities := make([]*pb.ActivityEvent, 0, len(searchResponse.Hits.Hits))
	for _, hit := range searchResponse.Hits.Hits {
		activity := db.convertToActivityEvent(hit.Source)
		activities = append(activities, activity)
	}

	return activities, searchResponse.Hits.Total.Value, nil
}

// convertToActivityEvent converts Elasticsearch document to protobuf ActivityEvent
func (db *ActivityDB) convertToActivityEvent(doc map[string]interface{}) *pb.ActivityEvent {
	activity := &pb.ActivityEvent{}

	if id, ok := doc["id"].(string); ok {
		activity.Id = id
	}
	if userID, ok := doc["user_id"].(string); ok {
		activity.UserId = userID
	}
	if accountID, ok := doc["account_id"].(string); ok {
		activity.AccountId = accountID
	}
	if sessionID, ok := doc["session_id"].(string); ok {
		activity.SessionId = sessionID
	}
	if category, ok := doc["category"].(string); ok {
		// Convert string back to enum
		activity.Category = db.stringToActivityCategory(category)
	}
	if action, ok := doc["action"].(string); ok {
		activity.Action = action
	}
	if resource, ok := doc["resource"].(string); ok {
		activity.Resource = resource
	}
	if resourceID, ok := doc["resource_id"].(string); ok {
		activity.ResourceId = resourceID
	}
	// Convert client_info from document
	activity.ClientInfo = &pb.ClientInfo{}

	// Check if we have the comprehensive client_info object
	if clientInfoObj, ok := doc["client_info"].(map[string]interface{}); ok && clientInfoObj != nil {
		// Extract comprehensive client information
		if ipAddress, ok := clientInfoObj["ip_address"].(string); ok {
			activity.ClientInfo.IpAddress = ipAddress
		}
		if userAgent, ok := clientInfoObj["user_agent"].(string); ok {
			activity.ClientInfo.UserAgent = userAgent
		}
		if country, ok := clientInfoObj["country"].(string); ok {
			activity.ClientInfo.Country = country
		}
		if platform, ok := clientInfoObj["platform"].(string); ok {
			activity.ClientInfo.Platform = db.stringToPlatform(platform)
		}
		if referer, ok := clientInfoObj["referer"].(string); ok {
			activity.ClientInfo.Referer = referer
		}
		if utmSource, ok := clientInfoObj["utm_source"].(string); ok {
			activity.ClientInfo.UtmSource = utmSource
		}
		if utmCampaign, ok := clientInfoObj["utm_campaign"].(string); ok {
			activity.ClientInfo.UtmCampaign = utmCampaign
		}
		if browser, ok := clientInfoObj["browser"].(string); ok {
			activity.ClientInfo.Browser = browser
		}
		if isBot, ok := clientInfoObj["is_bot"].(bool); ok {
			activity.ClientInfo.IsBot = isBot
		}
		if trustScore, ok := clientInfoObj["trust_score"].(float64); ok {
			activity.ClientInfo.TrustScore = trustScore
		}
		// Add more fields as needed...
	} else {
		// Fallback to backward compatibility fields
		if clientIP, ok := doc["client_ip"].(string); ok {
			activity.ClientInfo.IpAddress = clientIP
		}
		if userAgent, ok := doc["user_agent"].(string); ok {
			activity.ClientInfo.UserAgent = userAgent
		}
		if country, ok := doc["country"].(string); ok {
			activity.ClientInfo.Country = country
		}
		if platform, ok := doc["platform"].(string); ok {
			activity.ClientInfo.Platform = db.stringToPlatform(platform)
		}
		if referrer, ok := doc["referrer"].(string); ok {
			activity.ClientInfo.Referer = referrer
		}
	}
	if success, ok := doc["success"].(bool); ok {
		activity.Success = success
	}
	if duration, ok := doc["duration_ms"].(float64); ok {
		activity.DurationMs = int64(duration)
	}
	if timestamp, ok := doc["@timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339, timestamp); err == nil {
			activity.Timestamp = timestamppb.New(t)
			activity.CreatedAt = timestamppb.New(t)
		}
	}

	return activity
}

// SaveSecurityEvent saves a security event to Elasticsearch
func (db *ActivityDB) SaveSecurityEvent(ctx context.Context, req *pb.TrackSecurityEventRequest) (string, error) {
	eventID := fmt.Sprintf("security_%d_%s", time.Now().UnixNano(), req.GetUserId())

	doc := map[string]interface{}{
		"@timestamp":  time.Now().Format(time.RFC3339),
		"id":          eventID,
		"user_id":     req.GetUserId(),
		"account_id":  req.GetAccountId(),
		"event_type":  req.GetEventType(),
		"severity":    req.GetSeverity().String(),
		"description": req.GetDescription(),
		"risk_score":  req.GetRiskScore(),
		"resolved":    false,
		"context":     req.GetContext(),
	}

	docBytes, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal security event: %w", err)
	}

	indexReq := esapi.IndexRequest{
		Index:      SecurityEventIndex,
		DocumentID: eventID,
		Body:       bytes.NewReader(docBytes),
		Refresh:    "true",
	}

	res, err := indexReq.Do(ctx, db.client)
	if err != nil {
		return "", fmt.Errorf("failed to index security event: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return "", fmt.Errorf("security event indexing failed: %s", res.String())
	}

	db.logger.Warnw("Security event saved to Elasticsearch",
		"event_id", eventID,
		"user_id", req.GetUserId(),
		"event_type", req.GetEventType(),
		"severity", req.GetSeverity(),
	)

	return eventID, nil
}

// Health checks Elasticsearch connectivity
func (db *ActivityDB) Health(ctx context.Context) error {
	res, err := db.client.Info()
	if err != nil {
		return fmt.Errorf("elasticsearch health check failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch is not healthy: %s", res.String())
	}

	return nil
}

// Helper method to convert category string back to enum
func (db *ActivityDB) stringToActivityCategory(categoryStr string) pb.ActivityCategory {
	switch categoryStr {
	case "CATEGORY_AUTHENTICATION":
		return pb.ActivityCategory_CATEGORY_AUTHENTICATION
	case "CATEGORY_AUTHORIZATION":
		return pb.ActivityCategory_CATEGORY_AUTHORIZATION
	case "CATEGORY_CONTENT":
		return pb.ActivityCategory_CATEGORY_CONTENT
	case "CATEGORY_SOCIAL":
		return pb.ActivityCategory_CATEGORY_SOCIAL
	case "CATEGORY_SEARCH":
		return pb.ActivityCategory_CATEGORY_SEARCH
	case "CATEGORY_NAVIGATION":
		return pb.ActivityCategory_CATEGORY_NAVIGATION
	case "CATEGORY_RECOMMENDATION":
		return pb.ActivityCategory_CATEGORY_RECOMMENDATION
	case "CATEGORY_ANALYTICS":
		return pb.ActivityCategory_CATEGORY_ANALYTICS
	case "CATEGORY_SECURITY":
		return pb.ActivityCategory_CATEGORY_SECURITY
	case "CATEGORY_SYSTEM":
		return pb.ActivityCategory_CATEGORY_SYSTEM
	case "CATEGORY_NOTIFICATION":
		return pb.ActivityCategory_CATEGORY_NOTIFICATION
	case "CATEGORY_COMPLIANCE":
		return pb.ActivityCategory_CATEGORY_COMPLIANCE
	case "CATEGORY_FINANCIAL":
		return pb.ActivityCategory_CATEGORY_FINANCIAL
	case "CATEGORY_INTEGRATION":
		return pb.ActivityCategory_CATEGORY_INTEGRATION
	case "CATEGORY_INCIDENT":
		return pb.ActivityCategory_CATEGORY_INCIDENT
	default:
		return pb.ActivityCategory_CATEGORY_UNSPECIFIED
	}
}

// Helper method to convert platform string back to enum
func (db *ActivityDB) stringToPlatform(platformStr string) pb.Platform {
	switch platformStr {
	case "PLATFORM_WEB":
		return pb.Platform_PLATFORM_WEB
	case "PLATFORM_MOBILE":
		return pb.Platform_PLATFORM_MOBILE
	case "PLATFORM_TABLET":
		return pb.Platform_PLATFORM_TABLET
	case "PLATFORM_API":
		return pb.Platform_PLATFORM_API
	case "PLATFORM_DESKTOP":
		return pb.Platform_PLATFORM_DESKTOP
	default:
		return pb.Platform_PLATFORM_UNSPECIFIED
	}
}

// Time-series index management functions

// getTimeSeriesIndexName generates time-series index names based on current date
func (db *ActivityDB) getTimeSeriesIndexName(basePattern string, rotation string) string {
	now := time.Now()
	var suffix string

	switch rotation {
	case "daily":
		suffix = now.Format("2006-01-02")
	case "weekly":
		year, week := now.ISOWeek()
		suffix = fmt.Sprintf("%d-w%02d", year, week)
	case "monthly":
		suffix = now.Format("2006-01")
	default:
		suffix = now.Format("2006-01") // default to monthly
	}

	return fmt.Sprintf(basePattern, suffix)
}

// indexExists checks if an Elasticsearch index exists
func (db *ActivityDB) indexExists(ctx context.Context, indexName string) (bool, error) {
	req := esapi.IndicesExistsRequest{
		Index: []string{indexName},
	}

	res, err := req.Do(ctx, db.client)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()

	return res.StatusCode == 200, nil
}

// createTimeSeriesIndex creates a time-series index with proper settings
func (db *ActivityDB) createTimeSeriesIndex(ctx context.Context, indexName string, mappingType string) error {
	// Check if index already exists
	exists, err := db.indexExists(ctx, indexName)
	if err != nil {
		return fmt.Errorf("failed to check if index exists: %w", err)
	}

	if exists {
		db.logger.Debugw("Time-series index already exists", "index", indexName)
		return nil
	}

	// Create index with time-series optimized settings
	mapping := db.getTimeSeriesMapping(mappingType)

	createReq := esapi.IndicesCreateRequest{
		Index: indexName,
		Body:  strings.NewReader(mapping),
	}

	res, err := createReq.Do(ctx, db.client)
	if err != nil {
		return fmt.Errorf("failed to create time-series index: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to create time-series index: %s", res.String())
	}

	db.logger.Infow("Created time-series index", "index", indexName)
	return nil
}

// getTimeSeriesMapping returns optimized mappings for time-series data
func (db *ActivityDB) getTimeSeriesMapping(mappingType string) string {
	switch mappingType {
	case "activity":
		return `{
			"settings": {
				"number_of_shards": 1,
				"number_of_replicas": 0,
				"index": {
					"refresh_interval": "30s",
					"number_of_routing_shards": 30,
					"sort.field": "@timestamp",
					"sort.order": "desc"
				}
			},
			"mappings": {
				"properties": {
					"@timestamp": {"type": "date"},
					"user_id": {"type": "keyword"},
					"session_id": {"type": "keyword"},
					"action": {"type": "keyword"},
					"resource": {"type": "keyword"},
					"resource_id": {"type": "keyword"},
					"success": {"type": "boolean"},
					"duration_ms": {"type": "long"},
					"platform": {"type": "keyword"},
					"client_ip": {"type": "ip"},
					"client_info": {
						"type": "object",
						"properties": {
							"ip_address": {"type": "keyword"},
							"user_agent": {"type": "keyword"},
							"visitor_id": {"type": "keyword"},
							"x_session_id": {"type": "keyword"},
							"is_bot": {"type": "boolean"},
							"country": {"type": "keyword"},
							"city": {"type": "keyword"},
							"utm_source": {"type": "keyword"}
						}
					}
				}
			}
		}`
	case "recommendation":
		return `{
			"settings": {
				"number_of_shards": 1,
				"number_of_replicas": 0,
				"index": {
					"refresh_interval": "30s",
					"number_of_routing_shards": 30,
					"sort.field": "@timestamp",
					"sort.order": "desc"
				}
			},
			"mappings": {
				"properties": {
					"@timestamp": {"type": "date"},
					"user_id": {"type": "keyword"},
					"content_id": {"type": "keyword"},
					"interaction_type": {"type": "keyword"},
					"engagement_score": {"type": "float"},
					"interaction_duration": {"type": "long"},
					"content_categories": {"type": "keyword"},
					"implicit_signals": {"type": "object"}
				}
			}
		}`
	default:
		return `{
			"settings": {
				"number_of_shards": 1,
				"number_of_replicas": 0,
				"index": {
					"refresh_interval": "30s",
					"sort.field": "@timestamp",
					"sort.order": "desc"
				}
			},
			"mappings": {
				"properties": {
					"@timestamp": {"type": "date"},
					"user_id": {"type": "keyword"}
				}
			}
		}`
	}
}

// SaveActivityToTimeSeries saves activity to time-series optimized index
func (db *ActivityDB) SaveActivityToTimeSeries(ctx context.Context, req *pb.TrackActivityRequest, rotation string) (string, error) {
	indexName := db.getTimeSeriesIndexName(ActivityTimeSeriesPattern, rotation)

	// Ensure the time-series index exists
	if err := db.createTimeSeriesIndex(ctx, indexName, "activity"); err != nil {
		return "", fmt.Errorf("failed to create time-series index: %w", err)
	}

	activityID := fmt.Sprintf("activity_%d_%s", time.Now().UnixNano(), req.GetUserId())

	// Extract client information
	clientInfo := req.GetClientInfo()

	// Simplified document structure for time-series (optimized for performance)
	doc := map[string]interface{}{
		"@timestamp":  time.Now().Format(time.RFC3339),
		"user_id":     req.GetUserId(),
		"session_id":  req.GetSessionId(),
		"action":      req.GetAction(),
		"resource":    req.GetResource(),
		"resource_id": req.GetResourceId(),
		"success":     req.GetSuccess(),
		"duration_ms": req.GetDurationMs(),
	}

	// Add essential client info for time-series (minimal set for performance)
	if clientInfo != nil {
		doc["platform"] = clientInfo.GetPlatform().String()
		doc["client_ip"] = clientInfo.GetIpAddress()
		doc["country"] = clientInfo.GetCountry()
		doc["is_bot"] = clientInfo.GetIsBot()
	} else {
		doc["platform"] = "unknown"
		doc["client_ip"] = "unknown"
		doc["country"] = "unknown"
		doc["is_bot"] = false
	}

	docBytes, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal document: %w", err)
	}

	// Index to time-series index
	indexReq := esapi.IndexRequest{
		Index:      indexName,
		DocumentID: activityID,
		Body:       bytes.NewReader(docBytes),
		Refresh:    "false", // Don't refresh immediately for performance
	}

	res, err := indexReq.Do(ctx, db.client)
	if err != nil {
		return "", fmt.Errorf("failed to index to time-series: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return "", fmt.Errorf("time-series indexing failed: %s", res.String())
	}

	db.logger.Debugw("Activity saved to time-series index",
		"activity_id", activityID,
		"index", indexName,
		"user_id", req.GetUserId(),
	)

	return activityID, nil
}

// UpdateTimeSeriesConfig updates the time-series configuration
func (db *ActivityDB) UpdateTimeSeriesConfig(config TimeSeriesConfig) {
	db.timeSeriesConfig = config
	db.logger.Infow("Time-series configuration updated",
		"use_time_series", config.UseTimeSeries,
		"rotation", config.IndexRotation,
		"volume_threshold", config.VolumeThreshold,
	)
}

// GetIndexInfo returns information about current indices and their usage
func (db *ActivityDB) GetIndexInfo(ctx context.Context) (map[string]interface{}, error) {
	// Get index statistics
	req := esapi.IndicesStatsRequest{
		Index: []string{"activity-events*", "recommendation-interactions*"},
	}

	res, err := req.Do(ctx, db.client)
	if err != nil {
		return nil, fmt.Errorf("failed to get index stats: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("index stats request failed: %s", res.String())
	}

	var stats map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("failed to decode stats response: %w", err)
	}

	// Add configuration info
	info := map[string]interface{}{
		"elasticsearch_stats": stats,
		"time_series_config": map[string]interface{}{
			"enabled":          db.timeSeriesConfig.UseTimeSeries,
			"rotation":         db.timeSeriesConfig.IndexRotation,
			"volume_threshold": db.timeSeriesConfig.VolumeThreshold,
		},
		"current_month_index": db.getTimeSeriesIndexName(ActivityTimeSeriesPattern, "monthly"),
	}

	return info, nil
}

// GetBlogAnalytics calculates detailed analytics for a specific blog
// parseTimeRange converts a time_range string (e.g., "24h", "7d", "30d") into
// Elasticsearch range expressions for tiered resolution:
//   - hourly: bounded to min(requested, 48h) to cap at ~48 buckets
//   - daily:  bounded to min(requested, 90d) to cap at ~90 buckets
//   - monthly: unbounded (all-time) — safe since max ~120 buckets for 10-year-old content
func parseTimeRange(timeRange string) (hourlyGte, dailyGte string) {
	// Defaults: hourly=48h, daily=30d
	hourlyGte = "now-48h"
	dailyGte = "now-30d"

	switch timeRange {
	case "24h":
		hourlyGte = "now-24h"
		dailyGte = "now-24h"
	case "48h":
		hourlyGte = "now-48h"
		dailyGte = "now-48h"
	case "7d":
		hourlyGte = "now-48h" // hourly capped at 48h regardless
		dailyGte = "now-7d"
	case "30d":
		hourlyGte = "now-48h"
		dailyGte = "now-30d"
	case "90d":
		hourlyGte = "now-48h"
		dailyGte = "now-90d"
	case "1y":
		hourlyGte = "now-48h"
		dailyGte = "now-365d"
	}

	return hourlyGte, dailyGte
}

func (db *ActivityDB) GetBlogAnalytics(ctx context.Context, blogID string, timeRange string) (*BlogAnalytics, error) {
	hourlyGte, dailyGte := parseTimeRange(timeRange)
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"resource_id.keyword": blogID,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"client_info.is_bot": true,
						},
					},
				},
			},
		},
		"aggs": map[string]interface{}{
			// Filter for hits (read_blog)
			"views": map[string]interface{}{
				"filter": map[string]interface{}{
					"term": map[string]interface{}{
						"action.keyword": "read_blog",
					},
				},
				"aggs": map[string]interface{}{
					"unique_users": map[string]interface{}{
						"cardinality": map[string]interface{}{
							"field": "client_info.visitor_id.keyword",
						},
					},
					"countries": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "country.keyword",
							"size":  20,
						},
					},
					"referrers": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "referrer.keyword",
							"size":  20,
						},
					},
					"platforms": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "platform.keyword",
							"size":  10,
						},
					},
					"cities": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "city.keyword",
							"size":  20,
						},
					},
					"isps": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "isp.keyword",
							"size":  20,
						},
					},
					"daily_activity": map[string]interface{}{
						"filter": map[string]interface{}{
							"range": map[string]interface{}{
								"@timestamp": map[string]interface{}{
									"gte": dailyGte,
								},
							},
						},
						"aggs": map[string]interface{}{
							"histogram": map[string]interface{}{
								"date_histogram": map[string]interface{}{
									"field":             "@timestamp",
									"calendar_interval": "day",
									"format":            "yyyy-MM-dd",
								},
							},
						},
					},
					"hourly_activity": map[string]interface{}{
						"filter": map[string]interface{}{
							"range": map[string]interface{}{
								"@timestamp": map[string]interface{}{
									"gte": hourlyGte,
								},
							},
						},
						"aggs": map[string]interface{}{
							"histogram": map[string]interface{}{
								"date_histogram": map[string]interface{}{
									"field":             "@timestamp",
									"calendar_interval": "hour",
									"format":            "yyyy-MM-dd HH:00",
								},
							},
						},
					},
					"monthly_activity": map[string]interface{}{
						"date_histogram": map[string]interface{}{
							"field":             "@timestamp",
							"calendar_interval": "month",
							"format":            "yyyy-MM",
						},
					},
					"realtime_views": map[string]interface{}{
						"filter": map[string]interface{}{
							"range": map[string]interface{}{
								"@timestamp": map[string]interface{}{
									"gte": "now-48h",
								},
							},
						},
						"aggs": map[string]interface{}{
							"histogram": map[string]interface{}{
								"date_histogram": map[string]interface{}{
									"field":          "@timestamp",
									"fixed_interval": "1h",
									"format":         "yyyy-MM-dd HH:00",
								},
							},
						},
					},
				},
			},
			// Filter for duration stats (read_duration)
			"durations": map[string]interface{}{
				"filter": map[string]interface{}{
					"term": map[string]interface{}{
						"action.keyword": "read_duration",
					},
				},
				"aggs": map[string]interface{}{
					"valid_views": map[string]interface{}{
						"filter": map[string]interface{}{
							"range": map[string]interface{}{
								"duration_ms": map[string]interface{}{
									"gte": 10000,
								},
							},
						},
					},
					"bounces": map[string]interface{}{
						"filter": map[string]interface{}{
							"range": map[string]interface{}{
								"duration_ms": map[string]interface{}{
									"lt": 5000,
								},
							},
						},
					},
					"avg_read_time": map[string]interface{}{
						"avg": map[string]interface{}{
							"field": "duration_ms",
						},
					},
					"read_time_dist": map[string]interface{}{
						"range": map[string]interface{}{
							"field": "duration_ms",
							"ranges": []map[string]interface{}{
								{"to": 30000, "key": "<30s"},
								{"from": 30000, "to": 60000, "key": "30s-1m"},
								{"from": 60000, "to": 180000, "key": "1m-3m"},
								{"from": 180000, "key": ">3m"},
							},
						},
					},
				},
			},
			// Filter for likes
			"likes": map[string]interface{}{
				"filter": map[string]interface{}{
					"term": map[string]interface{}{
						"action.keyword": "blog_like",
					},
				},
			},
		},
		"size": 0,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return nil, fmt.Errorf("failed to encode query: %w", err)
	}

	// Search across all activity indices
	res, err := db.client.Search(
		db.client.Search.WithContext(ctx),
		db.client.Search.WithIndex("activity-events*"),
		db.client.Search.WithBody(&buf),
		db.client.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search request failed: %s", res.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	aggregations, ok := result["aggregations"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("aggregations not found in response")
	}

	analytics := &BlogAnalytics{
		Countries:            make(map[string]int64),
		Referrers:            make(map[string]int64),
		Platforms:            make(map[string]int64),
		Cities:               make(map[string]int64),
		ISPs:                 make(map[string]int64),
		DailyActivity:        make(map[string]int64),
		HourlyActivity:       make(map[string]int64),
		MonthlyActivity:      make(map[string]int64),
		ReadTimeDistribution: make(map[string]int64),
		RealtimeViews:        make(map[string]int64),
	}

	extractBuckets := func(aggMap map[string]interface{}, aggName string, targetMap map[string]int64) {
		if agg, ok := aggMap[aggName].(map[string]interface{}); ok {
			if buckets, ok := agg["buckets"].([]interface{}); ok {
				for _, b := range buckets {
					bucket := b.(map[string]interface{})
					var key string
					if k, ok := bucket["key_as_string"].(string); ok {
						key = k
					} else if k, ok := bucket["key"].(string); ok {
						key = k
					} else {
						key = fmt.Sprintf("%v", bucket["key"])
					}
					docCount := int64(bucket["doc_count"].(float64))
					targetMap[key] = docCount
				}
			}
		}
	}

	// Parse Likes
	if likesAgg, ok := aggregations["likes"].(map[string]interface{}); ok {
		if docCount, ok := likesAgg["doc_count"].(float64); ok {
			analytics.TotalLikes = int64(docCount)
		}
	}

	// Parse Views and nested aggregations
	if viewsAgg, ok := aggregations["views"].(map[string]interface{}); ok {
		if docCount, ok := viewsAgg["doc_count"].(float64); ok {
			analytics.TotalReads = int64(docCount)
		}

		if uniqueUsers, ok := viewsAgg["unique_users"].(map[string]interface{}); ok {
			if value, ok := uniqueUsers["value"].(float64); ok {
				analytics.UniqueReaders = int64(value)
			}
		}

		extractBuckets(viewsAgg, "countries", analytics.Countries)
		extractBuckets(viewsAgg, "referrers", analytics.Referrers)
		extractBuckets(viewsAgg, "platforms", analytics.Platforms)
		extractBuckets(viewsAgg, "cities", analytics.Cities)
		extractBuckets(viewsAgg, "isps", analytics.ISPs)
		extractBuckets(viewsAgg, "monthly_activity", analytics.MonthlyActivity)

		// daily_activity and hourly_activity are nested under filter aggs
		if dailyAgg, ok := viewsAgg["daily_activity"].(map[string]interface{}); ok {
			extractBuckets(dailyAgg, "histogram", analytics.DailyActivity)
		}
		if hourlyAgg, ok := viewsAgg["hourly_activity"].(map[string]interface{}); ok {
			extractBuckets(hourlyAgg, "histogram", analytics.HourlyActivity)
		}

		// Realtime views is nested deeper
		if realtime, ok := viewsAgg["realtime_views"].(map[string]interface{}); ok {
			extractBuckets(realtime, "histogram", analytics.RealtimeViews)
		}
	}

	// Parse Durations
	if durationsAgg, ok := aggregations["durations"].(map[string]interface{}); ok {
		if validViews, ok := durationsAgg["valid_views"].(map[string]interface{}); ok {
			if docCount, ok := validViews["doc_count"].(float64); ok {
				analytics.ValidViews = int64(docCount)
			}
		}

		if bounces, ok := durationsAgg["bounces"].(map[string]interface{}); ok {
			if docCount, ok := bounces["doc_count"].(float64); ok {
				analytics.Bounces = int64(docCount)
			}
		}

		if avgTime, ok := durationsAgg["avg_read_time"].(map[string]interface{}); ok {
			if val, ok := avgTime["value"].(float64); ok {
				analytics.AvgReadTimeMs = val
			}
		}
		extractBuckets(durationsAgg, "read_time_dist", analytics.ReadTimeDistribution)
	}

	return analytics, nil
}

// StartIPResolver starts a background routine to resolve missing countries from IPs
func (db *ActivityDB) StartIPResolver(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				db.resolveMissingCountries(ctx)
			}
		}
	}()
}

func (db *ActivityDB) resolveMissingCountries(ctx context.Context) {
	// Find documents with missing country but present IP
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"exists": map[string]interface{}{
							"field": "client_ip",
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "country",
								},
							},
						},
					},
					{
						"term": map[string]interface{}{
							"country.keyword": "",
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
		"aggs": map[string]interface{}{
			"ips": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "client_ip.keyword",
					"size":  100, // Process 100 IPs at a time
				},
			},
		},
		"size": 0,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		db.logger.Errorw("failed to encode ip resolution query", "error", err)
		return
	}

	res, err := db.client.Search(
		db.client.Search.WithContext(ctx),
		db.client.Search.WithIndex("activity-events*"),
		db.client.Search.WithBody(&buf),
	)
	if err != nil {
		db.logger.Errorw("failed to search for missing countries", "error", err)
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		db.logger.Errorw("search for missing countries failed", "status", res.Status())
		return
	}

	var result map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return
	}

	aggregations, ok := result["aggregations"].(map[string]interface{})
	if !ok {
		return
	}

	ipsAgg, ok := aggregations["ips"].(map[string]interface{})
	if !ok {
		return
	}

	buckets, ok := ipsAgg["buckets"].([]interface{})
	if !ok {
		return
	}

	db.logger.Debugw("Found IPs to resolve", "count", len(buckets))

	for _, b := range buckets {
		bucket := b.(map[string]interface{})
		ip := bucket["key"].(string)
		db.logger.Debugw("Processing IP", "ip", ip)

		// Resolve IP using external API
		location, err := db.resolveIP(ip)
		if err != nil {
			db.logger.Errorw("failed to resolve ip", "ip", ip, "error", err)
			continue
		}

		if location != nil && location.Status == "success" {
			if err := db.UpdateIPLocation(ctx, ip, location); err != nil {
				db.logger.Errorw("failed to update location for ip", "ip", ip, "error", err)
			}
		}

		// Rate limiting for free API tier (45 req/min)
		time.Sleep(100 * time.Millisecond)
	}
}

func (db *ActivityDB) resolveIP(ip string) (*IPGeoLocation, error) {
	// Handle local IPs
	if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "172.") || ip == "127.0.0.1" || ip == "::1" {
		return &IPGeoLocation{
			Status:     "success",
			Country:    "Local Network",
			City:       "Localhost",
			RegionName: "Local",
			Timezone:   "UTC",
			ISP:        "Local",
		}, nil
	}

	resp, err := http.Get(fmt.Sprintf("http://ip-api.com/json/%s", ip))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch IP geolocation: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var location IPGeoLocation
	if err := json.Unmarshal(body, &location); err != nil {
		return nil, fmt.Errorf("failed to unmarshal geolocation response: %w", err)
	}

	return &location, nil
}

// UpdateIPLocation updates all documents with the given IP to have the specified location data
func (db *ActivityDB) UpdateIPLocation(ctx context.Context, ip string, location *IPGeoLocation) error {
	db.logger.Debugw("Updating IP location", "ip", ip, "location", location)
	query := map[string]interface{}{
		"script": map[string]interface{}{
			"source": `
				ctx._source.country = params.country;
				ctx._source.city = params.city;
				ctx._source.region = params.region;
				ctx._source.timezone = params.timezone;
				ctx._source.isp = params.isp;
				if (ctx._source.client_info != null) {
					ctx._source.client_info.country = params.country;
					ctx._source.client_info.city = params.city;
					ctx._source.client_info.timezone = params.timezone;
				}
			`,
			"lang": "painless",
			"params": map[string]interface{}{
				"country":  location.Country,
				"city":     location.City,
				"region":   location.RegionName,
				"timezone": location.Timezone,
				"isp":      location.ISP,
			},
		},
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"client_ip.keyword": ip,
			},
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return fmt.Errorf("failed to encode update query: %w", err)
	}

	res, err := db.client.UpdateByQuery(
		[]string{"activity-events*"},
		db.client.UpdateByQuery.WithContext(ctx),
		db.client.UpdateByQuery.WithBody(&buf),
		db.client.UpdateByQuery.WithConflicts("proceed"),
	)
	if err != nil {
		return fmt.Errorf("failed to execute update by query: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("update by query failed: %s", res.String())
	}

	db.logger.Debugw("Updated IP location", "ip", ip, "country", location.Country, "city", location.City)

	return nil
}

// GetTrendingBlogs calculates trending blogs based on views and likes
func (db *ActivityDB) GetTrendingBlogs(ctx context.Context, req *pb.GetTrendingBlogsRequest) ([]*pb.TrendingBlog, error) {
	homepagePolicy := homepageTrendingRequested(ctx)
	timeRange := req.GetTimeRange()
	if timeRange == "" {
		timeRange = "24h"
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"action.keyword": "read_blog"}},
					{"range": map[string]interface{}{"@timestamp": map[string]interface{}{"gte": "now-" + timeRange}}},
				},
				"must_not": []map[string]interface{}{
					{"term": map[string]interface{}{"client_info.is_bot": true}},
				},
			},
		},
		"aggs": map[string]interface{}{
			"trending": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "resource_id.keyword",
					"size":  req.GetLimit(),
				},
				"aggs": map[string]interface{}{
					"likes": map[string]interface{}{
						"filter": map[string]interface{}{
							"term": map[string]interface{}{"action.keyword": "blog_like"},
						},
					},
				},
			},
		},
		"size": 0,
	}

	if req.GetAccountId() != "" {
		must := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		must = append(must, map[string]interface{}{"term": map[string]interface{}{"account_id": req.GetAccountId()}})
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = must
	}

	res, err := db.performSearch(ctx, ActivityEventIndex+"*", query)
	if err != nil {
		return nil, err
	}
	if homepagePolicy {
		defer res.Body.Close()
	}

	var searchRes struct {
		TimedOut     json.RawMessage `json:"timed_out"`
		Shards       json.RawMessage `json:"_shards"`
		Aggregations struct {
			Trending struct {
				Buckets []struct {
					Key      string `json:"key"`
					DocCount int64  `json:"doc_count"`
					Likes    struct {
						DocCount int64 `json:"doc_count"`
					} `json:"likes"`
				} `json:"buckets"`
			} `json:"trending"`
		} `json:"aggregations"`
	}

	if err := json.NewDecoder(res.Body).Decode(&searchRes); err != nil {
		return nil, err
	}
	if homepagePolicy {
		if err := validateHomepageTrendingSearch(searchRes.TimedOut, searchRes.Shards); err != nil {
			return nil, err
		}
	}

	trending := make([]*pb.TrendingBlog, 0)
	for _, b := range searchRes.Aggregations.Trending.Buckets {
		// Simple scoring: views + (likes * 5)
		score := float64(b.DocCount) + float64(b.Likes.DocCount*5)
		trending = append(trending, &pb.TrendingBlog{
			BlogId: b.Key,
			Views:  b.DocCount,
			Likes:  b.Likes.DocCount,
			Score:  score,
		})
	}

	return trending, nil
}

// GetActiveUsers returns the number of unique active users and their details
func (db *ActivityDB) GetActiveUsers(ctx context.Context, req *pb.GetActiveUsersRequest) (int64, []*pb.ActiveUser, error) {
	timeRange := req.GetTimeRange()
	if timeRange == "" {
		timeRange = "3h"
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"range": map[string]interface{}{"@timestamp": map[string]interface{}{"gte": "now-" + timeRange}}},
					{"exists": map[string]interface{}{"field": "user_id"}}, // Ensure user_id exists
				},
				"must_not": []map[string]interface{}{
					{"term": map[string]interface{}{"client_info.is_bot": true}},
					{"term": map[string]interface{}{"user_id.keyword": ""}}, // Exclude empty user_ids
				},
			},
		},
		"aggs": map[string]interface{}{
			"active_users_count": map[string]interface{}{
				"cardinality": map[string]interface{}{
					"field": "user_id.keyword",
				},
			},
			"active_users_list": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "user_id.keyword",
					"size":  100, // Limit to top 100 active users for now
				},
				"aggs": map[string]interface{}{
					"last_active": map[string]interface{}{
						"max": map[string]interface{}{
							"field": "@timestamp",
						},
					},
				},
			},
		},
		"size": 0,
	}

	if req.GetAccountId() != "" {
		must := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		must = append(must, map[string]interface{}{"term": map[string]interface{}{"account_id": req.GetAccountId()}})
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = must
	}

	res, err := db.performSearch(ctx, ActivityEventIndex+"*", query)
	if err != nil {
		return 0, nil, err
	}

	var searchRes struct {
		Aggregations struct {
			ActiveUsersCount struct {
				Value int64 `json:"value"`
			} `json:"active_users_count"`
			ActiveUsersList struct {
				Buckets []struct {
					Key        string `json:"key"`
					LastActive struct {
						ValueAsString string  `json:"value_as_string"`
						Value         float64 `json:"value"`
					} `json:"last_active"`
				} `json:"buckets"`
			} `json:"active_users_list"`
		} `json:"aggregations"`
	}

	if err := json.NewDecoder(res.Body).Decode(&searchRes); err != nil {
		return 0, nil, err
	}

	var activeUsersList []*pb.ActiveUser
	for _, bucket := range searchRes.Aggregations.ActiveUsersList.Buckets {
		// Use Value (epoch millis) instead of parsing string to avoid format issues
		// and handle potential scientific notation in JSON numbers
		sec := int64(bucket.LastActive.Value) / 1000
		nsec := int64(bucket.LastActive.Value) % 1000 * 1000000
		lastActiveTime := time.Unix(sec, nsec)

		activeUsersList = append(activeUsersList, &pb.ActiveUser{
			UserId:     bucket.Key,
			LastActive: timestamppb.New(lastActiveTime),
		})
	}

	return searchRes.Aggregations.ActiveUsersCount.Value, activeUsersList, nil
}

// GetAccountActivities retrieves activities filtered by account_id
func (db *ActivityDB) GetAccountActivities(ctx context.Context, req *pb.GetAccountActivitiesRequest) ([]*pb.ActivityEvent, int64, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"account_id": req.GetAccountId()}},
				},
			},
		},
		"sort": []map[string]interface{}{
			{"@timestamp": map[string]interface{}{"order": "desc"}},
		},
		"size": req.GetLimit(),
		"from": req.GetOffset(),
	}

	if req.GetAction() != "" {
		must := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		must = append(must, map[string]interface{}{"term": map[string]interface{}{"action.keyword": req.GetAction()}})
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = must
	}

	if req.GetCategory() != pb.ActivityCategory_CATEGORY_UNSPECIFIED {
		must := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		must = append(must, map[string]interface{}{"term": map[string]interface{}{"category": req.GetCategory().String()}})
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = must
	}

	// Add time range
	if req.GetStartTime() != nil || req.GetEndTime() != nil {
		rangeFilter := map[string]interface{}{"range": map[string]interface{}{"@timestamp": map[string]interface{}{}}}
		if req.GetStartTime() != nil {
			rangeFilter["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})["gte"] = req.GetStartTime().AsTime().Format(time.RFC3339)
		}
		if req.GetEndTime() != nil {
			rangeFilter["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})["lte"] = req.GetEndTime().AsTime().Format(time.RFC3339)
		}
		must := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		must = append(must, rangeFilter)
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = must
	}

	res, err := db.performSearch(ctx, ActivityEventIndex+"*", query)
	if err != nil {
		return nil, 0, err
	}

	// Reusing the same parsing logic as GetUserActivities
	return db.parseActivityEvents(res)
}

// GetAdvancedAnalytics performs complex aggregations for the 5-point suite
func (db *ActivityDB) GetAdvancedAnalytics(ctx context.Context, req *pb.GetAdvancedAnalyticsRequest) (*pb.AdvancedAnalytics, error) {
	timeRange := req.GetTimeRange()
	if timeRange == "" {
		timeRange = "7d"
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"range": map[string]interface{}{"@timestamp": map[string]interface{}{"gte": "now-" + timeRange}}},
				},
				"must_not": []map[string]interface{}{
					{"term": map[string]interface{}{"client_info.is_bot": true}},
				},
			},
		},
		"aggs": map[string]interface{}{
			"geo_hotspots": map[string]interface{}{
				"terms": map[string]interface{}{"field": "client_info.country.keyword", "size": 10},
			},
			"platforms": map[string]interface{}{
				"terms": map[string]interface{}{"field": "client_info.platform.keyword"},
			},
			"peak_times": map[string]interface{}{
				"date_histogram": map[string]interface{}{
					"field":             "@timestamp",
					"calendar_interval": "hour",
				},
			},
			"retention": map[string]interface{}{
				"filters": map[string]interface{}{
					"filters": map[string]interface{}{
						"new_users":       map[string]interface{}{"term": map[string]interface{}{"action.keyword": "register"}},
						"returning_users": map[string]interface{}{"term": map[string]interface{}{"action.keyword": "login"}},
					},
				},
				"aggs": map[string]interface{}{
					"unique_count": map[string]interface{}{
						"cardinality": map[string]interface{}{"field": "client_info.visitor_id.keyword"},
					},
				},
			},
		},
		"size": 0,
	}

	if req.GetAccountId() != "" {
		must := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{})
		must = append(must, map[string]interface{}{"term": map[string]interface{}{"account_id": req.GetAccountId()}})
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = must
	}

	res, err := db.performSearch(ctx, ActivityEventIndex+"*", query)
	if err != nil {
		return nil, err
	}

	var searchRes struct {
		Aggregations struct {
			GeoHotspots struct {
				Buckets []struct {
					Key      string `json:"key"`
					DocCount int64  `json:"doc_count"`
				} `json:"buckets"`
			} `json:"geo_hotspots"`
			Platforms struct {
				Buckets []struct {
					Key      string `json:"key"`
					DocCount int64  `json:"doc_count"`
				} `json:"buckets"`
			} `json:"platforms"`
			PeakTimes struct {
				Buckets []struct {
					Key      string `json:"key_as_string"`
					DocCount int64  `json:"doc_count"`
				} `json:"buckets"`
			} `json:"peak_times"`
			Retention struct {
				Buckets map[string]struct {
					UniqueCount struct {
						Value int64 `json:"value"`
					} `json:"unique_count"`
				} `json:"buckets"`
			} `json:"retention"`
		} `json:"aggregations"`
	}

	if err := json.NewDecoder(res.Body).Decode(&searchRes); err != nil {
		return nil, err
	}

	analytics := &pb.AdvancedAnalytics{
		GeographicHotspots: make(map[string]int64),
		PlatformBias:       make(map[string]int64),
		PeakReadingTimes:   make(map[string]int64),
	}

	for _, b := range searchRes.Aggregations.GeoHotspots.Buckets {
		analytics.GeographicHotspots[b.Key] = b.DocCount
	}
	for _, b := range searchRes.Aggregations.Platforms.Buckets {
		analytics.PlatformBias[b.Key] = b.DocCount
	}
	for _, b := range searchRes.Aggregations.PeakTimes.Buckets {
		analytics.PeakReadingTimes[b.Key] = b.DocCount
	}

	newUsers := searchRes.Aggregations.Retention.Buckets["new_users"].UniqueCount.Value
	returningUsers := searchRes.Aggregations.Retention.Buckets["returning_users"].UniqueCount.Value
	analytics.NewUsers = newUsers
	analytics.ReturningUsers = returningUsers
	if (newUsers + returningUsers) > 0 {
		analytics.RetentionRate = float64(returningUsers) / float64(newUsers+returningUsers)
	}

	return analytics, nil
}

// performSearch is a helper to run ES queries
func (db *ActivityDB) performSearch(ctx context.Context, index string, query map[string]interface{}) (*esapi.Response, error) {
	queryBytes, _ := json.Marshal(query)
	searchReq := esapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
	}
	res, err := searchReq.Do(ctx, db.client)
	if err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("search error: %s", res.String())
	}
	return res, nil
}

// parseActivityEvents is a helper to parse search hits into pb.ActivityEvent
func (db *ActivityDB) parseActivityEvents(res *esapi.Response) ([]*pb.ActivityEvent, int64, error) {
	var searchResponse struct {
		Hits struct {
			Total struct {
				Value int64 `json:"total"`
			} `json:"total"`
			Hits []struct {
				Source interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&searchResponse); err != nil {
		return nil, 0, err
	}

	activities := make([]*pb.ActivityEvent, 0, len(searchResponse.Hits.Hits))
	for _, hit := range searchResponse.Hits.Hits {
		doc := hit.Source.(map[string]interface{})

		// Map ES fields back to pb.ActivityEvent
		event := &pb.ActivityEvent{
			Id:         fmt.Sprintf("%v", doc["id"]),
			UserId:     fmt.Sprintf("%v", doc["user_id"]),
			AccountId:  fmt.Sprintf("%v", doc["account_id"]),
			SessionId:  fmt.Sprintf("%v", doc["session_id"]),
			Action:     fmt.Sprintf("%v", doc["action"]),
			Resource:   fmt.Sprintf("%v", doc["resource"]),
			ResourceId: fmt.Sprintf("%v", doc["resource_id"]),
			Success:    doc["success"].(bool),
			DurationMs: int64(doc["duration_ms"].(float64)),
		}

		// Handle @timestamp
		if ts, ok := doc["@timestamp"].(string); ok {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				event.Timestamp = timestamppb.New(t)
			}
		}

		// Handle Category
		if cat, ok := doc["category"].(string); ok {
			if val, exists := pb.ActivityCategory_value[cat]; exists {
				event.Category = pb.ActivityCategory(val)
			}
		}

		activities = append(activities, event)
	}

	return activities, searchResponse.Hits.Total.Value, nil
}
