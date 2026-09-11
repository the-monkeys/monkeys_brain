package consumer

import "testing"

func TestExtractBlogTitleFromHeaderBlock(t *testing.T) {
	doc := map[string]interface{}{
		"blog": map[string]interface{}{
			"blocks": []interface{}{
				map[string]interface{}{
					"type": "header",
					"data": map[string]interface{}{
						"level": float64(1),
						"text":  "How India's Traffic Congestion Impacts Key Sectors",
					},
				},
			},
		},
	}
	got := extractBlogTitle(doc)
	if got != "How India's Traffic Congestion Impacts Key Sectors" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractBlogTitleStripsHtml(t *testing.T) {
	doc := map[string]interface{}{
		"title": "<b>Software Design Principles</b>",
	}
	got := extractBlogTitle(doc)
	if got != "Software Design Principles" {
		t.Fatalf("got %q", got)
	}
}
