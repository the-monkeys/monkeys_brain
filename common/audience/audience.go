package audience

import "strings"

const (
	AudiencePublic    = "public"
	AudienceGroupOnly = "group_only"
)

func Normalize(audience string) string {
	if audience == AudienceGroupOnly {
		return AudienceGroupOnly
	}
	return AudiencePublic
}

func Coerce(audience, groupVisibility string) string {
	switch groupVisibility {
	case "private", "unlisted":
		return AudienceGroupOnly
	default:
		return Normalize(audience)
	}
}

func IsGroupOnly(docAudience any) bool {
	s, _ := docAudience.(string)
	return s == AudienceGroupOnly
}

func PublicListMustNot() map[string]interface{} {
	return map[string]interface{}{
		"term": map[string]interface{}{"audience": AudienceGroupOnly},
	}
}

func CanRead(audience, ownerAccountID, viewerAccountID string, memberActive bool) bool {
	if Normalize(audience) != AudienceGroupOnly {
		return true
	}
	if viewerAccountID != "" && viewerAccountID == ownerAccountID {
		return true
	}
	return memberActive
}

func AppendPublicListMustNot(mustNot []map[string]interface{}) []map[string]interface{} {
	if mustNot == nil {
		mustNot = []map[string]interface{}{}
	}
	return append(mustNot, PublicListMustNot())
}

func OwnerAccountID(doc map[string]interface{}) string {
	if doc == nil {
		return ""
	}
	for _, key := range []string{"owner_account_id", "OwnerAccountId"} {
		if s, ok := doc[key].(string); ok {
			return s
		}
	}
	return ""
}

func DocAudience(doc map[string]interface{}) string {
	if doc == nil {
		return AudiencePublic
	}
	for _, key := range []string{"audience", "Audience"} {
		if s, ok := doc[key].(string); ok {
			return Normalize(s)
		}
	}
	return AudiencePublic
}

func DocGroupSlug(doc map[string]interface{}) string {
	if doc == nil {
		return ""
	}
	for _, key := range []string{"group_slug", "GroupSlug"} {
		if s, ok := doc[key].(string); ok {
			return s
		}
	}
	return ""
}

func ShouldDetachScheduledGroup(groupSlug string, groupExists, memberActive bool) bool {
	if strings.TrimSpace(groupSlug) == "" {
		return false
	}
	return !groupExists || !memberActive
}
