package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/constants"
	"go.uber.org/zap"
)

// ScheduleBlogOptions contains all options for scheduling a blog
type ScheduleBlogOptions struct {
	BlogID       string    // Required: Blog ID to schedule
	ScheduleTime time.Time // Required: When to publish the blog (in UTC)
	Timezone     string    // Optional: User's timezone for display purposes
}

// Validate checks if the required fields are present
func (o *ScheduleBlogOptions) Validate() error {
	if o.BlogID == "" {
		return fmt.Errorf("blogId cannot be empty")
	}
	if o.ScheduleTime.IsZero() {
		return fmt.Errorf("scheduleTime cannot be empty")
	}
	return nil
}

// DueScheduledBlog represents a scheduled blog ready for publishing,
// along with Elasticsearch version control fields for optimistic concurrency.
type DueScheduledBlog struct {
	Source      map[string]interface{}
	SeqNo       int
	PrimaryTerm int
}

// ErrVersionConflict is returned when an Elasticsearch update fails due to a
// version conflict, indicating another scheduler instance already processed the document.
var ErrVersionConflict = errors.New("elasticsearch version conflict: document was modified by another process")

type ElasticsearchStorage interface {
	DraftABlog(ctx context.Context, blog *pb.DraftBlogRequest) (*esapi.Response, error)
	GetDraftBlogsByOwnerAccountID(ctx context.Context, ownerAccountID string) (*pb.GetDraftBlogsRes, error)
	GetDraftBlogByBlogId(ctx context.Context, blogId string) (*pb.BlogByIdRes, error)
	DoesBlogExist(ctx context.Context, blogID string) (bool, map[string]interface{}, error)
	PublishBlogById(ctx context.Context, blogId string) (*esapi.Response, error)
	ScheduleBlogById(ctx context.Context, opts ScheduleBlogOptions) (*esapi.Response, error)
	MoveBlogToDraft(ctx context.Context, blogId string) (*esapi.Response, error)
	GetPublishedBlogByTagsName(ctx context.Context, tags ...string) (*pb.GetBlogsByTagsNameRes, error)
	GetPublishedBlogById(ctx context.Context, id string) (*pb.BlogByIdRes, error)
	AchieveAPublishedBlogById(ctx context.Context, blogId string) (*esapi.Response, error)
	DeleteABlogById(ctx context.Context, blogId string) (*esapi.Response, error)
	GetLast100BlogsLatestFirst(ctx context.Context) (*pb.GetBlogsByTagsNameRes, error)
	GetDraftedBlogByIdAndOwner(ctx context.Context, blogId, ownerAccountId string) (*pb.BlogByIdRes, error)
	GetPublishedBlogByIdAndOwner(ctx context.Context, blogId, ownerAccountId string) (*pb.BlogByIdRes, error)
	GetPublishedBlogsByOwnerAccountID(ctx context.Context, ownerAccountID string) (*pb.GetPublishedBlogsRes, error)
	GetBlogsByBlogIds(ctx context.Context, blogIds []string) (*pb.GetBlogsRes, error)
	DeleteBlogsByOwnerAccountID(ctx context.Context, ownerAccountId string) (*esapi.Response, error)
	GetScheduledBlogsByOwnerAccountID(ctx context.Context, ownerAccountId string) (*pb.GetPublishedBlogsRes, error)
	GetDueScheduledBlogs(ctx context.Context, currentTime time.Time, maxFailedAttempts int) ([]DueScheduledBlog, error)
	PublishScheduledBlog(ctx context.Context, blogId string, seqNo *int, primaryTerm *int) (*esapi.Response, error)
	IncrementScheduleFailedAttempts(ctx context.Context, blogId string, reason string) error

	// -------------------------------------------------------------------------------- V2 --------------------------------------------------------------------------------
	SaveBlog(ctx context.Context, blog map[string]interface{}) (*esapi.Response, error)
	GetBlogsOfUsersByAccountIds(ctx context.Context, accountIds []string, limit, offset int32) ([]map[string]interface{}, error)
	GetBlogsByTagsAccId(ctx context.Context, accountId string, tags []string, isDraft bool, limit, offset int32) ([]map[string]interface{}, error)
	GetBlogsByAccountId(ctx context.Context, accountId string, isDraft bool, limit, offset int32) ([]map[string]interface{}, error)
	GetBlogByBlogId(ctx context.Context, blogId string, isDraft bool) (map[string]interface{}, error)
	GetABlogByBlogIdAccId(ctx context.Context, blogId, accountId string, isDraft bool) (map[string]interface{}, error)
	GetBlogsByTags(ctx context.Context, tags []string, isDraft bool, limit, offset int32) ([]map[string]interface{}, error)
	GetBlogsByBlogIdsV2(ctx context.Context, blogIds []string, limit, offset int32) ([]map[string]interface{}, error)
	GetAllPublishedBlogsLatestFirst(ctx context.Context, limit, offset int) ([]map[string]interface{}, error)
	GetAllTagsFromUserPublishedBlogs(ctx context.Context, accountID string) ([]string, error)

	// -------------------------------------------------------------------------------- Metadata --------------------------------------------------------------------------------
	GetBlogsMetadataByTags(ctx context.Context, tags []string, isDraft bool, limit, offset int32) ([]map[string]interface{}, int, error)
	GetAllPublishedBlogsMetadata(ctx context.Context, limit, offset int) ([]map[string]interface{}, int, error)
	GetBlogsMetadataByQuery(ctx context.Context, queryTexts []string, isDraft bool, limit, offset int32) ([]map[string]interface{}, int, error)
	GetBlogsMetaByAccountId(ctx context.Context, accountId string, isDraft bool, isSchedule bool, limit, offset int32) ([]map[string]interface{}, int, error)
	GetBlogsMetaByBlogIdsV2(ctx context.Context, blogIds []string, isDraft bool, limit, offset int32) ([]map[string]interface{}, int, error)
	ListBlogIDs(ctx context.Context, limit, offset int32) ([]string, int, error)
}

