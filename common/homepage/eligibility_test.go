package homepage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSharedWordCountFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/word-count.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name  string `json:"name"`
		Text  string `json:"text"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			if got := CountWords(fixture.Text); got != fixture.Count {
				t.Fatalf("got %d want %d", got, fixture.Count)
			}
		})
	}
}

func TestSharedDocumentFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/documents.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string                 `json:"name"`
		Document map[string]interface{} `json:"document"`
		Title    string                 `json:"title"`
		Count    int                    `json:"count"`
		Eligible bool                   `json:"eligible"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			got := Assess(fixture.Document)
			if got.Title != fixture.Title || got.WordCount != fixture.Count || got.Eligible != fixture.Eligible {
				t.Fatalf("got %+v, expected title=%q words=%d eligible=%t", got, fixture.Title, fixture.Count, fixture.Eligible)
			}
		})
	}
}

func article(words int) map[string]interface{} {
	return map[string]interface{}{
		"blog_id": "older-public-article", "is_draft": false,
		"audience": "public", "tags": []interface{}{"technology"},
		"blog": map[string]interface{}{"blocks": []interface{}{
			map[string]interface{}{"type": "header", "data": map[string]interface{}{"text": "A useful guide to building communities", "level": float64(1)}},
			map[string]interface{}{"type": "paragraph", "data": map[string]interface{}{"text": strings.Repeat("knowledge ", words)}},
		}},
	}
}

func blocks(doc map[string]interface{}) []interface{} {
	return doc["blog"].(map[string]interface{})["blocks"].([]interface{})
}
func setText(doc map[string]interface{}, index int, text string) {
	blocks(doc)[index].(map[string]interface{})["data"].(map[string]interface{})["text"] = text
}

func TestEligibilityWordBoundary(t *testing.T) {
	for _, count := range []int{0, 2, 249, 250, 251} {
		got := Assess(article(count))
		if got.Eligible != (count >= 250) || got.WordCount != count || got.MinimumWords != 250 || got.Version != "homepage-v1" {
			t.Fatalf("words=%d assessment=%+v", count, got)
		}
		if got.LeadEligible {
			t.Fatal("an image-less article must not become the lead")
		}
	}
}

func TestStructuredBodyCompatibility(t *testing.T) {
	for _, sample := range []struct {
		name     string
		doc      map[string]interface{}
		title    string
		count    int
		eligible bool
	}{
		{"explicit placeholder cannot borrow H2", map[string]interface{}{"title": "Untitled Post", "blocks": []interface{}{map[string]interface{}{"type": "header", "data": map[string]interface{}{"text": "Introduction", "level": 2}}, map[string]interface{}{"type": "paragraph", "data": map[string]interface{}{"text": strings.Repeat("knowledge ", 250)}}}}, "Untitled Post", 251, false},
		{"custom title", map[string]interface{}{"blocks": []interface{}{map[string]interface{}{"type": "title", "data": map[string]interface{}{"text": "Scientific observations"}}, map[string]interface{}{"type": "methodology", "data": map[string]interface{}{"studyDesign": strings.Repeat("knowledge ", 250)}}}}, "Scientific observations", 250, true},
		{"numbered markdown", map[string]interface{}{"title": "Scientific observations", "blocks": []interface{}{map[string]interface{}{"type": "markdown", "data": map[string]interface{}{"markdown": strings.Repeat("1. knowledge\n", 125)}}}}, "Scientific observations", 125, false},
		{"unused markdown references", map[string]interface{}{"title": "Scientific observations", "blocks": []interface{}{map[string]interface{}{"type": "markdown", "data": map[string]interface{}{"markdown": strings.Repeat("[unused]: https://example.test/image \"some words\"\n", 100)}}}}, "Scientific observations", 0, false},
		{"nested placeholders", map[string]interface{}{"title": "Scientific observations", "blocks": []interface{}{map[string]interface{}{"type": "paragraph", "data": map[string]interface{}{"text": strings.Repeat("knowledge ", 248)}}, map[string]interface{}{"type": "list", "data": map[string]interface{}{"items": []interface{}{"TODO", "knowledge"}}}}}, "Scientific observations", 249, false},
		{"legacy placeholders", map[string]interface{}{"title": "Scientific observations", "body": strings.Repeat("Start writing here\n", 84)}, "Scientific observations", 0, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			sample.doc["is_draft"] = false
			sample.doc["tags"] = []string{"science"}
			got := Assess(sample.doc)
			if got.Title != sample.title || got.WordCount != sample.count || got.Eligible != sample.eligible {
				t.Fatalf("got %+v; want title=%q words=%d eligible=%t", got, sample.title, sample.count, sample.eligible)
			}
		})
	}
}

func TestExactPlaceholderTitlesAndUnicode(t *testing.T) {
	for _, title := range []string{"", "Untitled Post", "<b>Untitled</b> Post", "Ｕｎｔｉｔｌｅｄ Ｐｏｓｔ", " Test! ", "<script>Real title</script>", "&nbsp;"} {
		doc := article(300)
		setText(doc, 0, title)
		if got := Assess(doc); got.Eligible {
			t.Errorf("placeholder title %q qualified: %+v", title, got)
		}
	}
	for _, title := range []string{"Testing Go applications", "हिंदी में उपयोगी लेख", "量子计算", "1984"} {
		doc := article(250)
		setText(doc, 0, title)
		if !Assess(doc).Eligible {
			t.Errorf("authored title %q rejected", title)
		}
	}
}

