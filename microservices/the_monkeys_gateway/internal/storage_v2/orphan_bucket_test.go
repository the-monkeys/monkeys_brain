package storage_v2

import "testing"

func TestMinioBucketForObjectKey(t *testing.T) {
	if minioBucketForObjectKey("pub", "ver", "assets/sha256/ab/cd/ab.bin") != "pub" {
		t.Fatal("cas assets use public bucket")
	}
	if minioBucketForObjectKey("pub", "ver", "verifications/sha256/deadbeef") != "ver" {
		t.Fatal("verification keys use private bucket")
	}
}
