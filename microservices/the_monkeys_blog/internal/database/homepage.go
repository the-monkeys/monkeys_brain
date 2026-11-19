package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"github.com/the-monkeys/the_monkeys/common/homepage"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/constants"
)

const (
	homepageBatch            = 100
	homepageMaxCandidates    = 100000
	homepageMaxIDs           = 10000
	homepageMaxPageBytes     = 32 << 20
	homepageMaxRetainedBytes = 32 << 20
)

type homepageRankedRow struct {
	rank  int
	row   map[string]interface{}
	bytes int
}

// GetHomepageBlogs is an optional storage capability. The ordinary metadata
// contract and indexing paths deliberately do not depend on homepage policy.
// Empty IDs select latest; callers must handle an empty trending result themselves.
func (es *elasticsearchStorage) GetHomepageBlogs(ctx context.Context, limit, offset int, blogIDs []string) (rows []map[string]interface{}, more bool, err error) {
	if limit < 1 || limit > 50 || offset < 0 || offset > 1000 || len(blogIDs) > homepageMaxIDs {
		return nil, false, fmt.Errorf("invalid homepage pagination")
	}
	ranks := make(map[string]int, len(blogIDs))
	for i, id := range blogIDs {
		if strings.TrimSpace(id) == "" {
			return nil, false, fmt.Errorf("invalid homepage article ID")
		}
		if _, ok := ranks[id]; !ok {
			ranks[id] = i
		}
	}
	ctx, cancel := context.WithTimeout(ctx, publicCatalogTraversalTimeout)
	defer cancel()
	scrollID := ""
	defer func() {
		if scrollID != "" {
			err = errors.Join(err, es.clearPublicCatalogScroll(ctx, scrollID))
		}
		err = errors.Join(err, ctx.Err())
		if err != nil {
			rows = nil
			more = false
		}
	}()
	must := []map[string]interface{}{{"term": map[string]interface{}{"is_draft": false}}}
	if len(blogIDs) > 0 {
		// The source index historically mapped blog_id as analyzed text. Its
		// document _id is the blog ID on every write, so IDs queries remain exact
		// without depending on a keyword subfield or a mapping migration.
		must = append(must, map[string]interface{}{"ids": map[string]interface{}{"values": blogIDs}})
	}
	query := map[string]interface{}{
		"size": homepageBatch, "track_total_hits": true,
		// Full _source includes stored EditorJS and legacy title/body/cover/topic
		// fields, plus all visibility fields. Never assess projected metadata.
		"query": map[string]interface{}{"bool": map[string]interface{}{
			"must": must, "must_not": audience.AppendPublicListMustNot([]map[string]interface{}{
				{"term": map[string]interface{}{"is_archived": true}},
				{"term": map[string]interface{}{"is_scheduled": true}},
			}),
		}},
		"sort": []map[string]interface{}{{"published_time": map[string]interface{}{"order": "desc", "unmapped_type": "date"}}, {"_doc": "asc"}},
	}
	if len(blogIDs) > 0 {
		query["sort"] = []string{"_doc"}
	}
	body, _ := json.Marshal(query)
	request := esapi.SearchRequest{Index: []string{constants.ElasticsearchBlogIndex}, Body: bytes.NewReader(body), Scroll: publicCatalogKeepAlive}
	response, err := es.homepageScrollPage(ctx, request, &scrollID)
	if err != nil {
		return nil, false, err
	}
	hits, total, err := publicCatalogHits(response)
	if err != nil {
		return nil, false, err
	}
	selected := make([]homepageRankedRow, 0, offset+limit+1)
	rows = make([]map[string]interface{}, 0, limit)
	seenIDs := make(map[string]bool)
	eligible, scanned, retainedBytes := 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if id, _ := response["_scroll_id"].(string); id == "" {
			return nil, false, fmt.Errorf("homepage scroll is missing its context")
		}
		if len(hits) != min(homepageBatch, total-scanned) {
			return nil, false, fmt.Errorf("homepage scroll returned an incomplete batch")
		}
		for _, raw := range hits {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			hit, _ := raw.(map[string]interface{})
			source, _ := hit["_source"].(map[string]interface{})
			id, _ := source["blog_id"].(string)
			if strings.TrimSpace(id) == "" || seenIDs[id] {
				continue
			}
			seenIDs[id] = true
			rank, requested := ranks[id]
			if len(blogIDs) > 0 && !requested {
				continue
			}
			assessment := homepage.Assess(source)
			if !assessment.Eligible {
				continue
			}
			eligible++
			if len(blogIDs) == 0 && eligible <= offset {
				continue
			}
			row := publicCatalogMetadata(source)
			row["title"] = assessment.Title
			row["first_image"] = assessment.CoverImage
			row["tags"] = assessment.Topics
			row["homepage_eligibility"] = assessment
			encoded, _ := json.Marshal(row)
			if len(blogIDs) == 0 {
				retainedBytes += len(encoded)
				if retainedBytes > homepageMaxRetainedBytes {
					return nil, false, fmt.Errorf("homepage metadata exceeds traversal budget")
				}
				if len(rows) == limit {
					return rows, true, nil
				}
				rows = append(rows, row)
			} else {
				// Retain only the highest-ranked eligible prefix needed for this page
				// and its lookahead, irrespective of Elasticsearch's physical ordering.
				pos := sort.Search(len(selected), func(i int) bool { return selected[i].rank >= rank })
				if pos >= offset+limit+1 {
					continue
				}
				selected = append(selected, homepageRankedRow{})
				copy(selected[pos+1:], selected[pos:])
				selected[pos] = homepageRankedRow{rank: rank, row: row, bytes: len(encoded)}
				retainedBytes += len(encoded)
				if len(selected) > offset+limit+1 {
					retainedBytes -= selected[len(selected)-1].bytes
					selected = selected[:len(selected)-1]
				}
				if retainedBytes > homepageMaxRetainedBytes {
					return nil, false, fmt.Errorf("homepage metadata exceeds traversal budget")
				}
			}
		}
		scanned += len(hits)
		if scanned >= total {
			break
		}
		if scanned >= homepageMaxCandidates {
			return nil, false, fmt.Errorf("homepage traversal candidate budget exhausted")
		}
		body, _ := json.Marshal(map[string]interface{}{"scroll_id": scrollID, "scroll": "1m"})
		response, err = es.homepageScrollPage(ctx, esapi.ScrollRequest{Body: bytes.NewReader(body)}, &scrollID)
		if err != nil {
			return nil, false, err
		}
		var pageTotal int
		hits, pageTotal, err = publicCatalogHits(response)
		if err != nil {
			return nil, false, err
		}
		if pageTotal != total {
			return nil, false, fmt.Errorf("homepage scroll total changed")
		}
	}
	if len(blogIDs) > 0 {
		for i := offset; i < min(len(selected), offset+limit); i++ {
			rows = append(rows, selected[i].row)
		}
		more = len(selected) > offset+limit
	}
	return rows, more, nil
}

// Read a bounded response and capture its scroll ID before validating the rest.
// Errors intentionally omit raw Elasticsearch bodies and opaque scroll tokens.
func (es *elasticsearchStorage) homepageScrollPage(ctx context.Context, request publicCatalogScrollRequest, scrollID *string) (map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := request.Do(ctx, es.client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("homepage search request failed")
	}
	defer res.Body.Close()
	dec := json.NewDecoder(io.LimitReader(res.Body, homepageMaxPageBytes))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("invalid homepage search response")
	}
	response := make(map[string]interface{})
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid homepage search response")
		}
		var value interface{}
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("invalid or oversized homepage search response")
		}
		response[key.(string)] = value
		if key == "_scroll_id" {
			if id, ok := value.(string); ok && id != "" {
				*scrollID = id
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("invalid homepage search response")
	}
	if dec.Decode(new(interface{})) != io.EOF {
		return nil, fmt.Errorf("invalid homepage search response")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("homepage search failed with status %d", res.StatusCode)
	}
	return response, nil
}
