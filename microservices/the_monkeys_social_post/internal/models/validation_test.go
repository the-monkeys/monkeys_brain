package models

import "testing"

func TestPlatformPoliciesHaveSupportedLimits(t *testing.T) {
	expected := []string{"x", "linkedin", "instagram", "facebook", "youtube", "tiktok"}
	for _, platform := range expected {
		policy, ok := PlatformPolicies[platform]
		if !ok {
			t.Fatalf("missing policy for %q", platform)
		}
		if policy.MaxTextCharacters <= 0 || policy.MaxMediaCount <= 0 || policy.MaxMediaBytes <= 0 {
			t.Fatalf("invalid limits for %q: %+v", platform, policy)
		}
		if len(policy.AllowedMediaKinds) == 0 {
			t.Fatalf("missing supported media kinds for %q", platform)
		}
	}
}

func TestValidateText(t *testing.T) {
	if violation := ValidateText("x", "short post"); violation != nil {
		t.Fatalf("unexpected violation: %+v", violation)
	}
	if violation := ValidateText("x", string(make([]rune, 281))); violation == nil || violation.RuleID != "text.max_characters" {
		t.Fatalf("expected character limit violation, got %+v", violation)
	}
	if violation := ValidateText("unknown", "post"); violation == nil || violation.RuleID != "platform.unsupported" {
		t.Fatalf("expected platform violation, got %+v", violation)
	}
}
