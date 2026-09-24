package database

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/constants"
	"github.com/the-monkeys/the_monkeys/searchdoc"
)

func (es *elasticsearchStorage) SaveBlog(ctx context.Context, blog map[string]interface{}) (*esapi.Response, error) {
	blogId, _ := blog["blog_id"].(string)

	// Denormalise editorjs blocks into the flat search-v2 fields
	// (title/summary/body/tags). This runs on every write so the v3
	// alias receives a self-contained, search-ready document without
	// any cross-shard joins at query time.
	blog = searchdoc.Apply(blog)

	bs, err := json.Marshal(blog)
	if err != nil {
		es.log.Errorf("SaveBlog: cannot marshal the blog %s, error: %v", blogId, err)
		return nil, err
	}

	jsonStr := string(bs)
	document := strings.NewReader(jsonStr)

	req := esapi.IndexRequest{
		Index:      constants.ElasticsearchBlogIndex,
		DocumentID: blogId,
		Body:       document,
	}

	insertResponse, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("SaveBlog: error while indexing blog %s, error: %+v", blogId, err)
		return insertResponse, err
	}

	if insertResponse.IsError() {
		err = fmt.Errorf("SaveBlog: error indexing blog %s, response: %+v", blogId, insertResponse)
		es.log.Error(err)
		return insertResponse, err
	}

	es.log.Infof("SaveBlog: successfully indexed blog %s", blogId)
	return insertResponse, nil
}

func (es *elasticsearchStorage) GetBlogsOfUsersByAccountIds(ctx context.Context, accountIds []string, limit, offset int32) ([]map[string]interface{}, error) {
	// Ensure accountIds is not empty
	if len(accountIds) == 0 {
		es.log.Error("GetBlogsOfUsersByAccountIds: accountIds array is empty")
		return nil, fmt.Errorf("accountIds array cannot be empty")
	}

	// Build the query to get published blogs, not archived, owned by specific account IDs
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date", // Use this to handle missing fields
				},
			},
		},
		"from": offset,
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"terms": map[string]interface{}{
							"owner_account_id": accountIds,
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
					audience.PublicListMustNot(),
				},
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetBlogsOfUsersByAccountIds: cannot marshal the query, error: %v", err)
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
		es.log.Errorf("GetBlogsOfUsersByAccountIds: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetBlogsOfUsersByAccountIds: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetBlogsOfUsersByAccountIds: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetBlogsOfUsersByAccountIds: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetBlogsOfUsersByAccountIds: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetBlogsOfUsersByAccountIds: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Prepare the result
	blogs := make([]map[string]interface{}, 0, len(hits))
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		blog, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetBlogsOfUsersByAccountIds: failed to cast hit source to map")
			continue
		}
		blogs = append(blogs, blog)
	}

	es.log.Debug("GetBlogsOfUsersByAccountIds: blog retrieval operation completed")
	return blogs, nil
}

func (es *elasticsearchStorage) GetBlogsByTagsAccId(ctx context.Context, accountId string, tags []string, isDraft bool, limit, offset int32) ([]map[string]interface{}, error) {
	// Ensure accountId and tags are not empty
	if accountId == "" {
		es.log.Error("GetBlogsByTags: accountId is empty")
		return nil, fmt.Errorf("accountId cannot be empty")
	}
	if len(tags) == 0 {
		es.log.Error("GetBlogsByTags: tags array is empty")
		return nil, fmt.Errorf("tags array cannot be empty")
	}

	// Normalize tags to lowercase for case-insensitive search
	normalizedTags := make([]string, len(tags))
	for i, tag := range tags {
		normalizedTags[i] = strings.ToLower(strings.TrimSpace(tag))
	}

	// Build the query to get blogs by tags with sorting by latest first
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"from": offset,
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"owner_account_id": accountId,
						},
					},
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
							"is_draft": isDraft,
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
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetBlogsByTags: cannot marshal the query, error: %v", err)
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
		es.log.Errorf("GetBlogsByTags: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetBlogsByTags: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetBlogsByTags: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetBlogsByTags: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetBlogsByTags: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetBlogsByTags: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Prepare the result
	blogs := make([]map[string]interface{}, 0, len(hits))
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		blog, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetBlogsByTags: failed to cast hit source to map")
			continue
		}
		blogs = append(blogs, blog)
	}

	es.log.Debug("GetBlogsByTags: blog retrieval by tags completed")
	return blogs, nil
}