type elasticsearchStorage struct {
	client *elasticsearch.Client
	log    *zap.SugaredLogger
}

func NewElasticsearchClient(url, username, password string, log *zap.SugaredLogger) (ElasticsearchStorage, error) {
	client, err := NewESClient(url, username, password, log)
	if err != nil {
		log.Errorw("elasticsearch connect failed", "err", err)
		return nil, err
	}
	return &elasticsearchStorage{client: client, log: log}, nil
}

func (es *elasticsearchStorage) DraftABlog(ctx context.Context, blog *pb.DraftBlogRequest) (*esapi.Response, error) {
	bs, err := json.Marshal(blog)
	if err != nil {
		es.log.Errorw("draft marshal failed", "err", err)
		return nil, err
	}

	document := strings.NewReader(string(bs))

	req := esapi.IndexRequest{
		Index:      constants.ElasticsearchBlogIndex,
		DocumentID: blog.BlogId,
		Body:       document,
	}

	insertResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("DraftABlog: error while indexing blog, error: %+v", err)
		return insertResponse, err
	}

	if insertResponse.IsError() {
		err = fmt.Errorf("DraftABlog: error indexing blog, response: %+v", insertResponse)
		es.log.Error(err)
		return insertResponse, err
	}

	return insertResponse, nil
}

func (es *elasticsearchStorage) GetDraftBlogsByOwnerAccountID(ctx context.Context, ownerAccountID string) (*pb.GetDraftBlogsRes, error) {
	// Ensure ownerAccountID is properly set
	if ownerAccountID == "" {
		es.log.Error("GetDraftBlogsByOwnerAccountID: ownerAccountID is empty")
		return nil, fmt.Errorf("ownerAccountID cannot be empty")
	}

	// Build the query to search for draft blogs by owner_account_id, sorted by published_time in descending order
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"owner_account_id": ownerAccountID,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": true,
						},
					},
				},
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetDraftBlogsByOwnerAccountID: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetDraftBlogsByOwnerAccountID: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetDraftBlogsByOwnerAccountID: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetDraftBlogsByOwnerAccountID: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetDraftBlogsByOwnerAccountID: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetDraftBlogsByOwnerAccountID: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetDraftBlogsByOwnerAccountID: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Convert the hits to a slice of GetDraftBlogsRes
	var blogs = &pb.GetDraftBlogsRes{
		Blogs: make([]*pb.GetBlogs, 0, len(hits)),
	}
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		hitBytes, err := json.Marshal(hitSource)
		if err != nil {
			es.log.Errorf("GetDraftBlogsByOwnerAccountID: error marshaling hit source, error: %v", err)
			continue
		}

		var blog pb.GetBlogs
		if err := json.Unmarshal(hitBytes, &blog); err != nil {
			es.log.Errorf("GetDraftBlogsByOwnerAccountID: error unmarshaling hit to GetBlogs, error: %v", err)
			continue
		}
		blogs.Blogs = append(blogs.Blogs, &blog)
	}

	es.log.Infof("GetDraftBlogsByOwnerAccountID: successfully fetched %d draft blogs for owner_account_id: %s", len(blogs.Blogs), ownerAccountID)
	return blogs, nil
}

func (es *elasticsearchStorage) GetPublishedBlogsByOwnerAccountID(ctx context.Context, ownerAccountID string) (*pb.GetPublishedBlogsRes, error) {
	// Ensure ownerAccountID is properly set
	if ownerAccountID == "" {
		es.log.Error("GetPublishedBlogsByOwnerAccountID: ownerAccountID is empty")
		return nil, fmt.Errorf("ownerAccountID cannot be empty")
	}

	// Build the query to search for published blogs by owner_account_id, sorted by published_time in descending order
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"owner_account_id": ownerAccountID,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": false,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
					{
						// Exclude scheduled blogs; docs without is_scheduled are treated as non-scheduled
						"term": map[string]interface{}{
							"is_scheduled": true,
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": false,
						},
					},
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "is_archived",
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetPublishedBlogsByOwnerAccountID: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetPublishedBlogsByOwnerAccountID: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetPublishedBlogsByOwnerAccountID: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetPublishedBlogsByOwnerAccountID: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetPublishedBlogsByOwnerAccountID: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetPublishedBlogsByOwnerAccountID: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetPublishedBlogsByOwnerAccountID: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Convert the hits to a slice of GetDraftBlogsRes
	var blogs = &pb.GetPublishedBlogsRes{
		Blogs: make([]*pb.GetBlogs, 0, len(hits)),
	}
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		hitBytes, err := json.Marshal(hitSource)
		if err != nil {
			es.log.Errorf("GetPublishedBlogsByOwnerAccountID: error marshaling hit source, error: %v", err)
			continue
		}

		var blog pb.GetBlogs
		if err := json.Unmarshal(hitBytes, &blog); err != nil {
			es.log.Errorf("GetPublishedBlogsByOwnerAccountID: error unmarshaling hit to GetBlogs, error: %v", err)
			continue
		}
		blogs.Blogs = append(blogs.Blogs, &blog)
	}

	es.log.Infof("GetPublishedBlogsByOwnerAccountID: successfully fetched %d published blogs for owner_account_id: %s", len(blogs.Blogs), ownerAccountID)
	return blogs, nil
}

