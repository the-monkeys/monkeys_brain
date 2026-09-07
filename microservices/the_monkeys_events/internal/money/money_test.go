package money

import "testing"

func TestSplitGross1000INR(t *testing.T) {
	s := SplitGross(100_000)
	if s.PlatformFeePaise != 5000 || s.GstPaise != 900 || s.HostPayablePaise != 94_100 {
		t.Fatalf("got %+v", s)
	}
	if s.FeeBPS != PlatformFeeBPS || s.GstBPS != GstBPS {
		t.Fatalf("bps not snapshotted: %+v", s)
	}
}

func TestSplitGrossZero(t *testing.T) {
	s := SplitGross(0)
	if s != (Split{FeeBPS: PlatformFeeBPS, GstBPS: GstBPS}) {
		t.Fatalf("got %+v", s)
	}
}

func TestToPaise(t *testing.T) {
	if ToPaise(499) != 49900 {
		t.Fatal("499 rupees")
	}
	if ToPaise(1.15) != 115 {
		t.Fatal("1.15")
	}
}

func TestFormatINR(t *testing.T) {
	if FormatINR(94_100) != "941.00" {
		t.Fatalf("got %q", FormatINR(94_100))
	}
}

func TestOpenPayable(t *testing.T) {
	if OpenPayable(94_100, 0) != 94_100 {
		t.Fatal("full open")
	}
	if OpenPayable(94_100, 94_100) != 0 {
		t.Fatal("fully settled")
	}
	if OpenPayable(94_100, 100_000) != 0 {
		t.Fatal("must not go negative")
	}
}

func TestSplitGrossOddPaiseIdentity(t *testing.T) {
	s := SplitGross(333)
	if s.PlatformFeePaise+s.GstPaise+s.HostPayablePaise != 333 {
		t.Fatalf("split must cover gross 333, got %+v", s)
	}
}
