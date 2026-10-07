package discussions

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxRunes = 500

// File is an image already stored in MinIO. The key is persisted; the bytes are not.
type File struct {
	StorageKey  string
	ContentType string
}

var imageTypes = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
	"image/gif":  {},
}

// ValidateBody trims the text and enforces the 500-rune cap.
// An empty body is allowed only when the post has at least one image.
func ValidateBody(body string, fileCount int) (string, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" && fileCount == 0 {
		return "", fmt.Errorf("write something or attach an image")
	}
	if utf8.RuneCountInString(trimmed) > maxRunes {
		return "", fmt.Errorf("a discussion can be at most 500 characters")
	}
	return trimmed, nil
}

// ValidateFiles accepts up to four images. PDF and video are rejected.
func ValidateFiles(files []File) error {
	if len(files) > 4 {
		return fmt.Errorf("you can attach up to 4 images")
	}
	for _, f := range files {
		if _, ok := imageTypes[f.ContentType]; !ok {
			return fmt.Errorf("only JPEG, PNG, WebP, and GIF images can be attached")
		}
		if strings.TrimSpace(f.StorageKey) == "" {
			return fmt.Errorf("an image is missing its storage key")
		}
	}
	return nil
}
