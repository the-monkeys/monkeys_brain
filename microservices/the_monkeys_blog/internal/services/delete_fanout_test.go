package services

import (
	"net/http"
	"testing"
)

func TestESDeleteOK(t *testing.T) {
	if !esDeleteOK(http.StatusOK) {
		t.Fatal("200 should be success")
	}
	if !esDeleteOK(http.StatusNotFound) {
		t.Fatal("404 should be success so orphans still fan out")
	}
	if esDeleteOK(http.StatusInternalServerError) {
		t.Fatal("500 should not be success")
	}
	if esDeleteOK(http.StatusBadRequest) {
		t.Fatal("400 should not be success")
	}
}

func TestBlogDeleteFanOutKeys(t *testing.T) {
	// Matches .env.example: key1,key2,blog_svc_file_svc_key,to_blog_svc_key,...
	keys := []string{"key1", "key2", "blog_svc_file_svc_key", "to_blog_svc_key"}
	users, storage, ok := blogDeleteFanOutKeys(keys)
	if !ok {
		t.Fatal("expected ok")
	}
	if users != "key2" {
		t.Fatalf("users key = %q", users)
	}
	if storage != "blog_svc_file_svc_key" {
		t.Fatalf("storage key = %q", storage)
	}

	if _, _, ok := blogDeleteFanOutKeys([]string{"only-one"}); ok {
		t.Fatal("short slice should not be ok")
	}
	if _, _, ok := blogDeleteFanOutKeys(nil); ok {
		t.Fatal("nil slice should not be ok")
	}
}