// Todo: get all the schedule blog and try to think about make this function also flexable for getting schedule blog with accountid
func (es *elasticsearchStorage) GetScheduledBlogsByOwnerAccountID(ctx context.Context, ownerAccountID string) (*pb.GetPublishedBlogsRes, error) {

	// Write and query to get schedule blog from elastic search
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"schedule_time": map[string]interface{}{
					"order":         "asc",
					"unmapped_type": "date",
				},
			},
		},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"owner_account_id": ownerAccountID,
						},
					},
					{
						"term": map[string]interface{}{
							"is_scheduled": true,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": true,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": false,
						},
					},
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "is_archived",
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	bs, err := json.Marshal(query)

	// Todo: if changing the function name change the below name as well (match function name and below error message)
	if err != nil {
		es.log.Errorf("GetAllScheduleBlog: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)

	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetAllScheduleBlogs: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetAllScheduleBlogs: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetAllScheduleBlogs: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetAllScheduleBlogs: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetAllScheduleBlogs: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Convert the hits to a slice of GetDraftBlogsRes
	var blogs = &pb.GetPublishedBlogsRes{
		Blogs: make([]*pb.GetBlogs, 0, len(hits)),
	}
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		hitBytes, err := json.Marshal(hitSource)
		if err != nil {
			es.log.Errorf("GetAllScheduleBlogs: error marshaling hit source, error: %v", err)
			continue
		}

		var blog pb.GetBlogs
		if err := json.Unmarshal(hitBytes, &blog); err != nil {
			es.log.Errorf("GetAllScheduleBlogs: error unmarshaling hit to GetBlogs, error: %v", err)
			continue
		}
		blogs.Blogs = append(blogs.Blogs, &blog)
	}

	es.log.Infof("GetAllScheduleBlogs: successfully fetched %d published blogs for owner_account_id: %s", len(blogs.Blogs))
	return blogs, nil

}

func (es *elasticsearchStorage) DoesBlogExist(ctx context.Context, blogID string) (bool, map[string]interface{}, error) {
	// Ensure blogID is not empty
	if blogID == "" {
		es.log.Error("DoesBlogExist: blogID is empty")
		return false, nil, fmt.Errorf("blogID cannot be empty")
	}

	// Create a Get request to check if the document exists
	req := esapi.GetRequest{
		Index:      constants.ElasticsearchBlogIndex,
		DocumentID: blogID,
	}

	// Execute the Get request
	getResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("DoesBlogExist: error executing Get request, error: %+v", err)
		return false, nil, err
	}
	defer func() {
		if err := getResponse.Body.Close(); err != nil {
			es.log.Errorf("DoesBlogExist: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates the document exists
	switch getResponse.StatusCode {
	case http.StatusOK:
		// Parse the response body to extract blog details
		bodyBytes, err := io.ReadAll(getResponse.Body)
		if err != nil {
			es.log.Errorf("DoesBlogExist: error reading response body, error: %v", err)
			return false, nil, err
		}

		var response map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &response); err != nil {
			es.log.Errorf("DoesBlogExist: error decoding response body, error: %v", err)
			return false, nil, err
		}

		// Extract the _source field which contains the blog details
		blogDetails, ok := response["_source"].(map[string]interface{})
		if !ok {
			es.log.Errorf("DoesBlogExist: failed to parse _source from response")
			return false, nil, fmt.Errorf("failed to parse blog details from response")
		}

		es.log.Infof("DoesBlogExist: blog with id %s exists", blogID)
		return true, blogDetails, nil

	case http.StatusNotFound:
		es.log.Infof("DoesBlogExist: blog with id %s does not exist", blogID)
		return false, nil, nil
	}

	// If the response is something else, log it as an error
	err = fmt.Errorf("DoesBlogExist: unexpected status code %d", getResponse.StatusCode)
	es.log.Error(err)
	return false, nil, err
}

func (es *elasticsearchStorage) PublishBlogById(ctx context.Context, blogId string) (*esapi.Response, error) {
	// Ensure blogId is not empty
	if blogId == "" {
		es.log.Error("PublishBlogById: blogId is empty")
		return nil, fmt.Errorf("blogId cannot be empty")
	}

	// Build the update query to set is_draft to false, is_scheduled to false, and add published_time.
	// Always set is_scheduled=false so future queries work correctly for old docs that lacked is_scheduled.
	updateScript := map[string]interface{}{
		"script": map[string]interface{}{
			"source": "ctx._source.is_draft = false; ctx._source.is_scheduled = false; ctx._source.published_time = params.published_time;",
			"params": map[string]interface{}{
				"published_time": time.Now().Format(time.RFC3339),
			},
		},
	}

	// Marshal the update script to JSON
	bs, err := json.Marshal(updateScript)
	if err != nil {
		es.log.Errorf("PublishBlogById: cannot marshal the update script, error: %v", err)
		return nil, err
	}

	// Create an update request with retry_on_conflict to handle concurrent writes
	// from DraftBlogV2 streaming auto-save that can bump the doc's _seq_no mid-update.
	retries := 3
	req := esapi.UpdateRequest{
		Index:           constants.ElasticsearchBlogIndex,
		DocumentID:      blogId,
		Body:            strings.NewReader(string(bs)),
		RetryOnConflict: &retries,
	}

	// Execute the update request
	updateResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("PublishBlogById: error executing update request, error: %+v", err)
		return updateResponse, err
	}
	defer func() {
		if err := updateResponse.Body.Close(); err != nil {
			es.log.Errorf("PublishBlogById: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if updateResponse.IsError() {
		err = fmt.Errorf("PublishBlogById: update query failed, response: %+v", updateResponse)
		es.log.Error(err)
		return updateResponse, err
	}

	es.log.Infof("PublishBlogById: successfully published blog with id: %s", blogId)
	return updateResponse, nil
}

// ScheduleBlogById schedules a blog for future publication
func (es *elasticsearchStorage) ScheduleBlogById(ctx context.Context, opts ScheduleBlogOptions) (*esapi.Response, error) {
	// Validate options
	if err := opts.Validate(); err != nil {
		es.log.Errorf("ScheduleBlogById: validation failed: %v", err)
		return nil, err
	}

	// Build the update script - sets is_scheduled=true, is_draft=true, and stores schedule_time + timezone
	updateScript := map[string]interface{}{
		"script": map[string]interface{}{
			"source": `ctx._source.is_scheduled = true; 
			            ctx._source.is_draft = true; 
			            ctx._source.schedule_time = params.schedule_time; 
			            ctx._source.timezone = params.timezone;`,
			"params": map[string]interface{}{
				"schedule_time": opts.ScheduleTime.UTC().Format(time.RFC3339), // Store in UTC
				"timezone":      opts.Timezone,
			},
		},
	}

	bs, err := json.Marshal(updateScript)
	if err != nil {
		es.log.Errorf("ScheduleBlogById: cannot marshal update script: %v", err)
		return nil, err
	}

	retries := 3
	req := esapi.UpdateRequest{
		Index:           constants.ElasticsearchBlogIndex,
		DocumentID:      opts.BlogID,
		Body:            strings.NewReader(string(bs)),
		RetryOnConflict: &retries,
	}

	updateResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("ScheduleBlogById: update request failed: %v", err)
		return updateResponse, err
	}
	defer func() {
		if err := updateResponse.Body.Close(); err != nil {
			es.log.Errorf("ScheduleBlogById: error closing response body: %v", err)
		}
	}()

	if updateResponse.IsError() {
		err = fmt.Errorf("ScheduleBlogById: update failed for blog %s: %s", opts.BlogID, updateResponse.String())
		es.log.Error(err)
		return updateResponse, err
	}

	es.log.Infof("ScheduleBlogById: successfully scheduled blog %s for %s (%s)",
		opts.BlogID, opts.ScheduleTime.Format(time.RFC3339), opts.Timezone)
	return updateResponse, nil
}

func (es *elasticsearchStorage) MoveBlogToDraft(ctx context.Context, blogId string) (*esapi.Response, error) {
	// Ensure blogId is not empty
	if blogId == "" {
		es.log.Error("MoveBlogToDraft: blogId is empty")
		return nil, fmt.Errorf("blogId cannot be empty")
	}

	// Build the update query to set is_draft to true and clear scheduling/publishing flags
	updateScript := map[string]interface{}{
		"script": map[string]interface{}{
			"source": "ctx._source.is_draft = true; ctx._source.is_scheduled = false; ctx._source.schedule_time = null; ctx._source.published_time = params.published_time; ctx._source.timezone = null",
			"params": map[string]interface{}{
				"published_time": time.Now().Format(time.RFC3339),
			},
		},
	}

	// Marshal the update script to JSON
	bs, err := json.Marshal(updateScript)
	if err != nil {
		es.log.Errorf("MoveBlogToDraft: cannot marshal the update script, error: %v", err)
		return nil, err
	}

	// Create an update request with retry_on_conflict for concurrent write safety
	retries := 3
	req := esapi.UpdateRequest{
		Index:           constants.ElasticsearchBlogIndex,
		DocumentID:      blogId,
		Body:            strings.NewReader(string(bs)),
		RetryOnConflict: &retries,
	}

	// Execute the update request
	updateResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("MoveBlogToDraft: error executing update request, error: %+v", err)
		return updateResponse, err
	}
	defer func() {
		if err := updateResponse.Body.Close(); err != nil {
			es.log.Errorf("MoveBlogToDraft: error closing response body, error: %v", err)
		}
	}()
	// Check if the response indicates an error
	if updateResponse.IsError() {
		err = fmt.Errorf("MoveBlogToDraft: update query failed, response: %+v", updateResponse)
		es.log.Error(err)
		return updateResponse, err
	}

	es.log.Infof("MoveBlogToDraft: successfully published blog with id: %s", blogId)
	return updateResponse, nil
}

