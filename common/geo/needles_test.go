package geo

import (
	"strings"
	"testing"
)

func TestUnpinnedNeedlesEmpty(t *testing.T) {
	if UnpinnedNeedles("  ") != nil {
		t.Fatal("blank city must skip needles")
	}
}

func TestUnpinnedNeedlesUnknownCity(t *testing.T) {
	got := UnpinnedNeedles("Paris")
	if len(got) != 1 || got[0] != "%Paris%" {
		t.Fatalf("unknown city must stay a single ILIKE, got %v", got)
	}
}

func TestUnpinnedNeedlesBengaluruIncludesBellandur(t *testing.T) {
	for _, city := range []string{"Bengaluru", "Bangalore", "Bengaluru, India"} {
		got := UnpinnedNeedles(city)
		if !containsFold(got, "%bellandur%") {
			t.Fatalf("%q must match Bellandur venues, got %v", city, got)
		}
		if !containsFold(got, "%bangalore%") || !containsFold(got, "%bengaluru%") {
			t.Fatalf("%q must include both city spellings, got %v", city, got)
		}
	}
}

func containsFold(needles []string, want string) bool {
	for _, n := range needles {
		if strings.EqualFold(n, want) {
			return true
		}
	}
	return false
}
