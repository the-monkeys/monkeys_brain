package imgstrip

import (
	"bytes"
	"encoding/binary"
)

func stripWebP(data []byte) ([]byte, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, fail("invalid webp")
	}

	var chunks bytes.Buffer
	chunks.WriteString("WEBP")
	i := 12
	for i+8 <= len(data) {
		fourcc := string(data[i : i+4])
		sz := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		start := i + 8
		if sz < 0 || start+sz > len(data) {
			return nil, fail("truncated webp")
		}
		payload := append([]byte{}, data[start:start+sz]...)
		pad := 0
		if sz%2 == 1 {
			pad = 1
			if start+sz+pad > len(data) {
				return nil, fail("truncated webp")
			}
		}
		i = start + sz + pad

		switch fourcc {
		case "EXIF", "XMP ":
			continue
		case "VP8X":
			if len(payload) > 0 {
				payload[0] &^= 0x08 | 0x04 // EXIF, XMP
			}
		}
		writeWebPChunk(&chunks, fourcc, payload)
	}

	body := chunks.Bytes()
	out := make([]byte, 8+len(body))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(body)))
	copy(out[8:], body)
	return out, nil
}

func writeWebPChunk(buf *bytes.Buffer, fourcc string, payload []byte) {
	buf.WriteString(fourcc)
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(len(payload)))
	buf.Write(sz[:])
	buf.Write(payload)
	if len(payload)%2 == 1 {
		buf.WriteByte(0)
	}
}
