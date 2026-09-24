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
)

// groupSlugTerm matches an exact group slug. group_slug is dynamically
// mapped as text + keyword; term on the text field tokenizes hyphens
// ("monkeys-admin-…") and returns zero hits.
func groupSlugTerm(slug string) map[string]interface{} {
	return map[string]interface{}{
		"term": map[string]interface{}{"group_slug.keyword": slug},
	}
}

func publishedBlogsByGroupSlugQuery(slug string, includeGroupOnly bool, limit, offset int32) map[string]interface{} {
	must := []map[string]interface{}{
		groupSlugTerm(slug),
		{"term": map[string]interface{}{"is_draft": false}},
	}
	mustNot := []map[string]interface{}{
		{"term": map[string]interface{}{"is_archived": true}},
	}
	if !includeGroupOnly {
		mustNot = audience.AppendPublicListMustNot(mustNot)
	}
	return map[string]interface{}{
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
				"must":     must,
				"must_not": mustNot,
			},
		},
	}
}

func (es *elasticsearchStorage) GetPublishedBlogsByGroupSlug(ctx context.Context, slug string, includeGroupOnly bool, limit, offset int32) ([]map[string]interface{}, error) {
	if slug == "" {
		return nil, fmt.Errorf("group slug cannot be empty")
	}

	query := publishedBlogsByGroupSlugQuery(slug, includeGroupOnly, limit, offset)
	bs, err := json.Marshal(query)
	if err != nil {
		es.log.Errorf("GetPublishedBlogsByGroupSlug: cannot marshal the query, error: %v", err)
		return nil, err
	}

	req := esapi.SearchRequest{
		Index: []string{constants.ElasticsearchBlogIndex},
		Body:  strings.NewReader(string(bs)),
	}

	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("GetPublishedBlogsByGroupSlug: error executing search request, error: %+v", err)
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("GetPublishedBlogsByGroupSlug: error closing response body, error: %v", err)
		}
	}()

	if res.IsError() {
		err = fmt.Errorf("GetPublishedBlogsByGroupSlug: search query failed, response: %+v", res)
		es.log.Error(err)
		return nil, err
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		es.log.Errorf("GetPublishedBlogsByGroupSlug: error reading response body, error: %v", err)
		return nil, err
	}

	var esResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &esResponse); err != nil {
		es.log.Errorf("GetPublishedBlogsByGroupSlug: error decoding response body, error: %v", err)
		return nil, err
	}

	hitsMap, ok := esResponse["hits"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("GetPublishedBlogsByGroupSlug: failed to parse hits from response")
	}
	hits, ok := hitsMap["hits"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("GetPublishedBlogsByGroupSlug: failed to parse hits from response")
	}

	blogs := make([]map[string]interface{}, 0, len(hits))
	for _, hit := range hits {
		hitSource := hit.(map[string]interface{})["_source"]
		blog, ok := hitSource.(map[string]interface{})
		if !ok {
			es.log.Errorf("GetPublishedBlogsByGroupSlug: failed to cast hit source to map")
			continue
		}
		blogs = append(blogs, blog)
	}
	return blogs, nil
}

func detachBlogsFromGroupQuery(slug string) map[string]interface{} {
	return map[string]interface{}{
		"query": groupSlugTerm(slug),
		"script": map[string]interface{}{
			"source": "ctx._source.remove('group_slug'); ctx._source.remove('group_id');",
			"lang":   "painless",
		},
	}
}

func (es *elasticsearchStorage) DetachBlogsFromGroup(ctx context.Context, groupSlug string) error {
	if groupSlug == "" {
		return fmt.Errorf("group slug cannot be empty")
	}

	bs, err := json.Marshal(detachBlogsFromGroupQuery(groupSlug))
	if err != nil {
		es.log.Errorf("DetachBlogsFromGroup: cannot marshal the query, error: %v", err)
		return err
	}

	refresh := true
	req := esapi.UpdateByQueryRequest{
		Index:     []string{constants.ElasticsearchBlogIndex},
		Body:      strings.NewReader(string(bs)),
		Conflicts: "proceed",
		Refresh:   &refresh,
	}

	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("DetachBlogsFromGroup: error executing update_by_query, error: %+v", err)
		return err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("DetachBlogsFromGroup: error closing response body, error: %v", err)
		}
	}()

	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		err = fmt.Errorf("DetachBlogsFromGroup: update_by_query failed: %s", strings.TrimSpace(string(body)))
		es.log.Error(err)
		return err
	}

	es.log.Infow("DetachBlogsFromGroup: stripped group_slug from blogs", "group_slug", groupSlug)
	return nil
}

func coerceGroupBlogsToGroupOnlyQuery(slug string) map[string]interface{} {
	return map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					groupSlugTerm(slug),
				},
				"must_not": audience.AppendPublicListMustNot(nil),
			},
		},
		"script": map[string]interface{}{
			"source": "ctx._source.audience = params.audience;",
			"lang":   "painless",
			"params": map[string]interface{}{
				"audience": audience.AudienceGroupOnly,
			},
		},
	}
}

func (es *elasticsearchStorage) CoerceBlogsToGroupOnly(ctx context.Context, groupSlug string) error {
	if groupSlug == "" {
		return fmt.Errorf("group slug cannot be empty")
	}

	bs, err := json.Marshal(coerceGroupBlogsToGroupOnlyQuery(groupSlug))
	if err != nil {
		es.log.Errorf("CoerceBlogsToGroupOnly: cannot marshal the query, error: %v", err)
		return err
	}

	refresh := true
	req := esapi.UpdateByQueryRequest{
		Index:     []string{constants.ElasticsearchBlogIndex},
		Body:      strings.NewReader(string(bs)),
		Conflicts: "proceed",
		Refresh:   &refresh,
	}

	res, err := req.Do(ctx, es.client)
	if err != nil {
		es.log.Errorf("CoerceBlogsToGroupOnly: error executing update_by_query, error: %+v", err)
		return err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			es.log.Errorf("CoerceBlogsToGroupOnly: error closing response body, error: %v", err)
		}
	}()

	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		err = fmt.Errorf("CoerceBlogsToGroupOnly: update_by_query failed: %s", strings.TrimSpace(string(body)))
		es.log.Error(err)
		return err
	}

	es.log.Infow("CoerceBlogsToGroupOnly: set audience to group_only", "group_slug", groupSlug)
	return nil
}
