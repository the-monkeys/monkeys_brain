// grafana_editorial prepares a local review artifact; it never contacts Elasticsearch.
package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/searchdoc"
)

const blogID = "1uvwek"
const ownerID = "9e48b1a5979fe25c187f2e20f024c20b"
const expectedOriginalTitle = "The Ultimate Guide to Grafana: What It Is, Why It’s Used, and How to Build a Complete Monitoring Stack with Prometheus, Node exporter and Docker"

//go:embed revision.json
var revisionJSON []byte

// decodeExport preserves integer precision, including counters and CAS tokens.
func decodeExport(raw []byte) (map[string]interface{}, map[string]interface{}, int64, int64, error) {
	var export map[string]interface{}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&export); err != nil {
		return nil, nil, 0, 0, err
	}
	if err := d.Decode(new(interface{})); err != io.EOF {
		return nil, nil, 0, 0, fmt.Errorf("expected one export object")
	}
	source, ok := export["_source"].(map[string]interface{})
	if !ok || source == nil {
		return nil, nil, 0, 0, fmt.Errorf("missing _source")
	}
	seq, seqOK := export["_seq_no"].(json.Number)
	term, termOK := export["_primary_term"].(json.Number)
	seqNo, seqErr := seq.Int64()
	primaryTerm, termErr := term.Int64()
	if !seqOK || !termOK || seqErr != nil || termErr != nil || seqNo < 0 || primaryTerm < 1 {
		return nil, nil, 0, 0, fmt.Errorf("valid _seq_no and _primary_term required")
	}
	return export, source, seqNo, primaryTerm, nil
}

func validateTarget(export, source map[string]interface{}, modified time.Time) error {
	now := time.Now().UTC()
	if modified.After(now) {
		return fmt.Errorf("content revision time must not be in the future")
	}
	if source["blog_id"] != blogID || source["owner_account_id"] != ownerID {
		return fmt.Errorf("unexpected blog ID or owner")
	}
	if id, present := export["_id"]; present && id != blogID {
		return fmt.Errorf("unexpected Elasticsearch _id")
	}
	if draft, ok := source["is_draft"].(bool); !ok || draft {
		return fmt.Errorf("target must be published, not a draft")
	}
	for _, key := range []string{"is_archived", "is_scheduled"} {
		if v := source[key]; v != nil {
			if b, ok := v.(bool); !ok || b {
				return fmt.Errorf("target has unsafe %s", key)
			}
		}
	}
	for _, key := range []string{"audience", "visibility", "group_visibility"} {
		if v := source[key]; v != nil {
			if s, ok := v.(string); !ok || (s != "" && s != "public") {
				return fmt.Errorf("target has restricted %s", key)
			}
		}
	}
	published, _ := source["published_time"].(string)
	publishedAt, err := time.Parse(time.RFC3339Nano, published)
	if err != nil || publishedAt.After(modified) || publishedAt.After(now) {
		return fmt.Errorf("valid past published_time required")
	}
	if v, present := source["content_modified_at"]; present && v != nil {
		s, ok := v.(string)
		when, err := time.Parse(time.RFC3339Nano, s)
		if !ok || err != nil || when.After(modified) {
			return fmt.Errorf("content_modified_at is invalid or newer than this revision")
		}
	}
	return nil
}

func blogDigest(source map[string]interface{}) (string, error) {
	if blog, ok := source["blog"].(map[string]interface{}); !ok || blog == nil {
		return "", fmt.Errorf("EditorJS blog object required")
	}
	b, err := json.Marshal(source["blog"])
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:]), nil
}

func jsonEqual(a, b interface{}) bool {
	aa, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(aa, bb)
}

