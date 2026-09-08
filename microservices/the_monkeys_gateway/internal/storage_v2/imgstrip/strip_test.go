package imgstrip

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestEmitHuffRLECoeffOverflow(t *testing.T) {
	w := &bitWriter{}
	if err := w.emitHuffRLE(0, 0, 65536); err == nil {
		t.Fatal("coeff >= 65536 must error, not panic")
	}
	if err := w.emitHuffRLE(1, 0, -70000); err == nil {
		t.Fatal("large negative coeff must error")
	}
}

func TestEmitHuffBadIndex(t *testing.T) {
	w := &bitWriter{}
	if err := w.emitHuff(0, 999); err == nil {
		t.Fatal("out-of-range huffman index must error")
	}
}

func TestEncodeJPEGCoeffsRejectsHugeCoeff(t *testing.T) {
	var blk [64]int32
	blk[0] = 70000
	fr := &jpegCoeffFrame{
		width: 8, height: 8,
		comps: []jpegCompBlocks{{
			id: 1, h: 1, v: 1, tq: 0,
			bx: 1, by: 1,
			blocks: [][64]int32{blk},
		}},
	}
	fr.dqtOK[0] = true
	_, err := encodeJPEGCoeffs(fr)
	if err == nil {
		t.Fatal("encode must reject huge DC coefficient")
	}
}

func TestStripJPEGDropsGPSMarkerPayload(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// SOI then APP1. Length is 2 length-bytes + 4-byte payload (not the marker).
	app1 := []byte{0xFF, 0xE1, 0x00, 0x06, 'G', 'P', 'S', 0x00}
	injected := append([]byte{0xFF, 0xD8}, append(app1, raw[2:]...)...)
	out, err := Strip(injected, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Fatal("GPS payload must be gone")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped jpeg must still decode: %v", err)
	}
}

func TestStripPNGDropsTextChunk(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	// Valid tests in png.go should drop tEXt; this fixture asserts API shape.
	out, err := Strip(buf.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped png must decode: %v", err)
	}
}

func TestStripNonImageUnchanged(t *testing.T) {
	in := []byte("not-an-image")
	out, err := Strip(in, "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatal("non-images must pass through")
	}
}

func TestStripGIFUnchanged(t *testing.T) {
	in := []byte("GIF89a-not-parsed")
	out, err := Strip(in, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatal("GIF must pass through")
	}
}

func TestStripPNGRemovesTEXt(t *testing.T) {
	raw := pngWithTextChunk(t)
	if !bytes.Contains(raw, []byte("tEXt")) {
		t.Fatal("fixture must contain tEXt")
	}
	out, err := Strip(raw, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("tEXt")) {
		t.Fatal("tEXt chunk must be dropped")
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped png must decode: %v", err)
	}
}

func TestStripJPEGOrientation1DropsAPP1(t *testing.T) {
	raw := jpegWithExifAPP1(t, 1, true)
	out, err := Strip(raw, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Fatal("GPS payload must be gone")
	}
	if hasJPEGAPP1(out) {
		t.Fatal("orientation 1 must drop APP1")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped jpeg must still decode: %v", err)
	}
}

func TestStripJPEGOrientationKeepsStubDropsGPS(t *testing.T) {
	raw := jpegWithExifAPP1(t, 6, true)
	out, err := Strip(raw, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Fatal("GPS payload must be gone")
	}
	if bytes.Contains(out, []byte("http://ns.adobe.com/xap")) {
		t.Fatal("XMP must be gone")
	}
	orient, ok := jpegAPP1Orientation(out)
	if !ok || orient != 6 {
		t.Fatalf("orientation 6 must keep orientation-only Exif, got ok=%v orient=%d", ok, orient)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped jpeg must still decode: %v", err)
	}
}