// GetDueScheduledBlogs returns all scheduled blogs that are due for publishing (schedule_time <= currentTime).
// Blogs that have exceeded maxFailedAttempts are excluded to prevent infinite retry loops.
// Returns blog data along with Elasticsearch version control fields for optimistic concurrency.
func (es *elasticsearchStorage) GetDueScheduledBlogs(ctx context.Context, currentTime time.Time, maxFailedAttempts int) ([]DueScheduledBlog, error) {
	// Query for scheduled blogs where:
	// - is_scheduled = true
	// - schedule_time <= currentTime
	// - is_archived != true
	// - failed_attempts < maxFailedAttempts (or field doesn't exist)
	query := map[string]interface{}{
		"size": 100, // Process up to 100 blogs per batch
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_scheduled": true,
						},
					},
					{
						"range": map[string]interface{}{
							"schedule_time": map[string]interface{}{
								"lte": currentTime.Format(time.RFC3339),
							},
						},
					},
					{
						// Exclude blogs that have exceeded max failed attempts.
						// Match if: failed_attempts < max OR the field doesn't exist yet.
						"bool": map[string]interface{}{
							"should": []map[string]interface{}{
								{
									"range": map[string]interface{}{
										"failed_attempts": map[string]interface{}{
											"lt": maxFailedAttempts,
										},
									},
								},
								{
									"bool": map[string]interface{}{
										"must_not": []map[string]interface{}{
											{
												"exists": map[string]interface{}{
													"field": "failed_attempts",
												},
											},
										},
									},
								},
							},
							"minimum_should_match": 1,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
				},
			},
		},
		"sort": []map[string]interface{}{
			{
				"schedule_time": map[string]interface{}{
					"order":         "asc",
					"unmapped_type": "date",
				},
			},
		},
	}

	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetDueScheduledBlogs: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Request seq_no and primary_term for optimistic concurrency control
	seqNoPrimaryTerm := true
	req := esapi.SearchRequest{
		Index:            []string{constants.ElasticsearchBlogIndex},
		Body:             strings.NewReader(string(bs)),
		SeqNoPrimaryTerm: &seqNoPrimaryTerm,
	}

	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetDueScheduledBlogs: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetDueScheduledBlogs: error closing response body, error: %v", err)
		}
	}()

	if res.IsError() {
		err = fmt.Errorf("GetDueScheduledBlogs: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetDueScheduledBlogs: error reading response body, error: %v", err)
		return nil, err
	}

	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetDueScheduledBlogs: error decoding response body, error: %v", err)
		return nil, err
	}

	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		es.log.Debug("GetDueScheduledBlogs: no scheduled blogs due")
		return []DueScheduledBlog{}, nil
	}

	var blogs []DueScheduledBlog
	for _, hit := range hits {
		hitMap := hit.(map[string]interface{})
		source, _ := hitMap["_source"].(map[string]interface{})

		seqNo := 0
		if v, ok := hitMap["_seq_no"].(float64); ok {
			seqNo = int(v)
		}
		primaryTerm := 0
		if v, ok := hitMap["_primary_term"].(float64); ok {
			primaryTerm = int(v)
		}

		blogs = append(blogs, DueScheduledBlog{
			Source:      source,
			SeqNo:       seqNo,
			PrimaryTerm: primaryTerm,
		})
	}

	es.log.Debug("GetDueScheduledBlogs: found %d scheduled blogs ready to be published", len(blogs))
	return blogs, nil
}

