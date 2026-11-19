// Package homepage defines homepage placement rules. It never gates publishing
// or mutates an article, and deliberately does not trust stored eligibility flags.
package homepage

import (
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/text/unicode/norm"
)

const (
	Version      = "homepage-v1"
	MinimumWords = 250
	MetadataKey  = "x-monkeys-homepage-policy"
	maxTextBytes = 1 << 20
)

type Assessment struct {
	Eligible     bool     `json:"eligible"`
	LeadEligible bool     `json:"lead_eligible"`
	WordCount    int      `json:"word_count"`
	MinimumWords int      `json:"minimum_words"`
	Reasons      []string `json:"reasons"`
	Version      string   `json:"version"`
	Title        string   `json:"-"`
	CoverImage   string   `json:"-"`
	Topics       []string `json:"-"`
}

var urls = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s<>]+`)
var commonPlaceholders = set("tbd", "tba", "todo", "placeholder", "coming soon", "to be announced", "to be confirmed", "lorem ipsum", "lorem ipsum dolor sit amet")
var titlePlaceholders = set("untitled", "untitled post", "untitled article", "untitled event", "new post", "new article", "title", "post title", "article title", "your title", "add a title", "enter title", "write your title", "test", "test post", "test article")
var bodyPlaceholders = set("start writing", "start writing here", "write something", "write your story", "your content here")

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}
func stringValue(value interface{}) string { s, _ := value.(string); return s }
func object(value interface{}) map[string]interface{} {
	m, _ := value.(map[string]interface{})
	return m
}
func array(value interface{}) []interface{} {
	switch v := value.(type) {
	case []interface{}:
		return v
	case []string:
		out := make([]interface{}, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out
	case []map[string]interface{}:
		out := make([]interface{}, len(v))
		for i, m := range v {
			out[i] = m
		}
		return out
	}
	return nil
}

func comparable(value string) string {
	return strings.TrimFunc(strings.ToLower(strings.Join(strings.Fields(PlainText(value)), " ")), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(".!?:;", r) })
}

func useful(value string, title bool) bool {
	text := comparable(value)
	if commonPlaceholders[text] || title && titlePlaceholders[text] {
		return false
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// PlainText retains visible text and inline word joins, not markup, attributes,
// scripts or styles. The tokenizer handles HTML entities without a browser.
func PlainText(value string) string {
	if len(value) > maxTextBytes {
		value = value[:maxTextBytes]
	}
	z := html.NewTokenizer(strings.NewReader(value))
	var b strings.Builder
	hidden := ""
	for {
		tokenType := z.Next()
		if tokenType == html.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			break
		}
		token := z.Token()
		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			if token.Data == "script" || token.Data == "style" {
				hidden = token.Data
				continue
			}
			if hidden == "" && blockTag(token.Data) {
				b.WriteByte(' ')
			}
		case html.EndTagToken:
			if token.Data == hidden {
				hidden = ""
				b.WriteByte(' ')
			} else if hidden == "" && blockTag(token.Data) {
				b.WriteByte(' ')
			}
		case html.TextToken:
			if hidden == "" {
				b.WriteString(token.Data)
			}
		}
	}
	return norm.NFKC.String(b.String())
}

func blockTag(tag string) bool {
	switch tag {
	case "p", "div", "br", "li", "ul", "ol", "table", "tr", "td", "th", "blockquote", "pre", "hr", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

// CountWords uses Unicode letter/number runs, preserving combining marks used
// in Hindi and other scripts. Han and Japanese kana count as individual readable
// units instead of treating a whole unspaced article as one ASCII word.
func CountWords(value string) int {
	text := urls.ReplaceAllString(PlainText(value), " ")
	count := 0
	inWord := false
	for _, r := range text {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
			count++
			inWord = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if !inWord {
				count++
			}
			inWord = true
			continue
		}
		if unicode.IsMark(r) || r == '\u200c' || r == '\u200d' {
			continue
		}
		inWord = false
	}
	return count
}

func headerOne(value interface{}) bool {
	switch value {
	case float64(1), float32(1), int(1), int32(1), int64(1), "1":
		return true
	}
	return false
}

func listText(value interface{}, depth int) string {
	if depth > 16 {
		return ""
	}
	var b strings.Builder
	for _, item := range array(value) {
		if s, ok := item.(string); ok {
			b.WriteString(bodyUnit(s))
			b.WriteByte(' ')
			continue
		}
		m := object(item)
		b.WriteString(bodyUnit(stringValue(m["content"])))
		b.WriteByte(' ')
		b.WriteString(bodyUnit(stringValue(m["text"])))
		b.WriteByte(' ')
		b.WriteString(listText(m["items"], depth+1))
		b.WriteByte(' ')
	}
	return b.String()
}

// Filter individual visible units, so list items, legacy lines and structured
// fields cannot turn editor placeholders into article body words.
func bodyUnit(value string) string {
	var parts []string
	for _, line := range strings.Split(value, "\n") {
		text := comparable(line)
		if text != "" && !bodyPlaceholders[text] && !commonPlaceholders[text] {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

var markdownDefinition = regexp.MustCompile(`(?m)^ {0,3}\[[^\]]+\]:.*$`)
var markdownLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^\n)]*\)`)
var markdownReference = regexp.MustCompile(`\[([^\]]+)\]\[[^\]]*\]`)
var markdownPrefix = regexp.MustCompile(`(?m)^\s*(?:#{1,6}\s+|[-*+]\s+|\d+[.)]\s+|>\s*)`)

