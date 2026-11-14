package database

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// publicCatalogHits validates the search envelope separately from individual
// legacy records. A partial search must not masquerade as an empty catalog.
func publicCatalogHits(response map[string]interface{}) ([]interface{}, int, error) {
	if response["timed_out"] == true {
		return nil, 0, fmt.Errorf("public catalog search timed out")
	}
	if shards, ok := response["_shards"].(map[string]interface{}); ok {
		if failed, ok := shards["failed"].(float64); ok && failed > 0 {
			return nil, 0, fmt.Errorf("public catalog search has failed shards")
		}
	}
	envelope, ok := response["hits"].(map[string]interface{})
	if !ok {
		return nil, 0, fmt.Errorf("public catalog search is missing hits")
	}
	value := envelope["total"]
	if object, ok := value.(map[string]interface{}); ok {
		if relation, present := object["relation"]; present && relation != "eq" {
			return nil, 0, fmt.Errorf("public catalog search total is not exact")
		}
		value = object["value"]
	}
	count, ok := value.(float64)
	// Reject fractional, negative and overflowing totals before converting to int.
	// Numeric totals remain supported for older Elasticsearch versions.
	if !ok || math.IsNaN(count) || math.IsInf(count, 0) || count < 0 ||
		math.Trunc(count) != count || count >= math.Exp2(float64(strconv.IntSize-1)) {
		return nil, 0, fmt.Errorf("public catalog search has an invalid total")
	}
	total := int(count)
	hits, ok := envelope["hits"].([]interface{})
	if !ok {
		return nil, total, fmt.Errorf("public catalog search has an invalid hit list")
	}
	return hits, total, nil
}

// publicCatalogMetadata preserves the existing response fields and authored
// strings. Missing or malformed content does not discard an otherwise valid hit.
func publicCatalogMetadata(source map[string]interface{}) map[string]interface{} {
	metadata := map[string]interface{}{
		"blog_id":          source["blog_id"],
		"owner_account_id": source["owner_account_id"],
		"tags":             source["tags"],
		"content_type":     source["content_type"],
		"published_time":   source["published_time"],
	}
	blog, _ := source["blog"].(map[string]interface{})
	blocks, _ := blog["blocks"].([]interface{})
	var title, firstHeader, paragraphTitle, firstParagraph, firstImage string
	for _, raw := range blocks {
		block, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		data, ok := block["data"].(map[string]interface{})
		if !ok {
			continue
		}
		text, _ := data["text"].(string)
		switch block["type"] {
		case "header":
			if strings.TrimSpace(text) == "" {
				continue
			}
			if firstHeader == "" {
				firstHeader = text
			}
			if title == "" {
				// JSON numbers normally decode as float64; old posts can have string
				// levels, and callers constructing maps can supply integer levels.
				switch data["level"] {
				case float64(1), float32(1), int(1), int32(1), int64(1), json.Number("1"), "1":
					title = text
				}
			}
		case "paragraph":
			if firstParagraph == "" {
				firstParagraph = text
			}
			if paragraphTitle == "" && strings.TrimSpace(text) != "" {
				paragraphTitle = text
			}
		case "image":
			if firstImage == "" {
				file, _ := data["file"].(map[string]interface{})
				firstImage, _ = file["url"].(string)
			}
		}
	}
	// Prefer the first usable H1 anywhere in the post, then a legacy header,
	// then the first usable paragraph. Posts without text remain untitled.
	if title == "" {
		title = firstHeader
	}
	if title == "" {
		title = paragraphTitle
	}
	metadata["title"] = title
	metadata["first_paragraph"] = firstParagraph
	metadata["first_image"] = firstImage
	addAIMetadata(metadata, source)
	return metadata
}