// PublishScheduledBlog publishes a scheduled blog by setting is_draft=false, is_scheduled=false, and published_time.
// Uses optimistic concurrency control (seqNo/primaryTerm) to prevent duplicate publishes across multiple instances.
// Returns ErrVersionConflict if the document was already modified by another instance.
func (es *elasticsearchStorage) PublishScheduledBlog(ctx context.Context, blogId string, seqNo *int, primaryTerm *int) (*esapi.Response, error) {
	if blogId == "" {
		es.log.Error("PublishScheduledBlog: blogId is empty")
		return nil, fmt.Errorf("blogId cannot be empty")
	}

	// Build the update query to publish the scheduled blog.
	// Also cleans up scheduling metadata (schedule_time, failed_attempts, etc.)
	updateScript := map[string]interface{}{
		"script": map[string]interface{}{
			"source": `ctx._source.is_draft = false;
			            ctx._source.is_scheduled = false;
			            ctx._source.published_time = params.published_time;
			            ctx._source.schedule_time = null;
			            ctx._source.failed_attempts = null;
			            ctx._source.last_failure_reason = null;
			            ctx._source.last_failure_time = null;`,
			"params": map[string]interface{}{
				"published_time": time.Now().Format(time.RFC3339),
			},
		},
	}

	bs, err := json.Marshal(updateScript)
	if err != nil {
		es.log.Errorf("PublishScheduledBlog: cannot marshal the update script, error: %v", err)
		return nil, err
	}

	req := esapi.UpdateRequest{
		Index:         constants.ElasticsearchBlogIndex,
		DocumentID:    blogId,
		Body:          strings.NewReader(string(bs)),
		IfSeqNo:       seqNo,
		IfPrimaryTerm: primaryTerm,
	}

	updateResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("PublishScheduledBlog: error executing update request, error: %+v", err)
		return updateResponse, err
	}
	defer func() {
		if err := updateResponse.Body.Close(); err != nil {
			es.log.Errorf("PublishScheduledBlog: error closing response body, error: %v", err)
		}
	}()

	// Check for version conflict — another instance already processed this blog
	if updateResponse.StatusCode == http.StatusConflict {
		es.log.Infof("PublishScheduledBlog: version conflict for blog %s - already processed by another instance", blogId)
		return nil, ErrVersionConflict
	}

	if updateResponse.IsError() {
		err = fmt.Errorf("PublishScheduledBlog: update query failed, response: %+v", updateResponse)
		es.log.Error(err)
		return updateResponse, err
	}

	es.log.Infof("PublishScheduledBlog: successfully published scheduled blog with id: %s", blogId)
	return updateResponse, nil
}

