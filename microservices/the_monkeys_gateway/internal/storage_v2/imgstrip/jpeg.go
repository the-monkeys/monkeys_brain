package imgstrip

import (
	"bytes"
	"encoding/binary"
)

func stripJPEG(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, fail("truncated jpeg")
	}

	var kept [][]byte
	var orient uint16
	var haveOrient bool
	var sof *jpegSOF
	foundSOS := false
	i := 2

	for i < len(data) {
		if data[i] != 0xFF {
			return nil, fail("truncated jpeg")
		}
		j := i
		for j < len(data) && data[j] == 0xFF {
			j++
		}
		if j >= len(data) {
			return nil, fail("truncated jpeg")
		}
		marker := data[j]
		if marker == 0x00 {
			return nil, fail("truncated jpeg")
		}
		i = j + 1

		if marker == 0xD9 {
			kept = append(kept, []byte{0xFF, 0xD9})
			break
		}
		if marker == 0xDA {
			rest := data[j-1:]
			if !jpegScanHasEOI(rest) {
				return nil, fail("truncated jpeg")
			}
			kept = append(kept, rest)
			foundSOS = true
			break
		}
		if (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			kept = append(kept, []byte{0xFF, marker})
			continue
		}
		if i+1 >= len(data) {
			return nil, fail("truncated jpeg")
		}
		n := int(binary.BigEndian.Uint16(data[i : i+2]))
		if n < 2 {
			return nil, fail("truncated jpeg")
		}
		end := i + n
		if end > len(data) {
			return nil, fail("truncated jpeg")
		}
		markerStart := j - 1
		payload := data[i+2 : end]
		raw := data[markerStart:end]
		i = end

		switch marker {
		case 0xFE, 0xED:
			continue
		case 0xE1:
			if bytes.HasPrefix(payload, []byte("http://ns.adobe.com/xap")) {
				continue
			}
			if len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
				if o, ok := parseJPEGOrientation(payload[6:]); ok {
					orient = o
					haveOrient = true
				}
			}
			continue
		case 0xC0, 0xC1:
			if s, err := parseJPEGSOF(payload); err == nil {
				sof = s
			}
			kept = append(kept, raw)
		default:
			kept = append(kept, raw)
		}
	}

	if !foundSOS {
		return nil, fail("truncated jpeg")
	}

	mcuAligned := sof != nil && sof.width%8 == 0 && sof.height%8 == 0 && sof.mcuAligned()
	stripped := []byte{0xFF, 0xD8}
	for _, s := range kept {
		stripped = append(stripped, s...)
	}
	if haveOrient && orient >= 2 && orient <= 8 && mcuAligned {
		return losslessJPEGOrient(stripped, sof, orient)
	}

	out := stripped
	if haveOrient && orient >= 2 && orient <= 8 {
		out = append([]byte{0xFF, 0xD8}, orientationOnlyAPP1(orient)...)
		out = append(out, stripped[2:]...)
	}
	return out, nil
}

func jpegScanHasEOI(scan []byte) bool {
	for i := 0; i+1 < len(scan); i++ {
		if scan[i] != 0xFF {
			continue
		}
		m := scan[i+1]
		if m == 0x00 || (m >= 0xD0 && m <= 0xD7) {
			continue
		}
		if m == 0xD9 {
			return true
		}
	}
	return false
}

type jpegSOFComp struct {
	id, h, v, tq uint8
}

type jpegSOF struct {
	width, height int
	comps         []jpegSOFComp
}

func (s *jpegSOF) mcuAligned() bool {
	if s.width <= 0 || s.height <= 0 || len(s.comps) == 0 {
		return false
	}
	maxH, maxV := 1, 1
	for _, c := range s.comps {
		if int(c.h) > maxH {
			maxH = int(c.h)
		}
		if int(c.v) > maxV {
			maxV = int(c.v)
		}
	}
	return s.width%(8*maxH) == 0 && s.height%(8*maxV) == 0
}

func parseJPEGSOF(payload []byte) (*jpegSOF, error) {
	if len(payload) < 6 {
		return nil, fail("truncated jpeg")
	}
	if payload[0] != 8 {
		return nil, fail("unsupported jpeg precision")
	}
	height := int(binary.BigEndian.Uint16(payload[1:3]))
	width := int(binary.BigEndian.Uint16(payload[3:5]))
	n := int(payload[5])
	if n < 1 || n > 4 || len(payload) < 6+3*n {
		return nil, fail("truncated jpeg")
	}
	s := &jpegSOF{width: width, height: height, comps: make([]jpegSOFComp, n)}
	for i := 0; i < n; i++ {
		p := 6 + 3*i
		hv := payload[p+1]
		h, v := hv>>4, hv&0x0f
		if h == 0 || v == 0 {
			return nil, fail("bad jpeg sampling")
		}
		s.comps[i] = jpegSOFComp{id: payload[p], h: h, v: v, tq: payload[p+2]}
	}
	return s, nil
}

func parseJPEGOrientation(tiff []byte) (uint16, bool) {
	if len(tiff) < 8 {
		return 0, false
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "MM":
		bo = binary.BigEndian
	case "II":
		bo = binary.LittleEndian
	default:
		return 0, false
	}
	if bo.Uint16(tiff[2:4]) != 0x002A {
		return 0, false
	}
	ifd := int(bo.Uint32(tiff[4:8]))
	if ifd < 0 || ifd+2 > len(tiff) {
		return 0, false
	}
	n := int(bo.Uint16(tiff[ifd : ifd+2]))
	p := ifd + 2
	for i := 0; i < n; i++ {
		if p+12 > len(tiff) {
			return 0, false
		}
		tag := bo.Uint16(tiff[p : p+2])
		typ := bo.Uint16(tiff[p+2 : p+4])
		count := bo.Uint32(tiff[p+4 : p+8])
		if tag == 0x0112 && count >= 1 {
			switch typ {
			case 3: // SHORT
				return bo.Uint16(tiff[p+8 : p+10]), true
			case 4: // LONG
				return uint16(bo.Uint32(tiff[p+8 : p+12])), true
			}
		}
		p += 12
	}
	return 0, false
}

func orientationOnlyAPP1(orient uint16) []byte {
	tiff := []byte{
		'M', 'M', 0x00, 0x2A,
		0x00, 0x00, 0x00, 0x08,
		0x00, 0x01,
		0x01, 0x12, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01,
		byte(orient >> 8), byte(orient), 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	body := append([]byte("Exif\x00\x00"), tiff...)
	seg := make([]byte, 4+len(body))
	seg[0], seg[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(body)))
	copy(seg[4:], body)
	return seg
}
