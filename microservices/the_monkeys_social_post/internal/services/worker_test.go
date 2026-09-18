package services

import "testing"

func TestMockPublishReturnsProviderReference(t *testing.T) {
	ref, err := mockPublish("linkedin", "A valid post")
	if err != nil {
		t.Fatalf("mockPublish returned error: %v", err)
	}
	if len(ref) < len("mock:linkedin:") || ref[:len("mock:linkedin:")] != "mock:linkedin:" {
		t.Fatalf("unexpected provider reference %q", ref)
	}
}

func TestMockPublishRejectsPlatformLimit(t *testing.T) {
	text := ""
	for i := 0; i < 281; i++ {
		text += "x"
	}
	if _, err := mockPublish("x", text); err == nil {
		t.Fatal("mockPublish accepted text over X limit")
	}
}

func TestMockPublishSupportsDeterministicTransientFailure(t *testing.T) {
	if _, err := mockPublish("x", "::force-transient-failure::"); err == nil {
		t.Fatal("mockPublish accepted forced transient failure marker")
	}
}