func TestStripJPEGRestartMarkersStillDropsGPS(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			src.Set(x, y, color.RGBA{R: 40, G: 80, B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	injected := jpegInjectDRI(jpegInjectExif(buf.Bytes(), 6, true), 16)
	out, err := Strip(injected, "image/jpeg")
	if err != nil {
		t.Fatalf("iPhone-style restart JPEGs must still strip, got %v", err)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Fatal("GPS payload must be gone")
	}
	orient, ok := jpegAPP1Orientation(out)
	if !ok || orient != 6 {
		t.Fatalf("when lossless rotate cannot apply, keep orientation stub, got ok=%v orient=%d", ok, orient)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped jpeg must still decode: %v", err)
	}
}

func TestJPEGLosslessBudgetSkipsPhoneSizedJPEGs(t *testing.T) {
	phone := &jpegSOF{width: 4032, height: 3024, comps: []jpegSOFComp{{h: 2, v: 2}, {h: 1, v: 1}, {h: 1, v: 1}}}
	if jpegLosslessBudgetOK(phone, 3*1024*1024) {
		t.Fatal("3MiB phone JPEGs must skip DCT rotate")
	}
	tiny := &jpegSOF{width: 16, height: 16, comps: []jpegSOFComp{{h: 1, v: 1}}}
	if !jpegLosslessBudgetOK(tiny, 2048) {
		t.Fatal("fixture MCU JPEGs must still attempt lossless rotate")
	}
}

func TestStripHEICRejected(t *testing.T) {
	heic := []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00")
	_, err := Strip(heic, "image/heic")
	if err == nil {
		t.Fatal("HEIC must be rejected")
	}
	if !IsUnsupportedFormat(err) {
		t.Fatalf("want unsupported-format error, got %v", err)
	}
	_, err = Strip(heic, "image/jpeg")
	if err == nil {
		t.Fatal("renamed HEIC must still be rejected")
	}
}

func TestStripAVIFMif1Allowed(t *testing.T) {
	avif := []byte{
		0x00, 0x00, 0x00, 0x14,
		'f', 't', 'y', 'p',
		'm', 'i', 'f', '1',
		0x00, 0x00, 0x00, 0x00,
		'a', 'v', 'i', 'f',
	}
	out, err := Strip(avif, "image/avif")
	if err != nil {
		t.Fatalf("AVIF with mif1 major brand must pass through, got %v", err)
	}
	if !bytes.Equal(out, avif) {
		t.Fatal("AVIF bytes must be unchanged")
	}
}

func TestStripHEICMif1StillRejected(t *testing.T) {
	heif := []byte{
		0x00, 0x00, 0x00, 0x18,
		'f', 't', 'y', 'p',
		'm', 'i', 'f', '1',
		0x00, 0x00, 0x00, 0x00,
		'm', 'i', 'f', '1',
		'h', 'e', 'i', 'c',
	}
	_, err := Strip(heif, "application/octet-stream")
	if err == nil {
		t.Fatal("HEIC with mif1 major brand must still be rejected")
	}
	if !IsUnsupportedFormat(err) {
		t.Fatalf("want unsupported-format error, got %v", err)
	}
}

func TestStripJPEGMCUAlignedOrientation6RotatesPixels(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			src.Set(x, y, color.RGBA{A: 255})
		}
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			src.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	orig, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	injected := jpegInjectExif(raw, 6, true)
	out, err := Strip(injected, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Fatal("GPS payload must be gone")
	}
	if hasJPEGAPP1(out) {
		t.Fatal("MCU-aligned orientation 6 must omit APP1 after lossless rotate")
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("stripped jpeg must still decode: %v", err)
	}
	want := rotate90CW(orig)
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds: got %v want %v", got.Bounds(), want.Bounds())
	}
	if !imagesApproxEqual(got, want) {
		t.Fatal("pixels must match 90° CW of the stored image, without using EXIF")
	}
}

func TestStripPNGRejectsMissingIEND(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	iend := bytes.Index(raw, []byte("IEND"))
	if iend < 4 {
		t.Fatal("fixture must contain IEND")
	}
	truncated := raw[:iend-4]
	_, err := Strip(truncated, "image/png")
	if err == nil {
		t.Fatal("PNG truncated before IEND must error")
	}
}

func TestStripJPEGTruncated(t *testing.T) {
	_, err := Strip([]byte{0xFF, 0xD8, 0xFF}, "image/jpeg")
	if err == nil {
		t.Fatal("truncated jpeg must error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("truncated jpeg")) {
		t.Fatalf("want truncated jpeg error, got %v", err)
	}
}

func TestStripWebPDropsEXIFAndXMP(t *testing.T) {
	in := webpWithEXIFAndXMP()
	out, err := Strip(in, "image/webp")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("EXIF")) {
		t.Fatal("EXIF chunk fourcc must be gone")
	}
	if bytes.Contains(out, []byte("XMP ")) {
		t.Fatal("XMP chunk fourcc must be gone")
	}
	if bytes.Contains(out, []byte("gps-secret")) {
		t.Fatal("EXIF payload must be gone")
	}
}

