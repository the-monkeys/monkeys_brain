package imgstrip

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupportedFormat = errors.New("imgstrip: heic not supported; convert to JPEG, PNG or WebP")

func Strip(data []byte, contentType string) ([]byte, error) {
	if err := RejectIfUnsupported(data, contentType); err != nil {
		return nil, err
	}
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

func RejectIfUnsupported(data []byte, contentType string) error {
	if looksLikeHEIC(data) || heicContentType(contentType) {
		return ErrUnsupportedFormat
	}
	return nil
}

func IsUnsupportedFormat(err error) bool {
	return errors.Is(err, ErrUnsupportedFormat)
}

func looksLikeHEIC(data []byte) bool {
	brands := ftypBrands(data)
	if len(brands) == 0 {
		return false
	}
	hasAVIF, hasHEIC := false, false
	for _, b := range brands {
		switch b {
		case "avif", "avis":
			hasAVIF = true
		case "heic", "heix", "heif", "hevc", "hevx":
			hasHEIC = true
		}
	}
	return hasHEIC && !hasAVIF
}

func ftypBrands(data []byte) []string {
	if len(data) < 16 || string(data[4:8]) != "ftyp" {
		return nil
	}
	size := int(binary.BigEndian.Uint32(data[0:4]))
	if size < 16 {
		return nil
	}
	if size > len(data) {
		size = len(data)
	}
	if size > 256 {
		size = 256
	}
	brands := []string{string(data[8:12])}
	for i := 16; i+4 <= size; i += 4 {
		brands = append(brands, string(data[i:i+4]))
	}
	return brands
}

func heicContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	switch ct {
	case "image/heic", "image/heif", "image/heic-sequence", "image/heif-sequence":
		return true
	default:
		return false
	}
}

func fail(format string, args ...any) error {
	return fmt.Errorf("imgstrip: "+format, args...)
}
