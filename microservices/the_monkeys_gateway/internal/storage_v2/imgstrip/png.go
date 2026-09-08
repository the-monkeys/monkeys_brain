package imgstrip

import "bytes"

var pngSig = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

func stripPNG(data []byte) ([]byte, error) {
	if len(data) < 8 || !bytes.Equal(data[:8], pngSig) {
		return nil, fail("invalid png")
	}
	out := append([]byte{}, data[:8]...)
	i := 8
	sawIEND := false
	for i < len(data) {
		if i+8 > len(data) {
			return nil, fail("truncated png")
		}
		n := int(data[i])<<24 | int(data[i+1])<<16 | int(data[i+2])<<8 | int(data[i+3])
		if n < 0 || i+12+n > len(data) {
			return nil, fail("truncated png")
		}
		typ := string(data[i+4 : i+8])
		end := i + 12 + n
		switch typ {
		case "eXIf", "tEXt", "iTXt", "zTXt", "tIME":
			// drop
		default:
			out = append(out, data[i:end]...)
		}
		i = end
		if typ == "IEND" {
			sawIEND = true
			break
		}
	}
	if !sawIEND {
		return nil, fail("truncated png")
	}
	return out, nil
}