func TestStripWebPNotRIFF(t *testing.T) {
	_, err := Strip([]byte("not-riff"), "image/webp")
	if err == nil {
		t.Fatal("non-RIFF webp must error")
	}
}

func pngWithTextChunk(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	const sig = 8
	if len(raw) < sig+4+4+13+4 {
		t.Fatal("short png")
	}
	ihdrLen := binary.BigEndian.Uint32(raw[sig : sig+4])
	ihdrEnd := sig + 4 + 4 + int(ihdrLen) + 4
	text := append([]byte("Comment\x00"), []byte("secret")...)
	chunk := pngChunk("tEXt", text)
	out := append([]byte{}, raw[:ihdrEnd]...)
	out = append(out, chunk...)
	out = append(out, raw[ihdrEnd:]...)
	return out
}

func pngChunk(typ string, data []byte) []byte {
	buf := make([]byte, 8+len(data)+4)
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(data)))
	copy(buf[4:8], typ)
	copy(buf[8:8+len(data)], data)
	crc := crc32.ChecksumIEEE(buf[4 : 8+len(data)])
	binary.BigEndian.PutUint32(buf[8+len(data):], crc)
	return buf
}

func jpegInjectDRI(raw []byte, interval uint16) []byte {
	sos := bytes.Index(raw, []byte{0xFF, 0xDA})
	if sos < 0 {
		return raw
	}
	dri := []byte{0xFF, 0xDD, 0x00, 0x04, byte(interval >> 8), byte(interval)}
	out := make([]byte, 0, len(raw)+len(dri))
	out = append(out, raw[:sos]...)
	out = append(out, dri...)
	out = append(out, raw[sos:]...)
	return out
}

func jpegWithExifAPP1(t *testing.T, orientation uint16, withGPS bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return jpegInjectExif(buf.Bytes(), orientation, withGPS)
}

func jpegInjectExif(raw []byte, orientation uint16, withGPS bool) []byte {
	exif := buildExifAPP1(orientation, withGPS)
	xmpPayload := []byte("http://ns.adobe.com/xap\x00")
	xmp := make([]byte, 4+len(xmpPayload))
	xmp[0], xmp[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(xmp[2:4], uint16(2+len(xmpPayload)))
	copy(xmp[4:], xmpPayload)
	out := append([]byte{0xFF, 0xD8}, exif...)
	out = append(out, xmp...)
	out = append(out, raw[2:]...)
	return out
}

func rotate90CW(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func imagesApproxEqual(a, b image.Image) bool {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return false
	}
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			ar, ag, av, _ := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			br, bg, bv, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			if absDiff(ar, br) > 256*3 || absDiff(ag, bg) > 256*3 || absDiff(av, bv) > 256*3 {
				return false
			}
		}
	}
	return true
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func buildExifAPP1(orientation uint16, withGPS bool) []byte {
	// TIFF at offset 0 after Exif\0\0.
	tiff := make([]byte, 8)
	copy(tiff, []byte("MM\x00\x2a"))
	binary.BigEndian.PutUint32(tiff[4:8], 8)

	n := 1
	if withGPS {
		n = 2
	}
	ifd := make([]byte, 2+12*n+4)
	binary.BigEndian.PutUint16(ifd[0:2], uint16(n))
	// tag 0x0112 Orientation SHORT count 1
	binary.BigEndian.PutUint16(ifd[2:4], 0x0112)
	binary.BigEndian.PutUint16(ifd[4:6], 3)
	binary.BigEndian.PutUint32(ifd[6:10], 1)
	binary.BigEndian.PutUint16(ifd[10:12], orientation)
	off := 2 + 12
	if withGPS {
		gpsIFDOff := uint32(8 + 2 + 12*n + 4)
		binary.BigEndian.PutUint16(ifd[off:off+2], 0x8825)
		binary.BigEndian.PutUint16(ifd[off+2:off+4], 4)
		binary.BigEndian.PutUint32(ifd[off+4:off+8], 1)
		binary.BigEndian.PutUint32(ifd[off+8:off+12], gpsIFDOff)
		off += 12
	}
	binary.BigEndian.PutUint32(ifd[off:off+4], 0)
	tiff = append(tiff, ifd...)
	if withGPS {
		// GPS IFD with ASCII payload "GPS" so Strip must not leak it.
		gps := make([]byte, 2+12+4+4)
		binary.BigEndian.PutUint16(gps[0:2], 1)
		binary.BigEndian.PutUint16(gps[2:4], 0x0012) // GPSMapDatum
		binary.BigEndian.PutUint16(gps[4:6], 2)      // ASCII
		binary.BigEndian.PutUint32(gps[6:10], 4)
		binary.BigEndian.PutUint32(gps[10:14], uint32(8+len(ifd)+2+12+4))
		binary.BigEndian.PutUint32(gps[14:18], 0)
		copy(gps[18:22], []byte("GPS\x00"))
		tiff = append(tiff, gps...)
	}
	body := append([]byte("Exif\x00\x00"), tiff...)
	seg := make([]byte, 4+len(body))
	seg[0], seg[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(body)))
	copy(seg[4:], body)
	return seg
}