func (es *elasticsearchStorage) GetBlogsByAccountId(ctx context.Context, accountId string, isDraft bool, excludeGroupOnly bool, limit, offset int32) ([]map[string]interface{}, error) {
	// Ensure accountId is not empty
	if accountId == "" {
		es.log.Error("GetBlogsByAccountId: accountId is empty")
		return nil, fmt.Errorf("accountId cannot be empty")
	}

	mustNot := []map[string]interface{}{
		{
			"term": map[string]interface{}{
				"is_archived": true,
			},
		},
	}
	if !isDraft && excludeGroupOnly {
		mustNot = audience.AppendPublicListMustNot(mustNot)
	}

	// Build the query to get blogs by accountId, filtered by isDraft, with sorting by latest first
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"from": offset,
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"owner_account_id": accountId,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": isDraft,
						},
					},
				},
				"must_not": mustNot,
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetBlogsByAccountId: cannot marshal the query, error: %v", err)
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
		es.log.Errorf("GetBlogsByAccountId: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetBlogsByAccountId: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetBlogsByAccountId: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetBlogsByAccountId: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetBlogsByAccountId: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetBlogsByAccountId: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Prepare the result
	blogs := make([]map[string]interface{}, 0, len(hits))
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		blog, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetBlogsByAccountId: failed to cast hit source to map")
			continue
		}
		blogs = append(blogs, blog)
	}

	es.log.Debug("GetBlogsByAccountId: account-specific blog retrieval completed")
	return blogs, nil
}

func (es *elasticsearchStorage) GetBlogByBlogId(ctx context.Context, blogId string, isDraft bool) (map[string]interface{}, error) {
	// Ensure blogId is not empty
	if blogId == "" {
		es.log.Error("GetBlogByBlogId: blogId is empty")
		return nil, fmt.Errorf("blogId cannot be empty")
	}

	// Build the query to fetch the blog by blogId
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
							"is_draft": isDraft,
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
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetBlogByBlogId: cannot marshal the query, error: %v", err)
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
		es.log.Errorf("GetBlogByBlogId: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetBlogByBlogId: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetBlogByBlogId: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetBlogByBlogId: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetBlogByBlogId: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok || len(hits) == 0 {
		es.log.Debug("GetBlogByBlogId: no blog found matching criteria")
		return nil, nil
	}

	// Return the first blog
	hitSource := hits[0].(map[string]interface{})["_source"]
	blog, ok := hitSource.(map[string]interface{})
	if !ok {
		es.log.Errorf("GetBlogByBlogId: failed to cast hit source to map")
		return nil, fmt.Errorf("failed to cast hit source to map")
	}

	es.log.Debug("GetBlogByBlogId: blog retrieval completed successfully")
	return blog, nil
}

