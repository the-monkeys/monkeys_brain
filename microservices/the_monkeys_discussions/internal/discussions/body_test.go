package discussions

import "testing"

func TestValidateBodyTrimsAndCapsRunes(t *testing.T) {
	got, err := ValidateBody("  hello  ", 0)
	if err != nil || got != "hello" {
		t.Fatalf("got %q err %v", got, err)
	}
	long := make([]rune, 501)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := ValidateBody(string(long), 0); err == nil {
		t.Fatal("501 runes must be rejected")
	}
}

func TestValidateBodyAllowsEmptyWhenImageAttached(t *testing.T) {
	got, err := ValidateBody("   ", 1)
	if err != nil || got != "" {
		t.Fatalf("empty with image: got %q err %v", got, err)
	}
	if _, err := ValidateBody("  ", 0); err == nil {
		t.Fatal("empty without image must be rejected")
	}
}

func TestValidateFilesCapsAtFourImages(t *testing.T) {
	ok := []File{
		{StorageKey: "a", ContentType: "image/jpeg"},
		{StorageKey: "b", ContentType: "image/png"},
		{StorageKey: "c", ContentType: "image/webp"},
		{StorageKey: "d", ContentType: "image/gif"},
	}
	if err := ValidateFiles(ok); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFiles(append(ok, File{ContentType: "image/png"})); err == nil {
		t.Fatal("five images must be rejected")
	}
	if err := ValidateFiles([]File{{StorageKey: "p", ContentType: "application/pdf"}}); err == nil {
		t.Fatal("pdf must be rejected")
	}
	if err := ValidateFiles([]File{{StorageKey: "v", ContentType: "video/mp4"}}); err == nil {
		t.Fatal("video must be rejected")
	}
}

func TestVisibleBodyHidesDeletedTextFromOthers(t *testing.T) {
	if got := VisibleBody("secret", "deleted", false); got != "" {
		t.Fatalf("others see %q", got)
	}
	if got := VisibleBody("secret", "deleted", true); got != "secret" {
		t.Fatalf("author sees %q", got)
	}
	if got := VisibleBody("hello", "visible", false); got != "hello" {
		t.Fatalf("visible body %q", got)
	}
}