// IncrementScheduleFailedAttempts increments the failed_attempts counter for a scheduled blog
// and records the failure reason and time. This prevents blogs with persistent errors from
// being retried indefinitely by the scheduler.
func (es *elasticsearchStorage) IncrementScheduleFailedAttempts(ctx context.Context, blogId string, reason string) error {
	if blogId == "" {
		return fmt.Errorf("blogId cannot be empty")
	}

	updateScript := map[string]interface{}{
		"script": map[string]interface{}{
			"source": `if (ctx._source.failed_attempts == null) {
			              ctx._source.failed_attempts = 1;
			            } else {
			              ctx._source.failed_attempts += 1;
			            }
			            ctx._source.last_failure_reason = params.reason;
			            ctx._source.last_failure_time = params.failure_time;`,
			"params": map[string]interface{}{
				"reason":       reason,
				"failure_time": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	bs, err := json.Marshal(updateScript)
	if err != nil {
		es.log.Errorf("IncrementScheduleFailedAttempts: cannot marshal update script: %v", err)
		return err
	}

	retries := 3
	req := esapi.UpdateRequest{
		Index:           constants.ElasticsearchBlogIndex,
		DocumentID:      blogId,
		Body:            strings.NewReader(string(bs)),
		RetryOnConflict: &retries,
	}

	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("IncrementScheduleFailedAttempts: update failed for blog %s: %v", blogId, err)
		return err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("IncrementScheduleFailedAttempts: error closing response body: %v", err)
		}
	}()

	if res.IsError() {
		err = fmt.Errorf("IncrementScheduleFailedAttempts: update failed for blog %s: %s", blogId, res.String())
		es.log.Error(err)
		return err
	}

	es.log.Warnf("IncrementScheduleFailedAttempts: incremented failed attempts for blog %s, reason: %s", blogId, reason)
	return nil
}

func (es *elasticsearchStorage) GetPublishedBlogByTagsName(ctx context.Context, tags ...string) (*pb.GetBlogsByTagsNameRes, error) {
	// Ensure at least one tag is provided
	if len(tags) == 0 {
		es.log.Error("GetPublishedBlogByTagsName: no tags provided")
		return nil, fmt.Errorf("at least one tag must be provided")
	}

	// Normalize tags to lowercase for case-insensitive search
	normalizedTags := make([]string, len(tags))
	for i, tag := range tags {
		normalizedTags[i] = strings.ToLower(strings.TrimSpace(tag))
	}

	// Build the query to search for published blogs by tags with the `is_archived` filtering
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"size": 100,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"bool": map[string]interface{}{
							"should": func() []map[string]interface{} {
								var shouldClauses []map[string]interface{}
								for _, tag := range normalizedTags {
									shouldClauses = append(shouldClauses, map[string]interface{}{
										"term": map[string]interface{}{
											"tags": map[string]interface{}{
												"value":            tag,
												"case_insensitive": true,
											},
										},
									})
								}
								return shouldClauses
							}(),
							"minimum_should_match": 1,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": false,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": false,
						},
					},
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "is_archived",
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetPublishedBlogByTagsName: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Print the query for debugging
	es.log.Infof("Executing query: %s", string(bs))

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetPublishedBlogByTagsName: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetPublishedBlogByTagsName: error closing response body, error: %v", err)
		}
	}()
	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetPublishedBlogByTagsName: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetPublishedBlogByTagsName: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetPublishedBlogByTagsName: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetPublishedBlogByTagsName: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Convert the hits to a slice of GetBlogsByTagsNameRes
	var blogs = &pb.GetBlogsByTagsNameRes{
		TheBlogs: make([]*pb.GetBlogsByTags, 0, len(hits)),
	}
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		hitBytes, err := json.Marshal(hitSource)
		if err != nil {
			es.log.Errorf("GetPublishedBlogByTagsName: error marshaling hit source, error: %v", err)
			continue
		}

		var blog pb.GetBlogsByTags
		if err := json.Unmarshal(hitBytes, &blog); err != nil {
			es.log.Errorf("GetPublishedBlogByTagsName: error unmarshaling hit to GetBlogsByTags, error: %v", err)
			continue
		}
		blogs.TheBlogs = append(blogs.TheBlogs, &blog)
	}

	es.log.Infof("GetPublishedBlogByTagsName: successfully fetched %d published blogs for tags: %v", len(blogs.TheBlogs), tags)
	return blogs, nil
}

func (es *elasticsearchStorage) GetPublishedBlogById(ctx context.Context, id string) (*pb.BlogByIdRes, error) {
	// Ensure id is not empty
	if id == "" {
		es.log.Error("GetPublishedBlogById: id is empty")
		return nil, fmt.Errorf("blog id cannot be empty")
	}

	// Build the query to search for a published blog by id
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"blog_id": id,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": false,
						},
					},
				},
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetPublishedBlogById: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Print the query for debugging
	es.log.Infof("Executing query: %s", string(bs))

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetPublishedBlogById: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetPublishedBlogById: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetPublishedBlogById: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetPublishedBlogById: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetPublishedBlogById: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok || len(hits) == 0 {
		es.log.Infof("GetPublishedBlogById: no published blog found with id: %s", id)
		return nil, nil
	}

	// Convert the first hit to GetBlogByIdRes
	hitSource := hits[0].(map[string]interface{})["_source"]
	hitBytes, err := json.Marshal(hitSource)
	if err != nil {
		es.log.Errorf("GetPublishedBlogById: error marshaling hit source, error: %v", err)
		return nil, err
	}

	var blog pb.BlogByIdRes
	if err := json.Unmarshal(hitBytes, &blog); err != nil {
		es.log.Errorf("GetPublishedBlogById: error unmarshaling hit to GetBlogByIdRes, error: %v", err)
		return nil, err
	}

	es.log.Infof("GetPublishedBlogById: successfully fetched published blog with id: %s", id)
	return &blog, nil
}

