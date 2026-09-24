package blogsearch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/common/audience"
)

// SearchOpts captures the validated, server-side-bounded inputs the
// caller wants to query. The HTTP handler is responsible for clamping
// raw user input into this struct.
type SearchOpts struct {
	Query  string
	Limit  int
	Cursor string // opaque, base64(json([sort_value, blog_id])) — see encodeCursor
}

// Hit is a slim projection of an ES document plus its highlight snippets.
// We deliberately do not echo the entire `_source` because the legacy
// `blog.blocks[]` payload is large and irrelevant to a result-list view.
type Hit struct {
	BlogID            string                 `json:"blog_id"`
	Title             string                 `json:"title,omitempty"`
	Summary           string                 `json:"summary,omitempty"`
	Slug              string                 `json:"slug,omitempty"`
	AuthorUsername    string                 `json:"author_username,omitempty"`
	AuthorDisplayName string                 `json:"author_display_name,omitempty"`
	OwnerAccountID    string                 `json:"owner_account_id,omitempty"`
	PublishedTime     string                 `json:"published_time,omitempty"`
	LikeCount         int                    `json:"like_count"`
	ViewCount         int                    `json:"view_count"`
	Tags              []string               `json:"tags,omitempty"`
	Highlight         map[string][]string    `json:"highlight,omitempty"`
	Score             float64                `json:"score"`
	Extra             map[string]interface{} `json:"-"` // reserved
}

// SearchResult is the gateway-facing response shape.
type SearchResult struct {
	Hits       []Hit  `json:"hits"`
	NextCursor string `json:"next_cursor,omitempty"`
	Took       int64  `json:"took_ms"`
	Total      int64  `json:"total"`
}

