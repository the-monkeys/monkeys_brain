package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/constants"
)

func (es *elasticsearchStorage) ListBlogIDs(ctx context.Context, limit, offset int32) ([]string, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	body := map[string]any{
		"_source": false,
		"query":   map[string]any{"match_all": map[string]any{}},
		"from":    offset,
		"size":    limit,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	res, err := es.client.Search(
		es.client.Search.WithContext(ctx),
		es.client.Search.WithIndex(constants.ElasticsearchBlogIndex),
		es.client.Search.WithBody(bytes.NewReader(raw)),
		es.client.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return nil, 0, fmt.Errorf("es search: %s", string(b))
	}
	var parsed struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		if h.ID != "" {
			ids = append(ids, h.ID)
		}
	}
	return ids, parsed.Hits.Total.Value, nil
}