// AchieveAPublishedBlogById archives a published blog by setting an "is_archived" field to true
func (es *elasticsearchStorage) AchieveAPublishedBlogById(ctx context.Context, blogId string) (*esapi.Response, error) {
	// Ensure blogId is not empty
	if blogId == "" {
		es.log.Error("AchieveAPublishedBlogById: blogId is empty")
		return nil, fmt.Errorf("blogId cannot be empty")
	}

	// Build the update query to set is_archived to true
	updateScript := map[string]interface{}{
		"script": map[string]interface{}{
			"source": "ctx._source.is_archived = true",
		},
	}

	// Marshal the update script to JSON
	bs, err := json.Marshal(updateScript)
	if err != nil {
		es.log.Errorf("AchieveAPublishedBlogById: cannot marshal the update script, error: %v", err)
		return nil, err
	}

	// Create an update request with retry_on_conflict for concurrent write safety
	retries := 3
	req := esapi.UpdateRequest{
		Index:           constants.ElasticsearchBlogIndex,
		DocumentID:      blogId,
		Body:            strings.NewReader(string(bs)),
		RetryOnConflict: &retries,
	}

	// Execute the update request
	updateResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("AchieveAPublishedBlogById: error executing update request, error: %+v", err)
		return updateResponse, err
	}
	defer func() {
		if err := updateResponse.Body.Close(); err != nil {
			es.log.Errorf("AchieveAPublishedBlogById: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if updateResponse.IsError() {
		err = fmt.Errorf("AchieveAPublishedBlogById: update query failed, response: %+v", updateResponse)
		es.log.Error(err)
		return updateResponse, err
	}

	es.log.Infof("AchieveAPublishedBlogById: successfully archived blog with id: %s", blogId)
	return updateResponse, nil
}

// DeleteABlogById deletes a blog by its ID
func (es *elasticsearchStorage) DeleteABlogById(ctx context.Context, blogId string) (*esapi.Response, error) {
	// Ensure blogId is not empty
	if blogId == "" {
		es.log.Error("DeleteABlogById: blogId is empty")
		return nil, fmt.Errorf("blogId cannot be empty")
	}

	// Create a Delete request
	req := esapi.DeleteRequest{
		Index:      constants.ElasticsearchBlogIndex,
		DocumentID: blogId,
	}

	// Execute the delete request
	deleteResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("DeleteABlogById: error executing delete request, error: %+v", err)
		return deleteResponse, err
	}
	defer func() {
		if err := deleteResponse.Body.Close(); err != nil {
			es.log.Errorf("DeleteABlogById: error closing response body, error: %v", err)
		}
	}()

	// 404: document already gone — still a successful delete so callers can
	// fan out BLOG_DELETE to Postgres and storage (orphan / replay).
	if deleteResponse.StatusCode == http.StatusNotFound {
		es.log.Infof("DeleteABlogById: blog %s not in ES (already gone)", blogId)
		return deleteResponse, nil
	}
	if deleteResponse.IsError() {
		err = fmt.Errorf("DeleteABlogById: delete query failed, response: %+v", deleteResponse)
		es.log.Error(err)
		return deleteResponse, err
	}

	es.log.Infof("DeleteABlogById: successfully deleted blog with id: %s", blogId)
	return deleteResponse, nil
}

// GetLast100BlogsLatestFirst retrieves the last 100 blogs sorted by the latest first
func (es *elasticsearchStorage) GetLast100BlogsLatestFirst(ctx context.Context) (*pb.GetBlogsByTagsNameRes, error) {
	// Build the query to retrieve the last 100 blogs, sorted by published_time in descending order
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"size": 100,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_draft": false,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": false,
						},
					},
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "is_archived",
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetLast100BlogsLatestFirst: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetLast100BlogsLatestFirst: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetLast100BlogsLatestFirst: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetLast100BlogsLatestFirst: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetLast100BlogsLatestFirst: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetLast100BlogsLatestFirst: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetLast100BlogsLatestFirst: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Convert the hits to a slice of GetBlogs
	var blogs = &pb.GetBlogsByTagsNameRes{
		TheBlogs: make([]*pb.GetBlogsByTags, 0, len(hits)),
	}
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		hitBytes, err := json.Marshal(hitSource)
		if err != nil {
			es.log.Errorf("GetLast100BlogsLatestFirst: error marshaling hit source, error: %v", err)
			continue
		}

		var blog pb.GetBlogsByTags
		if err := json.Unmarshal(hitBytes, &blog); err != nil {
			es.log.Errorf("GetLast100BlogsLatestFirst: error unmarshaling hit to GetBlogsByTags, error: %v", err)
			continue
		}
		blogs.TheBlogs = append(blogs.TheBlogs, &blog)
	}

	es.log.Infof("GetLast100BlogsLatestFirst: successfully fetched last 100 blogs sorted by latest first")
	return blogs, nil
}

func (es *elasticsearchStorage) GetDraftedBlogByIdAndOwner(ctx context.Context, blogId, ownerAccountId string) (*pb.BlogByIdRes, error) {
	// Ensure blogId and ownerAccountId are not empty
	if blogId == "" || ownerAccountId == "" {
		es.log.Error("GetDraftedBlogByIdAndOwner: blogId or ownerAccountId is empty")
		return nil, fmt.Errorf("blog id and owner account id cannot be empty")
	}

	// Build the query to search for a drafted blog by blog_id and owner_account_id
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"blog_id": blogId,
						},
					},
					{
						"term": map[string]interface{}{
							"owner_account_id": ownerAccountId,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": true,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": false,
						},
					},
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "is_archived",
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetDraftedBlogByIdAndOwner: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Print the query for debugging
	es.log.Infof("Executing query: %s", string(bs))

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetDraftedBlogByIdAndOwner: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetDraftedBlogByIdAndOwner: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetDraftedBlogByIdAndOwner: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetDraftedBlogByIdAndOwner: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetDraftedBlogByIdAndOwner: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok || len(hits) == 0 {
		es.log.Infof("GetDraftedBlogByIdAndOwner: no drafted blog found with blogId: %s and ownerAccountId: %s", blogId, ownerAccountId)
		return nil, nil
	}

	// Convert the first hit to GetBlogByIdRes
	hitSource := hits[0].(map[string]interface{})["_source"]
	hitBytes, err := json.Marshal(hitSource)
	if err != nil {
		es.log.Errorf("GetDraftedBlogByIdAndOwner: error marshaling hit source, error: %v", err)
		return nil, err
	}

	var blog pb.BlogByIdRes
	if err := json.Unmarshal(hitBytes, &blog); err != nil {
		es.log.Errorf("GetDraftedBlogByIdAndOwner: error unmarshaling hit to GetBlogByIdRes, error: %v", err)
		return nil, err
	}

	es.log.Infof("GetDraftedBlogByIdAndOwner: successfully fetched drafted blog with blogId: %s and ownerAccountId: %s", blogId, ownerAccountId)
	return &blog, nil
}

