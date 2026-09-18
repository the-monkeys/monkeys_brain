package models

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type PlatformPolicy struct {
	MaxTextCharacters int
	AllowedMediaKinds []string
	MaxMediaCount     int
	MaxMediaBytes     int64
	MaxVideoDuration  int64
	MediaRequired     bool
}

var PlatformPolicies = map[string]PlatformPolicy{
	"x":         {280, []string{"image", "video"}, 4, 512 << 20, 140_000, false},
	"linkedin":  {3_000, []string{"image", "video", "audio"}, 9, 5 << 30, 600_000, false},
	"instagram": {2_200, []string{"image", "video"}, 10, 4 << 30, 3_600_000, true},
	"facebook":  {63_206, []string{"image", "video"}, 10, 10 << 30, 14_400_000, false},
	"youtube":   {5_000, []string{"video"}, 1, 256 << 30, 43_200_000, true},
	"tiktok":    {2_200, []string{"video"}, 1, 4 << 30, 600_000, true},
}

type Violation struct {
	Field, RuleID, Message, Actual, Allowed string
}

func ValidateText(platform, text string) *Violation {
	policy, ok := PlatformPolicies[platform]
	if !ok {
		return &Violation{Field: "platform", RuleID: "platform.unsupported", Message: "unsupported platform", Actual: platform}
	}
	length := utf8.RuneCountInString(strings.TrimSpace(text))
	if length > policy.MaxTextCharacters {
		return &Violation{
			Field: "text", RuleID: "text.max_characters", Message: "text exceeds platform limit",
			Actual: fmt.Sprintf("%d", length), Allowed: fmt.Sprintf("%d", policy.MaxTextCharacters),
		}
	}
	return nil
}
