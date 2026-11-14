package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/constants"
)

const (
	publicCatalogWindow           = 10000
	publicCatalogMaxPageSize      = 1000
	publicCatalogTraversalTimeout = 10 * time.Second
	publicCatalogBatch            = 1000
	publicCatalogKeepAlive        = time.Minute
	publicCatalogCleanupTimeout   = 3 * time.Second
)

func publicCatalogNeedsScroll(limit, offset int) bool {
	// Subtract rather than add so even an extreme offset cannot overflow.
	return limit >= 0 && offset >= 0 && (offset > publicCatalogWindow || limit > publicCatalogWindow-offset)
}

// publicCatalogScroll preserves the offset API using a bounded internal traversal.
// Only the requested range is retained; skipped batches are discarded.
func (es *elasticsearchStorage) publicCatalogScroll(ctx context.Context, query map[string]interface{}, limit, offset int) (rows []map[string]interface{}, total int, err error) {
	limit = min(limit, publicCatalogMaxPageSize)
	ctx, cancel := context.WithTimeout(ctx, publicCatalogTraversalTimeout)
	defer cancel()

	scrollID := ""
	defer func() {
		if scrollID != "" {
			err = errors.Join(err, es.clearPublicCatalogScroll(ctx, scrollID))
		}
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		if err != nil {
			rows = nil
		}
	}()

	query["from"] = 0
	query["size"] = publicCatalogBatch
	body, err := json.Marshal(query)
	if err != nil {
		return nil, 0, fmt.Errorf("public catalog scroll query is invalid")
	}
	request := esapi.SearchRequest{
		Index:  []string{constants.ElasticsearchBlogIndex},
		Body:   bytes.NewReader(body),
		Scroll: publicCatalogKeepAlive,
	}
	response, err := es.publicCatalogScrollPage(ctx, request, &scrollID)
	if err != nil {
		return nil, 0, err
	}
	hits, total, err := publicCatalogHits(response)
	if err != nil {
		return nil, total, err
	}
	rows = make([]map[string]interface{}, 0)
	// Clamp the requested end to the snapshot total without overflowing.
	end := offset
	if offset < total {
		end += min(limit, total-offset)
	}
	for seen := 0; ; {
		if err := ctx.Err(); err != nil {
			return nil, total, err
		}
		if id, ok := response["_scroll_id"].(string); !ok || id == "" {
			return nil, total, fmt.Errorf("public catalog scroll is missing its context")
		}
		if len(hits) != min(publicCatalogBatch, total-seen) {
			return nil, total, fmt.Errorf("public catalog scroll returned an unexpected batch length")
		}
		if end == offset {
			return rows, total, nil
		}
		start := max(0, offset-seen)
		stop := min(len(hits), end-seen)
		for i := start; i < stop; i++ {
			hit, ok := hits[i].(map[string]interface{})
			if !ok {
				continue
			}
			source, ok := hit["_source"].(map[string]interface{})
			if ok && source != nil {
				rows = append(rows, publicCatalogMetadata(source))
			}
		}
		seen += len(hits)
		if seen >= end {
			return rows, total, ctx.Err()
		}
		body, _ := json.Marshal(map[string]interface{}{
			"scroll_id": scrollID,
			"scroll":    "1m",
		})
		next := esapi.ScrollRequest{Body: bytes.NewReader(body)}
		response, err = es.publicCatalogScrollPage(ctx, next, &scrollID)
		if err != nil {
			return nil, total, err
		}
		var pageTotal int
		hits, pageTotal, err = publicCatalogHits(response)
		if err != nil {
			return nil, total, err
		}
		if pageTotal != total {
			return nil, total, fmt.Errorf("public catalog scroll total changed")
		}
	}
}

type publicCatalogScrollRequest interface {
	Do(context.Context, esapi.Transport) (*esapi.Response, error)
}

// Capture the latest context before validating even an error response, so any
// context supplied by Elasticsearch is still cleared. Never return raw errors or
// response bodies here: they can contain scroll IDs.
func (es *elasticsearchStorage) publicCatalogScrollPage(ctx context.Context, request publicCatalogScrollRequest, scrollID *string) (map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := request.Do(ctx, es.client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("public catalog scroll request failed")
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	var response map[string]interface{}
	decodeErr := json.Unmarshal(body, &response)
	if id, ok := response["_scroll_id"].(string); ok && id != "" {
		*scrollID = id
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if res.IsError() {
		return nil, fmt.Errorf("public catalog scroll request failed with status %d", res.StatusCode)
	}
	if readErr != nil || decodeErr != nil {
		return nil, fmt.Errorf("public catalog scroll response is invalid")
	}
	return response, nil
}

func (es *elasticsearchStorage) clearPublicCatalogScroll(ctx context.Context, scrollID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publicCatalogCleanupTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]interface{}{"scroll_id": []string{scrollID}})
	request := esapi.ClearScrollRequest{Body: bytes.NewReader(body)}
	res, err := request.Do(cleanupCtx, es.client)
	if err != nil {
		return fmt.Errorf("public catalog scroll cleanup request failed")
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("public catalog scroll cleanup failed with status %d", res.StatusCode)
	}
	var result struct {
		Succeeded bool `json:"succeeded"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil || !result.Succeeded {
		return fmt.Errorf("public catalog scroll cleanup was unsuccessful")
	}
	return nil
}