func (es *elasticsearchStorage) GetPublishedBlogByIdAndOwner(ctx context.Context, blogId, ownerAccountId string) (*pb.BlogByIdRes, error) {
	// Ensure blogId and ownerAccountId are not empty
	if blogId == "" || ownerAccountId == "" {
		es.log.Error("GetPublishedBlogByIdAndOwner: blogId or ownerAccountId is empty")
		return nil, fmt.Errorf("blog id and owner account id cannot be empty")
	}

	// Build the query to search for a published blog by blog_id and owner_account_id
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"blog_id": blogId,
						},
					},
					{
						"term": map[string]interface{}{
							"owner_account_id": ownerAccountId,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": false,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": false,
						},
					},
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "is_archived",
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetPublishedBlogByIdAndOwner: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Print the query for debugging
	es.log.Infof("Executing query: %s", string(bs))

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetPublishedBlogByIdAndOwner: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetPublishedBlogByIdAndOwner: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetPublishedBlogByIdAndOwner: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetPublishedBlogByIdAndOwner: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetPublishedBlogByIdAndOwner: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok || len(hits) == 0 {
		es.log.Infof("GetPublishedBlogByIdAndOwner: no published blog found with blogId: %s and ownerAccountId: %s", blogId, ownerAccountId)
		return nil, nil
	}

	// Convert the first hit to GetBlogByIdRes
	hitSource := hits[0].(map[string]interface{})["_source"]
	hitBytes, err := json.Marshal(hitSource)
	if err != nil {
		es.log.Errorf("GetPublishedBlogByIdAndOwner: error marshaling hit source, error: %v", err)
		return nil, err
	}

	var blog pb.BlogByIdRes
	if err := json.Unmarshal(hitBytes, &blog); err != nil {
		es.log.Errorf("GetPublishedBlogByIdAndOwner: error unmarshaling hit to GetBlogByIdRes, error: %v", err)
		return nil, err
	}

	es.log.Infof("GetPublishedBlogByIdAndOwner: successfully fetched published blog with blogId: %s and ownerAccountId: %s", blogId, ownerAccountId)
	return &blog, nil
}

func (es *elasticsearchStorage) GetBlogsByBlogIds(ctx context.Context, blogIds []string) (*pb.GetBlogsRes, error) {
	// Ensure blogIds is not empty
	if len(blogIds) == 0 {
		es.log.Error("GetBlogsByBlogIds: blogIds array is empty")
		return nil, fmt.Errorf("blogIds array cannot be empty")
	}

	// Build the query to search for blogs by blog_id and sort by published_time in descending order (latest first)
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"terms": map[string]interface{}{
				"blog_id": blogIds,
			},
		},
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetBlogsByBlogIds: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Print the query for debugging
	es.log.Infof("Executing query: %s", string(bs))

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetBlogsByBlogIds: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetBlogsByBlogIds: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetBlogsByBlogIds: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetBlogsByBlogIds: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetBlogsByBlogIds: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok || len(hits) == 0 {
		es.log.Infof("GetBlogsByBlogIds: no blogs found for provided blogIds")
		return nil, nil
	}

	// Initialize a response structure to hold multiple blogs
	var blogs = &pb.GetBlogsRes{
		Blogs: make([]*pb.GetBlogs, 0, len(hits)),
	}

	// Iterate over the hits and convert them to GetBlogs
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		hitBytes, err := json.Marshal(hitSource)
		if err != nil {
			es.log.Errorf("GetBlogsByBlogIds: error marshaling hit source, error: %v", err)
			return nil, err
		}

		var blog pb.GetBlogs
		if err := json.Unmarshal(hitBytes, &blog); err != nil {
			es.log.Errorf("GetBlogsByBlogIds: error unmarshaling hit to GetBlogs, error: %v", err)
			return nil, err
		}

		blogs.Blogs = append(blogs.Blogs, &blog)
	}

	es.log.Infof("GetBlogsByBlogIds: successfully fetched %d blogs for provided blogIds", len(blogs.Blogs))
	return blogs, nil
}

func (es *elasticsearchStorage) GetDraftBlogByBlogId(ctx context.Context, blogId string) (*pb.BlogByIdRes, error) {
	// Ensure blogId is not empty
	if blogId == "" {
		es.log.Error("GetDraftedBlogById: blogId is empty")
		return nil, fmt.Errorf("blog id cannot be empty")
	}

	// Build the query to search for a drafted blog by blog_id
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"blog_id": blogId,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": true,
						},
					},
				},
				"must_not": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": true,
						},
					},
				},
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"is_archived": false,
						},
					},
					{
						"bool": map[string]interface{}{
							"must_not": map[string]interface{}{
								"exists": map[string]interface{}{
									"field": "is_archived",
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetDraftedBlogById: cannot marshal the query, error: %v", err)
		return nil, err
	}

	// Print the query for debugging
	es.log.Infof("Executing query: %s", string(bs))

	// Create a new search request with the query
	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	// Execute the search request
	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetDraftedBlogById: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetDraftedBlogById: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetDraftedBlogById: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetDraftedBlogById: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetDraftedBlogById: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok || len(hits) == 0 {
		es.log.Infof("GetDraftedBlogById: no drafted blog found with blogId: %s", blogId)
		return nil, nil
	}

	// Convert the first hit to GetBlogByIdRes
	hitSource := hits[0].(map[string]interface{})["_source"]
	hitBytes, err := json.Marshal(hitSource)
	if err != nil {
		es.log.Errorf("GetDraftedBlogById: error marshaling hit source, error: %v", err)
		return nil, err
	}

	var blog pb.BlogByIdRes
	if err := json.Unmarshal(hitBytes, &blog); err != nil {
		es.log.Errorf("GetDraftedBlogById: error unmarshaling hit to GetBlogByIdRes, error: %v", err)
		return nil, err
	}

	es.log.Infof("GetDraftedBlogById: successfully fetched drafted blog with blogId: %s", blogId)
	return &blog, nil
}