func (es *elasticsearchStorage) GetABlogByBlogIdAccId(ctx context.Context, blogId, accountId string, isDraft bool) (map[string]interface{}, error) {
	// Ensure blogId and accountId are not empty
	if blogId == "" {
		es.log.Error("GetABlogByBlogIdAccId: blogId is empty")
		return nil, fmt.Errorf("blogId cannot be empty")
	}
	if accountId == "" {
		es.log.Error("GetABlogByBlogIdAccId: accountId is empty")
		return nil, fmt.Errorf("accountId cannot be empty")
	}

	// Build the query to fetch the blog by blogId and accountId
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
							"owner_account_id": accountId,
						},
					},
					{
						"term": map[string]interface{}{
							"is_draft": isDraft,
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
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetABlogByBlogIdAccId: cannot marshal the query, error: %v", err)
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
		es.log.Errorf("GetABlogByBlogIdAccId: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetABlogByBlogIdAccId: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetABlogByBlogIdAccId: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetABlogByBlogIdAccId: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetABlogByBlogIdAccId: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok || len(hits) == 0 {
		es.log.Debug("GetABlogByBlogIdAccId: no blog found matching criteria")
		return nil, nil
	}

	// Return the first blog
	hitSource := hits[0].(map[string]interface{})["_source"]
	blog, ok := hitSource.(map[string]interface{})
	if !ok {
		es.log.Errorf("GetABlogByBlogIdAccId: failed to cast hit source to map")
		return nil, fmt.Errorf("failed to cast hit source to map")
	}

	es.log.Debug("GetABlogByBlogIdAccId: blog retrieval completed successfully")
	return blog, nil
}

func (es *elasticsearchStorage) GetBlogsByTags(ctx context.Context, tags []string, isDraft bool, limit, offset int32) ([]map[string]interface{}, error) {
	// Ensure tags are not empty
	if len(tags) == 0 {
		es.log.Error("GetBlogsByTagsWithoutAccId: tags array is empty")
		return nil, fmt.Errorf("tags array cannot be empty")
	}

	// Normalize tags to lowercase for case-insensitive search
	normalizedTags := make([]string, len(tags))
	for i, tag := range tags {
		normalizedTags[i] = strings.ToLower(strings.TrimSpace(tag))
	}

	// Build the query to get blogs by tags with sorting by latest first
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"from": offset,
		"size": limit,
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
							"is_draft": isDraft,
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
	}
	if !isDraft {
		boolQuery := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
		mustNot := boolQuery["must_not"].([]map[string]interface{})
		boolQuery["must_not"] = audience.AppendPublicListMustNot(mustNot)
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetBlogsByTagsWithoutAccId: cannot marshal the query, error: %v", err)
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
		es.log.Errorf("GetBlogsByTagsWithoutAccId: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetBlogsByTagsWithoutAccId: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetBlogsByTagsWithoutAccId: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetBlogsByTagsWithoutAccId: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetBlogsByTagsWithoutAccId: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetBlogsByTagsWithoutAccId: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Prepare the result
	blogs := make([]map[string]interface{}, 0, len(hits))
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		blog, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetBlogsByTagsWithoutAccId: failed to cast hit source to map")
			continue
		}
		blogs = append(blogs, blog)
	}

	es.log.Debug("GetBlogsByTagsWithoutAccId: tag-based blog retrieval completed")
	return blogs, nil
}

func (es *elasticsearchStorage) GetBlogsByBlogIdsV2(ctx context.Context, blogIds []string, limit, offset int32) ([]map[string]interface{}, error) {
	// Ensure blogIds is not empty
	if len(blogIds) == 0 {
		es.log.Error("GetBlogsByBlogIds: blogIds array is empty")
		return nil, fmt.Errorf("blogIds array cannot be empty")
	}

	// Build the query to get blogs by blogIds with sorting by latest first
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date",
				},
			},
		},
		"from": offset,
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"terms": map[string]interface{}{
							"blog_id": blogIds,
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
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetBlogsByBlogIds: cannot marshal the query, error: %v", err)
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
	if !ok {
		err := fmt.Errorf("GetBlogsByBlogIds: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Prepare the result
	blogs := make([]map[string]interface{}, 0, len(hits))
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		blog, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetBlogsByBlogIds: failed to cast hit source to map")
			continue
		}
		blogs = append(blogs, blog)
	}

	es.log.Debug("GetBlogsByBlogIds: blog ID-based retrieval completed")
	return blogs, nil
}