func markdownText(value string) string {
	value = markdownDefinition.ReplaceAllString(value, "")
	value = markdownLink.ReplaceAllString(value, "$1")
	value = markdownReference.ReplaceAllString(value, "$1")
	return markdownPrefix.ReplaceAllString(value, "")
}

func bodyFields(data map[string]interface{}, keys ...string) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, bodyUnit(stringValue(data[key])))
	}
	return strings.Join(parts, " ")
}

func blockBody(kind string, data map[string]interface{}) string {
	switch kind {
	case "paragraph", "header":
		return stringValue(data["text"])
	case "quote":
		return bodyFields(data, "text", "caption")
	case "list", "checklist":
		return listText(data["items"], 0)
	case "table":
		var b strings.Builder
		for _, row := range array(data["content"]) {
			for _, cell := range array(row) {
				b.WriteString(bodyUnit(stringValue(cell)))
				b.WriteByte(' ')
			}
		}
		return b.String()
	case "warning":
		return stringValue(data["message"])
	case "code":
		return stringValue(data["code"])
	case "markdown", "Markdown", "MarkdownBlock":
		for _, key := range []string{"markdown", "content", "text"} {
			if value := stringValue(data[key]); value != "" {
				return markdownText(value)
			}
		}
	case "citation":
		return bodyFields(data, "title", "authors", "year", "source", "identifier", "citationText")
	case "methodology":
		return bodyFields(data, "studyDesign", "dataCollection", "analysisMethod", "assumptions", "limitations")
	case "dataset":
		return bodyFields(data, "title", "source", "sampleSize", "collectionDate", "license", "variables", "notes")
	case "formula":
		return bodyFields(data, "description", "expression")
	case "trend":
		return bodyFields(data, "summary")
	case "chart":
		return bodyFields(data, "title")
	}
	return ""
}

func usableCover(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\<>") || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "image-placeholder") || strings.Contains(lower, "placeholder-image") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return false
	}
	if u.Scheme == "" {
		return strings.HasPrefix(u.Path, "/") && u.Path != "/"
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.Path != "" && u.Path != "/"
}

