package interservice

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Shapes copied from production structs so we prove rolling-deploy compatibility
// without importing every microservice.

type legacyUsers struct {
	AccountId  string   `json:"account_id"`
	Username   string   `json:"username"`
	IpAddress  string   `json:"ip_address"`
	Action     string   `json:"action"`
	BlogIds    []string `json:"blog_ids,omitempty"`
	BlogStatus string   `json:"blog_status"`
}

type legacyStorage struct {
	AccountId  string   `json:"account_id"`
	Username   string   `json:"username"`
	IpAddress  string   `json:"ip"`
	Action     string   `json:"action"`
	BlogId     string   `json:"blog_id"`
	BlogIds    []string `json:"blog_ids,omitempty"`
	BlogStatus string   `json:"status"`
}

func TestUnmarshalUsersPayload(t *testing.T) {
	raw := []byte(`{
		"account_id":"acc-1",
		"username":"ada",
		"email":"ada@example.com",
		"ip_address":"1.2.3.4",
		"action":"user_profile_directory_delete",
		"blog_ids":["b1","b2"],
		"blog_status":"published"
	}`)
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.AccountId != "acc-1" || m.Username != "ada" || m.IpAddress != "1.2.3.4" {
		t.Fatalf("got %+v", m)
	}
	if m.Action != "user_profile_directory_delete" || m.BlogStatus != "published" {
		t.Fatalf("got %+v", m)
	}
	if len(m.BlogIds) != 2 || m.BlogIds[0] != "b1" {
		t.Fatalf("blog_ids=%v", m.BlogIds)
	}
}

func TestUnmarshalStoragePayload(t *testing.T) {
	raw := []byte(`{
		"account_id":"acc-1",
		"username":"ada",
		"ip":"9.9.9.9",
		"action":"delete",
		"blog_id":"b9",
		"status":"deleted"
	}`)
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.IpAddress != "9.9.9.9" {
		t.Fatalf("IpAddress=%q", m.IpAddress)
	}
	if m.BlogStatus != "deleted" {
		t.Fatalf("BlogStatus=%q", m.BlogStatus)
	}
	if m.BlogId != "b9" {
		t.Fatalf("BlogId=%q", m.BlogId)
	}
}

func TestUnmarshalBlogPayload(t *testing.T) {
	raw := []byte(`{
		"account_id":"acc-1",
		"blog_id":"b1",
		"action":"blog_update",
		"blog_status":"published",
		"ip_address":"1.1.1.1",
		"client":"web",
		"tags":["go"],
		"schedule_time":"2026-09-06T10:00:00Z",
		"timezone":"UTC",
		"correlation_id":"c-1",
		"priority":"high"
	}`)
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.CorrelationId != "c-1" || m.Priority != "high" || m.Timezone != "UTC" {
		t.Fatalf("got %+v", m)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "go" {
		t.Fatalf("tags=%v", m.Tags)
	}
	if !m.ScheduleTime.Equal(time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("schedule=%v", m.ScheduleTime)
	}
}

func TestUnmarshalEventNotificationPayload(t *testing.T) {
	raw := []byte(`{
		"account_id":"acc-1",
		"username":"host",
		"new_username":"guest",
		"action":"event_rsvp_confirmed",
		"notification":"you are in",
		"event_slug":"tea-talk",
		"event_title":"Tea Talk"
	}`)
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.EventSlug != "tea-talk" || m.EventTitle != "Tea Talk" || m.NewUsername != "guest" {
		t.Fatalf("got %+v", m)
	}
}

func TestUnmarshalEventGroupFanoutPayload(t *testing.T) {
	raw := []byte(`{
		"username":"guest",
		"new_username":"host",
		"action":"event_application_received",
		"event_slug":"tea-talk",
		"event_title":"Tea Talk",
		"group_slug":"writers",
		"group_name":"Writers",
		"next_step":"You are in.",
		"change_summary":"The time changed.",
		"reason":"full"
	}`)
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.GroupName != "Writers" || m.NextStep != "You are in." {
		t.Fatalf("got %+v", m)
	}
	if m.ChangeSummary != "The time changed." || m.Reason != "full" {
		t.Fatalf("got %+v", m)
	}
	if m.GroupSlug != "writers" {
		t.Fatalf("got %+v", m)
	}

	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	for _, key := range []string{`"group_name":"Writers"`, `"next_step":"You are in."`, `"change_summary":"The time changed."`, `"reason":"full"`} {
		if !strings.Contains(s, key) {
			t.Fatalf("missing %s in %s", key, s)
		}
	}
}

func TestMarshalEmitsBothAliases(t *testing.T) {
	raw, err := json.Marshal(Message{
		IpAddress:  "1.2.3.4",
		BlogStatus: "published",
		Action:     "blog_update",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	for _, key := range []string{`"ip":"1.2.3.4"`, `"ip_address":"1.2.3.4"`, `"status":"published"`, `"blog_status":"published"`} {
		if !strings.Contains(s, key) {
			t.Fatalf("missing %s in %s", key, s)
		}
	}
}

func TestOldStorageStructReadsNewBytes(t *testing.T) {
	raw, err := json.Marshal(Message{
		AccountId:  "acc-1",
		Username:   "ada",
		IpAddress:  "1.2.3.4",
		Action:     "delete",
		BlogId:     "b1",
		BlogStatus: "deleted",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var old legacyStorage
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("old storage unmarshal: %v", err)
	}
	if old.IpAddress != "1.2.3.4" || old.BlogStatus != "deleted" || old.BlogId != "b1" {
		t.Fatalf("got %+v from %s", old, raw)
	}
}

func TestOldUsersStructReadsNewBytes(t *testing.T) {
	raw, err := json.Marshal(Message{
		AccountId:  "acc-1",
		Username:   "ada",
		IpAddress:  "1.2.3.4",
		Action:     "user_profile_directory_delete",
		BlogIds:    []string{"b1"},
		BlogStatus: "published",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var old legacyUsers
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("old users unmarshal: %v", err)
	}
	if old.IpAddress != "1.2.3.4" || old.BlogStatus != "published" || len(old.BlogIds) != 1 {
		t.Fatalf("got %+v from %s", old, raw)
	}
}

func TestZeroScheduleTimeOmitted(t *testing.T) {
	raw, err := json.Marshal(Message{Action: "blog_create", AccountId: "acc-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "0001-01-01") {
		t.Fatalf("zero time leaked: %s", raw)
	}
	if strings.Contains(string(raw), "schedule_time") {
		t.Fatalf("empty schedule_time present: %s", raw)
	}
}

func TestCanonicalWinsWhenAliasesDiffer(t *testing.T) {
	raw := []byte(`{"ip_address":"canonical","ip":"alias","blog_status":"published","status":"draft"}`)
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.IpAddress != "canonical" {
		t.Fatalf("IpAddress=%q", m.IpAddress)
	}
	if m.BlogStatus != "published" {
		t.Fatalf("BlogStatus=%q", m.BlogStatus)
	}
}