func hasJPEGAPP1(data []byte) bool {
	i := 2
	for i+1 < len(data) {
		if data[i] != 0xFF {
			return false
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return false
		}
		marker := data[i]
		i++
		if marker == 0xD9 || marker == 0xDA {
			return false
		}
		if marker >= 0xD0 && marker <= 0xD7 {
			continue
		}
		if i+1 >= len(data) {
			return false
		}
		n := int(binary.BigEndian.Uint16(data[i : i+2]))
		if marker == 0xE1 {
			return true
		}
		i += n
	}
	return false
}

func jpegAPP1Orientation(data []byte) (uint16, bool) {
	i := 2
	for i+1 < len(data) {
		if data[i] != 0xFF {
			return 0, false
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return 0, false
		}
		marker := data[i]
		i++
		if marker == 0xD9 || marker == 0xDA {
			return 0, false
		}
		if marker >= 0xD0 && marker <= 0xD7 {
			continue
		}
		if i+1 >= len(data) {
			return 0, false
		}
		n := int(binary.BigEndian.Uint16(data[i : i+2]))
		if n < 2 || i+n > len(data) {
			return 0, false
		}
		payload := data[i+2 : i+n]
		i += n
		if marker != 0xE1 {
			continue
		}
		if len(payload) < 14 || string(payload[:6]) != "Exif\x00\x00" {
			continue
		}
		return tiffOrientation(payload[6:])
	}
	return 0, false
}

func tiffOrientation(tiff []byte) (uint16, bool) {
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
	ifd := int(bo.Uint32(tiff[4:8]))
	if ifd+2 > len(tiff) {
		return 0, false
	}
	n := int(bo.Uint16(tiff[ifd : ifd+2]))
	p := ifd + 2
	for i := 0; i < n; i++ {
		if p+12 > len(tiff) {
			return 0, false
		}
		tag := bo.Uint16(tiff[p : p+2])
		if tag == 0x0112 {
			typ := bo.Uint16(tiff[p+2 : p+4])
			if typ == 3 {
				return bo.Uint16(tiff[p+8 : p+10]), true
			}
			return 0, false
		}
		p += 12
	}
	return 0, false
}

func webpWithEXIFAndXMP() []byte {
	// RIFF/WEBP with VP8X (EXIF+XMP flags), a dummy VP8 chunk, EXIF, and XMP.
	vp8x := make([]byte, 10)
	vp8x[0] = 0x08 | 0x04 // EXIF | XMP
	vp8 := []byte{0x00, 0x00, 0x00}
	exif := []byte("gps-secret")
	xmp := []byte("<x:xmpmeta/>")

	var payload bytes.Buffer
	payload.WriteString("WEBP")
	appendWebPChunk(&payload, "VP8X", vp8x)
	appendWebPChunk(&payload, "VP8 ", vp8)
	appendWebPChunk(&payload, "EXIF", exif)
	appendWebPChunk(&payload, "XMP ", xmp)

	body := payload.Bytes()
	out := make([]byte, 8+len(body))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(body)))
	copy(out[8:], body)
	return out
}

func appendWebPChunk(buf *bytes.Buffer, fourcc string, data []byte) {
	buf.WriteString(fourcc)
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(len(data)))
	buf.Write(sz[:])
	buf.Write(data)
	if len(data)%2 == 1 {
		buf.WriteByte(0)
	}
}
