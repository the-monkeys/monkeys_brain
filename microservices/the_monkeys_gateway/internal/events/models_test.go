package events

import (
	"encoding/json"
	"testing"
)

func TestEventBodyPinOptional(t *testing.T) {
	var body EventBody
	if err := json.Unmarshal([]byte(`{"title":"t","start_time":"2026-09-06T10:00:00Z","end_time":"2026-09-06T11:00:00Z","event_type":"in_person"}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.Latitude != 0 || body.Longitude != 0 {
		t.Fatal("omitted pin must stay 0")
	}
	if err := json.Unmarshal([]byte(`{"title":"t","start_time":"2026-09-06T10:00:00Z","end_time":"2026-09-06T11:00:00Z","event_type":"in_person","latitude":12.97,"longitude":77.59}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.Latitude != 12.97 || body.Longitude != 77.59 {
		t.Fatalf("pin must bind, got %v,%v", body.Latitude, body.Longitude)
	}
}
