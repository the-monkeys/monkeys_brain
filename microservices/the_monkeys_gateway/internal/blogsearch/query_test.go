package blogsearch

import (
	"strings"
	"testing"
)

func TestBuildSearchBodyHidesGroupOnly(t *testing.T) {
	raw, err := buildSearchBody(SearchOpts{Query: "tea", Limit: 10})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(raw), `"group_only"`) {
		t.Fatalf("search must hide group_only: %s", raw)
	}
}

func TestBuildSuggestBodyHidesGroupOnly(t *testing.T) {
	raw, err := buildSuggestBody(SuggestOpts{Query: "tea", Limit: 5})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(raw), `"group_only"`) {
		t.Fatalf("suggest must hide group_only: %s", raw)
	}
}