// Search executes the v2 ranked query against the alias.
//
// Query shape (high-level):
//
//   - multi_match with cross_fields + AND operator across
//     title^4, summary^2, body, tags.text^2, author_*.text^1.5.
//   - function_score wrapping the multi_match to bias by recency
//     (gauss decay on published_time, scale = 90d) and popularity
//     (log1p like_count). Recency has the larger weight because stale
//     "viral" posts shouldn't permanently dominate.
//   - filter: is_draft=false AND is_archived=false. is_scheduled
//     posts in the future are also excluded.
//   - search_after cursor pagination using [_score, blog_id]. We never
//     accept `from` > 0 — deep pagination at the gateway is a
//     well-known DoS vector against ES.
//
// All bytes returned to the caller come from controlled fields. We do
// not echo the raw `_source` blob, only the projected subset, so a
// future schema field cannot accidentally leak through search results.
func (c *Client) Search(ctx context.Context, opts SearchOpts) (*SearchResult, error) {
	if !c.Available() {
		return nil, errSearchUnavailable
	}

	body, err := buildSearchBody(opts)
	if err != nil {
		return nil, fmt.Errorf("blogsearch: build query: %w", err)
	}

	res, err := c.es.Search(
		c.es.Search.WithContext(ctx),
		c.es.Search.WithIndex(BlogIndexAlias),
		c.es.Search.WithBody(bytes.NewReader(body)),
		// Trim the wire payload — we project hits ourselves below.
		c.es.Search.WithSourceIncludes(
			"blog_id", "title", "summary", "slug",
			"author_username", "author_display_name",
			"owner_account_id", "published_time",
			"like_count", "view_count", "tags",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("blogsearch: es search: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		// Read a bounded slice of the error body — never echo it to the
		// client (could leak index names / cluster details).
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		c.log.Warnw("blogsearch: es returned error", "status", res.Status(), "body", string(b))
		return nil, fmt.Errorf("blogsearch: es error %s", res.Status())
	}

	return decodeSearch(res.Body)
}

// SuggestOpts is the validated input for autocomplete.
type SuggestOpts struct {
	Query string
	Limit int
}

// Suggest serves the autocomplete dropdown. It runs a much cheaper
// match query against `title.autocomplete` only and returns at most
// `Limit` items. No highlighting (we re-bold on the client) and no
// scoring tweaks — recency / popularity bias only matters once a user
// commits to a real search.
func (c *Client) Suggest(ctx context.Context, opts SuggestOpts) (*SearchResult, error) {
	if !c.Available() {
		return nil, errSearchUnavailable
	}

	body, err := buildSuggestBody(opts)
	if err != nil {
		return nil, fmt.Errorf("blogsearch: build suggest: %w", err)
	}

	res, err := c.es.Search(
		c.es.Search.WithContext(ctx),
		c.es.Search.WithIndex(BlogIndexAlias),
		c.es.Search.WithBody(bytes.NewReader(body)),
		c.es.Search.WithSourceIncludes(
			"blog_id", "title", "slug", "author_username", "published_time",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("blogsearch: es suggest: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		c.log.Warnw("blogsearch: suggest es error", "status", res.Status(), "body", string(b))
		return nil, fmt.Errorf("blogsearch: es error %s", res.Status())
	}

	return decodeSearch(res.Body)
}

// buildSearchBody marshals the full v2 query body. Constructing the
// JSON via map[string]interface{} (instead of string templates) keeps
// us safe from injection: user input only ever enters the tree as a
// concrete JSON string value, never as raw bytes.
func buildSearchBody(opts SearchOpts) ([]byte, error) {
	q := strings.TrimSpace(opts.Query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}

	now := time.Now().UTC().Format(time.RFC3339)

	multiMatch := map[string]interface{}{
		"multi_match": map[string]interface{}{
			"query": q,
			"type":  "best_fields",
			"fields": []string{
				"title^4",
				"title.autocomplete^2",
				"summary^2",
				"body",
				"tags.text^2",
				"author_username.text^1.5",
				"author_display_name^1.5",
			},
			"operator":    "and",
			"fuzziness":   "AUTO:4,7",
			"tie_breaker": 0.3,
		},
	}

	functionScore := map[string]interface{}{
		"function_score": map[string]interface{}{
			"query": multiMatch,
			"functions": []map[string]interface{}{
				{
					"gauss": map[string]interface{}{
						"published_time": map[string]interface{}{
							"origin": now,
							"scale":  "90d",
							"decay":  0.5,
						},
					},
					"weight": 1.5,
				},
				{
					"field_value_factor": map[string]interface{}{
						"field":    "like_count",
						"modifier": "log1p",
						"missing":  0,
					},
					"weight": 0.5,
				},
			},
			"score_mode": "sum",
			"boost_mode": "multiply",
		},
	}

	// Hard filters use must_not on the positive boolean instead of
	// term=false. The legacy v2 docs don't carry is_archived/is_draft
	// at all for most rows, and a term filter on a missing field
	// excludes the doc — which would silently zero out search results
	// during the v2→v3 cut-over. must_not on the positive value
	// treats "missing" as "not set", which matches our intent.
	mustNot := []map[string]interface{}{
		{"term": map[string]interface{}{"is_draft": true}},
		{"term": map[string]interface{}{"is_archived": true}},
		// Scheduled posts whose publish time hasn't arrived yet.
		{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"is_scheduled": true}},
					{"range": map[string]interface{}{
						"published_time": map[string]interface{}{"gt": now},
					}},
				},
			},
		},
	}
	mustNot = audience.AppendPublicListMustNot(mustNot)

	body := map[string]interface{}{
		"size":             opts.Limit,
		"track_total_hits": false, // we paginate via cursor; count is irrelevant
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":     []interface{}{functionScore},
				"must_not": mustNot,
			},
		},
		"sort": []interface{}{
			map[string]interface{}{"_score": "desc"},
			map[string]interface{}{"blog_id": "asc"},
		},
		"highlight": map[string]interface{}{
			"pre_tags":  []string{"<mark>"},
			"post_tags": []string{"</mark>"},
			"fields": map[string]interface{}{
				"title":   map[string]interface{}{"number_of_fragments": 0},
				"summary": map[string]interface{}{"number_of_fragments": 1, "fragment_size": 160},
				"body":    map[string]interface{}{"number_of_fragments": 1, "fragment_size": 200},
			},
			"encoder": "html",
		},
	}

	if cursor := strings.TrimSpace(opts.Cursor); cursor != "" {
		sortVals, err := decodeCursor(cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor: %w", err)
		}
		body["search_after"] = sortVals
	}

	return json.Marshal(body)
}