func public(doc map[string]interface{}) bool {
	draft, ok := doc["is_draft"].(bool)
	if !ok || draft {
		return false
	}
	for _, key := range []string{"is_archived", "is_scheduled"} {
		if value, present := doc[key]; present && value != nil {
			flag, ok := value.(bool)
			if !ok || flag {
				return false
			}
		}
	}
	for _, key := range []string{"audience", "group_visibility", "visibility"} {
		if value, present := doc[key]; present && value != nil {
			scope, ok := value.(string)
			if !ok {
				return false
			}
			scope = strings.TrimSpace(scope)
			if scope != "" && scope != "public" {
				return false
			}
		}
	}
	if published := stringValue(doc["published_time"]); published != "" {
		if date, err := time.Parse(time.RFC3339Nano, published); err == nil && date.After(time.Now()) {
			return false
		}
	}
	return true
}

func Assess(doc map[string]interface{}) Assessment {
	a := Assessment{Version: Version, MinimumWords: MinimumWords, Reasons: []string{}}
	bl := object(doc["blog"])
	blocks := array(bl["blocks"])
	if blocks == nil {
		blocks = array(doc["blocks"])
	}
	titleIndex := -1
	firstHeader := -1
	hasH1 := false
	for i, raw := range blocks {
		block := object(raw)
		data := object(block["data"])
		if stringValue(block["type"]) == "title" || stringValue(block["type"]) == "header" {
			if firstHeader < 0 {
				firstHeader = i
			}
			if !hasH1 && (stringValue(block["type"]) == "title" || headerOne(data["level"])) {
				hasH1 = true
				titleIndex = i
				a.Title = stringValue(data["text"])
			}
		}
		if stringValue(block["type"]) == "image" {
			image := strings.TrimSpace(stringValue(object(data["file"])["url"]))
			if a.CoverImage == "" && usableCover(image) {
				a.CoverImage = image
			}
		}
	}
	if !hasH1 {
		if _, explicitTitle := doc["title"]; explicitTitle {
			a.Title = stringValue(doc["title"])
		} else if firstHeader >= 0 {
			titleIndex = firstHeader
			a.Title = stringValue(object(object(blocks[firstHeader])["data"])["text"])
		} else {
			a.Title = stringValue(doc["title"])
		}
	}
	if a.CoverImage == "" {
		if image := strings.TrimSpace(stringValue(doc["first_image"])); usableCover(image) {
			a.CoverImage = image
		}
	}
	var body strings.Builder
	titleComparison := comparable(a.Title)
	for i, raw := range blocks {
		block := object(raw)
		data := object(block["data"])
		kind := stringValue(block["type"])
		if i == titleIndex || kind == "title" || kind == "header" && headerOne(data["level"]) {
			continue
		}
		text := blockBody(kind, data)
		for _, line := range strings.Split(text, "\n") {
			if comparable(line) != titleComparison {
				body.WriteString(bodyUnit(line))
				body.WriteByte(' ')
			}
		}
		if body.Len() >= maxTextBytes {
			break
		}
	}
	if len(blocks) == 0 {
		text := stringValue(doc["body"])
		for _, line := range strings.Split(text, "\n") {
			if comparable(line) != titleComparison {
				body.WriteString(bodyUnit(line))
				body.WriteByte(' ')
			}
		}
	}
	a.WordCount = CountWords(body.String())
	tags := array(doc["tags"])
	if len(tags) == 0 {
		tags = array(bl["tags"])
	}
	for _, raw := range tags {
		topic := strings.TrimSpace(stringValue(raw))
		if useful(topic, false) {
			a.Topics = append(a.Topics, topic)
		}
	}
	if !public(doc) {
		a.Reasons = append(a.Reasons, "not_public")
	}
	if !useful(a.Title, true) {
		a.Reasons = append(a.Reasons, "title_missing")
	}
	if a.WordCount < MinimumWords {
		a.Reasons = append(a.Reasons, "body_too_short")
	}
	if len(a.Topics) == 0 {
		a.Reasons = append(a.Reasons, "topic_missing")
	}
	a.Eligible = len(a.Reasons) == 0
	a.LeadEligible = a.Eligible && a.CoverImage != ""
	return a
}
