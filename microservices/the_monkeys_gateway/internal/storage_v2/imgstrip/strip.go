package imgstrip

import (
	"fmt"
	"strings"
)

func Strip(data []byte, contentType string) ([]byte, error) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.Contains(ct, "image/jpeg"), strings.Contains(ct, "image/jpg"):
		return stripJPEG(data)
	case strings.Contains(ct, "image/png"):
		return stripPNG(data)
	case strings.Contains(ct, "image/webp"):
		return stripWebP(data)
	default:
		return data, nil
	}
}

func fail(format string, args ...any) error {
	return fmt.Errorf("imgstrip: "+format, args...)
}
