package geo

import "testing"

func TestClampSearchRadius(t *testing.T) {
	tests := []struct {
		in    int32
		want  int32
		apply bool
	}{
		{0, 0, false},
		{-5, 0, false},
		{1, 2, true},
		{2, 2, true},
		{25, 25, true},
		{100, 100, true},
		{250, 100, true},
	}
	for _, tc := range tests {
		got, apply := ClampSearchRadius(tc.in)
		if apply != tc.apply || got != tc.want {
			t.Fatalf("ClampSearchRadius(%d) = (%d, %v), want (%d, %v)",
				tc.in, got, apply, tc.want, tc.apply)
		}
	}
}

func TestUseClientPin(t *testing.T) {
	if UseClientPin(0, 0) || UseClientPin(12.97, 0) || UseClientPin(0, 77.59) {
		t.Fatal("zero on either axis is not a pin")
	}
	if !UseClientPin(12.97, 77.59) {
		t.Fatal("both non-zero is a pin")
	}
}