func buildSuggestBody(opts SuggestOpts) ([]byte, error) {
	q := strings.TrimSpace(opts.Query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}

	body := map[string]interface{}{
		"size":             opts.Limit,
		"track_total_hits": false,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"match": map[string]interface{}{
							"title.autocomplete": map[string]interface{}{
								"query":    q,
								"operator": "and",
							},
						},
					},
				},
				"filter": []map[string]interface{}{},
				// must_not on the positive value treats missing fields
				// as "not set" — see the equivalent comment in
				// buildSearchBody.
				"must_not": []map[string]interface{}{
					{"term": map[string]interface{}{"is_draft": true}},
					{"term": map[string]interface{}{"is_archived": true}},
					audience.PublicListMustNot(),
				},
			},
		},
		"sort": []interface{}{
			map[string]interface{}{"_score": "desc"},
			map[string]interface{}{"published_time": map[string]interface{}{
				"order": "desc", "unmapped_type": "date",
			}},
		},
	}
	return json.Marshal(body)
}

// decodeSearch turns the raw ES response into a typed SearchResult.
// Defensive: every field is type-asserted with a fall-back so a
// schema drift on a single field does not break the entire response.
func decodeSearch(r io.Reader) (*SearchResult, error) {
	var raw struct {
		Took int64 `json:"took"`
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID        string                 `json:"_id"`
				Score     float64                `json:"_score"`
				Source    map[string]interface{} `json:"_source"`
				Sort      []interface{}          `json:"sort"`
				Highlight map[string][]string    `json:"highlight"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("blogsearch: decode: %w", err)
	}

	out := &SearchResult{
		Took:  raw.Took,
		Total: raw.Hits.Total.Value,
		Hits:  make([]Hit, 0, len(raw.Hits.Hits)),
	}

	var lastSort []interface{}
	for _, h := range raw.Hits.Hits {
		hit := Hit{
			BlogID:            asString(h.Source["blog_id"], h.ID),
			Title:             asString(h.Source["title"], ""),
			Summary:           asString(h.Source["summary"], ""),
			Slug:              asString(h.Source["slug"], ""),
			AuthorUsername:    asString(h.Source["author_username"], ""),
			AuthorDisplayName: asString(h.Source["author_display_name"], ""),
			OwnerAccountID:    asString(h.Source["owner_account_id"], ""),
			PublishedTime:     asString(h.Source["published_time"], ""),
			LikeCount:         asInt(h.Source["like_count"]),
			ViewCount:         asInt(h.Source["view_count"]),
			Tags:              asStringSlice(h.Source["tags"]),
			Highlight:         h.Highlight,
			Score:             h.Score,
		}
		out.Hits = append(out.Hits, hit)
		lastSort = h.Sort
	}

	if len(lastSort) > 0 && len(out.Hits) > 0 {
		if enc, err := encodeCursor(lastSort); err == nil {
			out.NextCursor = enc
		}
	}
	return out, nil
}

// encodeCursor / decodeCursor wrap the ES `sort` vector in opaque
// base64(json(...)). Opaqueness matters: callers cannot reverse-engineer
// the underlying sort fields, which means we can change the sort
// strategy in a future release without breaking pagination contracts.
func encodeCursor(sortVals []interface{}) (string, error) {
	b, err := json.Marshal(sortVals)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCursor(s string) ([]interface{}, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var out []interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	// Bound on cursor shape — refuse anything that looks pathological.
	if len(out) == 0 || len(out) > 4 {
		return nil, fmt.Errorf("cursor arity %d out of range", len(out))
	}
	return out, nil
}

func asString(v interface{}, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func asInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func asStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