func TestBodyCountsVisibleUnicodeWordsWithoutTitleOrURLs(t *testing.T) {
	for _, sample := range []struct {
		text string
		want int
	}{
		{"<strong>Hel</strong>lo &amp; world", 2},
		{"<p>one</p><p>two</p>", 2},
		{"<script>one two three</script><style>four five</style>visible", 1},
		{strings.Repeat("https://example.test/one/two?q=three ", 260), 0},
		{"<a href=\"https://example.test/hidden words\">Visible link</a>", 2},
		{strings.Repeat("ज्ञान ", 250), 250},
		{strings.Repeat("学", 250), 250},
		{"<b>नम</b>स्ते दुनिया", 2},
	} {
		if got := CountWords(sample.text); got != sample.want {
			t.Errorf("CountWords sample %q = %d want %d", sample.text[:min(70, len(sample.text))], got, sample.want)
		}
	}
	doc := article(249)
	setText(doc, 0, strings.Repeat("long title ", 50))
	if got := Assess(doc); got.Eligible || got.WordCount != 249 {
		t.Fatalf("title inflated body: %+v", got)
	}
}

func TestEligibilityPreservesPublicScopeAndLegacyArticles(t *testing.T) {
	doc := article(250)
	delete(doc, "audience")
	if !Assess(doc).Eligible {
		t.Fatal("legacy public article without an audience or new fields should qualify")
	}
	for _, field := range []string{"is_draft", "is_archived", "is_scheduled"} {
		doc := article(300)
		doc[field] = true
		if Assess(doc).Eligible {
			t.Errorf("%s content reached homepage", field)
		}
	}
	for _, audience := range []string{"group_only", "private", "unlisted", "unexpected"} {
		doc := article(300)
		doc["audience"] = audience
		if Assess(doc).Eligible {
			t.Errorf("restricted/unknown audience %q qualified", audience)
		}
	}
	for _, topic := range []interface{}{nil, []interface{}{}, []interface{}{nil, "  ", "TODO"}} {
		doc := article(300)
		doc["tags"] = topic
		if Assess(doc).Eligible {
			t.Errorf("missing topic %#v qualified", topic)
		}
	}
	doc = article(250)
	doc["tags"] = []interface{}{"", "Technology"}
	if !Assess(doc).Eligible {
		t.Fatal("one usable topic should suffice")
	}
}

func TestLeadRequiresPersistedUsableCover(t *testing.T) {
	for _, image := range []string{"", "blob:temporary", "data:image/png;base64,a", "javascript:alert(1)", "/image-placeholder.png", "not an image URL"} {
		doc := article(250)
		blocks := blocks(doc)
		doc["blog"].(map[string]interface{})["blocks"] = append(blocks, map[string]interface{}{"type": "image", "data": map[string]interface{}{"file": map[string]interface{}{"url": image}}})
		if got := Assess(doc); !got.Eligible || got.LeadEligible {
			t.Errorf("bad cover %q: %+v", image, got)
		}
	}
	for _, image := range []string{"https://images.example.test/cover.webp", "/api/v2/storage/public/cover.png"} {
		doc := article(250)
		doc["blog"].(map[string]interface{})["blocks"] = append(blocks(doc), map[string]interface{}{"type": "image", "data": map[string]interface{}{"file": map[string]interface{}{"url": image}}})
		if !Assess(doc).LeadEligible {
			t.Errorf("persisted cover %q rejected", image)
		}
	}
}

func TestUntitledCannotBorrowAParagraphOrSectionHeading(t *testing.T) {
	doc := article(300)
	setText(doc, 0, "")
	doc["blog"].(map[string]interface{})["blocks"] = append(blocks(doc), map[string]interface{}{"type": "header", "data": map[string]interface{}{"text": "Introduction", "level": float64(2)}})
	if Assess(doc).Eligible {
		t.Fatal("an empty title must not be replaced with a section or paragraph")
	}
}

func TestPolicyDoesNotTrustOrMutateStoredEligibility(t *testing.T) {
	doc := article(2)
	doc["homepage_eligibility"] = map[string]interface{}{"eligible": true, "word_count": 10000}
	before, _ := json.Marshal(doc)
	if Assess(doc).Eligible {
		t.Fatal("a client/stale eligibility flag overrode actual content")
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("homepage policy changed the published article")
	}
}

func TestLegacyListsAndFlatTextRemainSupported(t *testing.T) {
	doc := article(0)
	items := make([]interface{}, 250)
	for i := range items {
		items[i] = map[string]interface{}{"content": "ज्ञान", "items": []interface{}{}}
	}
	doc["blog"].(map[string]interface{})["blocks"] = append(blocks(doc), map[string]interface{}{"type": "list", "data": map[string]interface{}{"items": items}})
	if got := Assess(doc); !got.Eligible || got.WordCount != 250 {
		t.Fatalf("nested list article: %+v", got)
	}
	doc = article(0)
	delete(doc, "blog")
	doc["title"] = "A legacy article"
	doc["body"] = "A legacy article\n" + strings.Repeat("useful ", 250)
	if got := Assess(doc); !got.Eligible || got.WordCount != 250 {
		t.Fatalf("legacy flat article: %+v", got)
	}
}

func TestMalformedDocumentsAndFuturePublicationDoNotQualify(t *testing.T) {
	for _, doc := range []map[string]interface{}{nil, {}, {"is_draft": false, "blog": []interface{}{}}, {"is_draft": "false", "blog": map[string]interface{}{"blocks": true}}} {
		if Assess(doc).Eligible {
			t.Fatal("malformed article qualified")
		}
	}
	doc := article(250)
	doc["published_time"] = time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	if Assess(doc).Eligible {
		t.Fatal("future publication qualified before publication")
	}
}
