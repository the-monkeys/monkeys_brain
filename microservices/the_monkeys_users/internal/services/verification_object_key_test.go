package services

import "testing"

func TestVerificationObjectKey(t *testing.T) {
	if verificationObjectKey("abc") != "verifications/sha256/abc" {
		t.Fatal("path must match private bucket layout")
	}
	if verificationObjectKey("") != "" {
		t.Fatal("empty checksum → empty key")
	}
}
