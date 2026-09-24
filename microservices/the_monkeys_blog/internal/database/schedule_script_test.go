package database

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScheduledPublishScriptDetachesGroupWhenAsked(t *testing.T) {
	raw, err := json.Marshal(scheduledPublishScript(true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "remove('group_slug')") || !strings.Contains(s, "remove('group_id')") {
		t.Fatalf("must strip group: %s", s)
	}
	if strings.Contains(s, "audience") {
		t.Fatalf("must not change audience: %s", s)
	}
}

func TestScheduledPublishScriptKeepsGroupByDefault(t *testing.T) {
	raw, err := json.Marshal(scheduledPublishScript(false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "remove('group_slug')") {
		t.Fatalf("active member keeps group: %s", raw)
	}
}