// prepare emits only a partial ES update and its mandatory CAS coordinates.
// It has no client, URL, credentials, network import, or application startup hook.
func prepare(raw []byte, originalTitle, originalDigest string, modified time.Time) (map[string]interface{}, error) {
	digestBytes, err := hex.DecodeString(originalDigest)
	if originalTitle != expectedOriginalTitle || err != nil || len(digestBytes) != sha256.Size || modified.IsZero() {
		return nil, fmt.Errorf("reviewed original title, SHA-256 and modification time required")
	}
	export, source, seqNo, primaryTerm, err := decodeExport(raw)
	if err != nil {
		return nil, err
	}
	if err := validateTarget(export, source, modified); err != nil {
		return nil, err
	}
	digest, err := blogDigest(source)
	if err != nil {
		return nil, err
	}
	var revision map[string]interface{}
	if err := json.Unmarshal(revisionJSON, &revision); err != nil {
		return nil, err
	}
	oldBlog := source["blog"].(map[string]interface{})
	newBlog := make(map[string]interface{}, len(oldBlog))
	for k, v := range oldBlog {
		newBlog[k] = v
	}
	for k, v := range revision["blog"].(map[string]interface{}) {
		newBlog[k] = v
	}
	doc := map[string]interface{}{"blog": newBlog, "canonical_slug": revision["canonical_slug"], "seo_description": revision["seo_description"], "content_modified_at": modified.UTC().Format(time.RFC3339Nano), "rss_guid": revision["rss_guid"]}
	searchInput := map[string]interface{}{"blog": newBlog}
	if tags, present := source["tags"]; present {
		searchInput["tags"] = tags
	}
	derived := searchdoc.Apply(searchInput)
	for _, field := range []string{"title", "summary", "body", "tags"} {
		if value, present := derived[field]; present {
			doc[field] = value
		}
	}
	plan := map[string]interface{}{"blog_id": blogID, "if_seq_no": seqNo, "if_primary_term": primaryTerm, "already_applied": false}
	// A matching revision is a no-op, even if the requested preparation date
	// differs. Never bump its timestamp merely by rerunning the tool.
	matching := true
	for k, v := range doc {
		if k != "content_modified_at" && !jsonEqual(source[k], v) {
			matching = false
			break
		}
	}
	if matching && source["content_modified_at"] != nil {
		plan["already_applied"] = true
		return plan, nil
	}
	// Validate the authored first header rather than trusting a stale flat title.
	original := searchdoc.Build(map[string]interface{}{"blog": oldBlog}).Title
	if original != originalTitle || digest != strings.ToLower(originalDigest) {
		return nil, fmt.Errorf("original title/content does not match reviewed baseline; refusing newer or unrelated content")
	}
	plan["update"] = map[string]interface{}{"doc": doc}
	return plan, nil
}

func run(args []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("grafana_editorial", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	input := flags.String("input", "", "existing ES export JSON file (required)")
	inspect := flags.Bool("inspect", false, "print original title and canonical JSON blog digest for offline review; no update")
	title := flags.String("original-title", "", "exact original title from the reviewed backup")
	digest := flags.String("original-blog-sha256", "", "reviewed blog digest printed by -inspect")
	modified := flags.String("modified-at", "", "explicit content revision time in RFC3339 format")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" || flags.NArg() != 0 {
		return fmt.Errorf("-input is required; positional arguments are not accepted")
	}
	f, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer f.Close()
	const maxBytes = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxBytes {
		return fmt.Errorf("export exceeds 32 MiB")
	}
	var result map[string]interface{}
	if *inspect {
		export, source, seq, term, err := decodeExport(raw)
		if err != nil {
			return err
		}
		if err := validateTarget(export, source, time.Now().UTC()); err != nil {
			return err
		}
		digest, err := blogDigest(source)
		if err != nil {
			return err
		}
		result = map[string]interface{}{"blog_id": blogID, "original_title": searchdoc.Build(map[string]interface{}{"blog": source["blog"]}).Title, "original_blog_sha256": digest, "if_seq_no": seq, "if_primary_term": term}
	} else {
		when, err := time.Parse(time.RFC3339Nano, *modified)
		if err != nil {
			return fmt.Errorf("-modified-at must be RFC3339: %w", err)
		}
		result, err = prepare(raw, *title, *digest, when)
		if err != nil {
			return err
		}
	}
	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