func (es *elasticsearchStorage) GetAllPublishedBlogsLatestFirst(ctx context.Context, limit, offset int) ([]map[string]interface{}, error) {
	// Build the query to get all published blogs sorted by published_time
	query := map[string]interface{}{
		"sort": []map[string]interface{}{
			{
				"published_time": map[string]interface{}{
					"order":         "desc",
					"unmapped_type": "date", // Handle cases where published_time is missing
				},
			},
		},
		"from": offset,
		"size": limit,
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
					{
						// Exclude scheduled blogs; docs without is_scheduled are treated as non-scheduled
						"term": map[string]interface{}{
							"is_scheduled": true,
						},
					},
					audience.PublicListMustNot(),
				},
			},
		},
	}

	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetAllPublishedBlogsLatestFirst: cannot marshal the query, error: %v", err)
		return nil, err
	}

	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetAllPublishedBlogsLatestFirst: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetAllPublishedBlogsLatestFirst: error closing response body, error: %v", err)
		}
	}()

	if res.IsError() {
		err = fmt.Errorf("GetAllPublishedBlogsLatestFirst: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetAllPublishedBlogsLatestFirst: error reading response body, error: %v", err)
		return nil, err
	}

	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetAllPublishedBlogsLatestFirst: error decoding response body, error: %v", err)
		return nil, err
	}

	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetAllPublishedBlogsLatestFirst: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	blogs := make([]map[string]interface{}, 0, len(hits))
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		blog, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetAllPublishedBlogsLatestFirst: failed to cast hit source to map")
			continue
		}
		blogs = append(blogs, blog)
	}

	es.log.Debug("GetAllPublishedBlogsLatestFirst: published blog retrieval completed")
	return blogs, nil
}

func (es *elasticsearchStorage) GetAllTagsFromUserPublishedBlogs(ctx context.Context, accountID string) ([]string, error) {
	// Ensure accountID is not empty
	if accountID == "" {
		es.log.Error("GetAllTagsFromUserPublishedBlogs: accountID is empty")
		return nil, fmt.Errorf("accountID cannot be empty")
	}

	// Build the query to get all published blogs by accountID and fetch only tags field
	query := map[string]interface{}{
		"_source": []string{"tags"}, // Only fetch tags field for performance
		"size":    10000,            // Get a large number of blogs to fetch all tags
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"owner_account_id": accountID,
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
					audience.PublicListMustNot(),
				},
			},
		},
	}

	// Marshal the query to JSON
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetAllTagsFromUserPublishedBlogs: cannot marshal the query, error: %v", err)
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
		es.log.Errorf("GetAllTagsFromUserPublishedBlogs: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetAllTagsFromUserPublishedBlogs: error closing response body, error: %v", err)
		}
	}()

	// Check if the response indicates an error
	if res.IsError() {
		err = fmt.Errorf("GetAllTagsFromUserPublishedBlogs: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	// Read the response body
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetAllTagsFromUserPublishedBlogs: error reading response body, error: %v", err)
		return nil, err
	}

	// Parse the response body
	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetAllTagsFromUserPublishedBlogs: error decoding response body, error: %v", err)
		return nil, err
	}

	// Extract the hits from the response
	hits, ok := esResponse["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		err := fmt.Errorf("GetAllTagsFromUserPublishedBlogs: failed to parse hits from response")
		es.log.Error(err)
		return nil, err
	}

	// Collect all tags from all blogs (including duplicates)
	var allTags []string
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		source, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetAllTagsFromUserPublishedBlogs: failed to cast hit source to map")
			continue
		}

		// Extract tags field
		if tagsInterface, exists := source["tags"]; exists {
			if tagsArray, ok := tagsInterface.([]interface{}); ok {
				// Convert interface{} slice to string slice
				for _, tag := range tagsArray {
					if tagStr, ok := tag.(string); ok {
						allTags = append(allTags, tagStr)
					}
				}
			}
		}
	}

	es.log.Debug("GetAllTagsFromUserPublishedBlogs: tag extraction operation completed")
	return allTags, nil
}
