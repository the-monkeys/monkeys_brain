package consumer

import "strings"

// entityStoragePrefix is the MinIO prefix for an event or group folder.
// Rejects empty slugs and path traversal so a delete message cannot wipe
// sibling keys.
func entityStoragePrefix(kind, slug string) (string, bool) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "", false
	}
	if strings.Contains(slug, "/") || strings.Contains(slug, "\\") || strings.Contains(slug, "..") {
		return "", false
	}
	return kind + "/" + slug + "/", true
}
