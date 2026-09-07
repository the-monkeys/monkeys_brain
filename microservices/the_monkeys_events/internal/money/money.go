package money

import (
	"fmt"
	"math"
)

const (
	CurrencyINR    = "INR"
	PlatformFeeBPS = 500
	GstBPS         = 1800
)

type Split struct {
	GrossPaise       int64
	PlatformFeePaise int64
	GstPaise         int64
	HostPayablePaise int64
	FeeBPS           int
	GstBPS           int
}

func ToPaise(rupees float64) int64 {
	if rupees <= 0 {
		return 0
	}
	return int64(math.Round(rupees * 100))
}

func roundBPS(amount int64, bps int64) int64 {
	if amount == 0 || bps == 0 {
		return 0
	}
	return (amount*bps + 5000) / 10000
}

func FormatINR(paise int64) string {
	return fmt.Sprintf("%.2f", float64(paise)/100)
}

// OpenPayable is captured host_payable minus pending+paid settlements.
func OpenPayable(capturedHostPaise, settledPaise int64) int64 {
	open := capturedHostPaise - settledPaise
	if open < 0 {
		return 0
	}
	return open
}

func SplitGross(grossPaise int64) Split {
	s := Split{FeeBPS: PlatformFeeBPS, GstBPS: GstBPS}
	if grossPaise <= 0 {
		return s
	}
	s.GrossPaise = grossPaise
	s.PlatformFeePaise = roundBPS(grossPaise, PlatformFeeBPS)
	s.GstPaise = roundBPS(s.PlatformFeePaise, GstBPS)
	s.HostPayablePaise = grossPaise - s.PlatformFeePaise - s.GstPaise
	if s.HostPayablePaise < 0 {
		s.HostPayablePaise = 0
	}
	return s
}
