package consumer

import "testing"

func TestEntityStoragePrefix(t *testing.T) {
	got, ok := entityStoragePrefix("events", "tea-talk")
	if !ok || got != "events/tea-talk/" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	got, ok = entityStoragePrefix("groups", "book-club")
	if !ok || got != "groups/book-club/" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := entityStoragePrefix("events", ""); ok {
		t.Fatal("empty slug")
	}
	if _, ok := entityStoragePrefix("events", "a/../b"); ok {
		t.Fatal("slash/dotdot slug")
	}
	if _, ok := entityStoragePrefix("events", "../x"); ok {
		t.Fatal("dotdot slug")
	}
}
