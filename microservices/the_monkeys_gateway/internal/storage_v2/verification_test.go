package storage_v2

import (
	"testing"
)

// TestSniffImageType locks down the magic-byte contract used to reject
// masqueraded uploads: declared headers mean nothing, bytes decide.
func TestSniffImageType(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	png := []byte("\x89PNG\r\n\x1a\n" + "rest")
	webp := []byte("RIFF" + "\x00\x00\x00\x00" + "WEBPVP8 ")

	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	html := []byte("<html><script>fetch('/admin')</script></html>")
	heic := []byte("\x00\x00\x00\x18ftypheic")

	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"jpeg", jpeg, "image/jpeg"},
		{"png", png, "image/png"},
		{"webp", webp, "image/webp"},
		{"svg rejected", svg, ""},
		{"html rejected", html, ""},
		{"heic rejected", heic, ""},
		{"empty rejected", nil, ""},
		{"truncated jpeg magic rejected", []byte{0xFF, 0xD8}, ""},
		{"riff without webp rejected", []byte("RIFFxxxxWAVE"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniffImageType(tc.in); got != tc.want {
				t.Fatalf("sniffImageType(% x...) = %q, want %q", headBytes(tc.in), got, tc.want)
			}
		})
	}
}

func headBytes(b []byte) []byte {
	if len(b) > 8 {
		return b[:8]
	}
	return b
}
