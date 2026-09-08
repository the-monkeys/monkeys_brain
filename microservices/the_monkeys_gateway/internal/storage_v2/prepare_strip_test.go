package storage_v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/storage_v2/imgstrip"
)

type memMultipartFile struct {
	*bytes.Reader
}

func (m *memMultipartFile) Close() error { return nil }

func jpegWithGPSAPP1(t *testing.T) []byte {
	t.Helper()
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
	app1 := []byte{0xFF, 0xE1, 0x00, 0x06, 'G', 'P', 'S', 0x00}
	return append([]byte{0xFF, 0xD8}, append(app1, raw[2:]...)...)
}

func TestChecksumUsesStrippedBytes(t *testing.T) {
	raw := jpegWithGPSAPP1(t)
	stripped, err := imgstrip.Strip(raw, "image/jpeg")
	if err != nil {
		t.Fatalf("fixture must strip: %v", err)
	}
	if !bytes.Contains(raw, []byte("GPS")) {
		t.Fatal("fixture must contain GPS")
	}
	if bytes.Contains(stripped, []byte("GPS")) {
		t.Fatal("stripped bytes must not contain GPS")
	}

	sumRaw := sha256.Sum256(raw)
	sumStrip := sha256.Sum256(stripped)
	rawHex := hex.EncodeToString(sumRaw[:])
	stripHex := hex.EncodeToString(sumStrip[:])
	if rawHex == stripHex {
		t.Fatal("stripped checksum must differ when GPS was removed")
	}

	s := &Service{log: zap.NewNop().Sugar()}
	fh := &multipart.FileHeader{Size: int64(len(raw))}
	prepared, err := s.prepareAssetUpload(&memMultipartFile{bytes.NewReader(raw)}, fh, "image/jpeg")
	if err != nil {
		t.Fatalf("prepareAssetUpload: %v", err)
	}
	if prepared.cleanup != nil {
		defer prepared.cleanup()
	}
	if prepared.checksum != stripHex {
		t.Fatalf("checksum must be of stripped bytes, got %s want %s", prepared.checksum, stripHex)
	}
	if prepared.checksum == rawHex {
		t.Fatal("checksum must not be of raw GPS jpeg")
	}
	got, err := io.ReadAll(prepared.reader)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("GPS")) {
		t.Fatal("prepared reader must not contain GPS")
	}
	if !bytes.Equal(got, stripped) {
		t.Fatal("prepared body must be stripped bytes")
	}
}

func TestPrepareAssetUploadRejectsHEIC(t *testing.T) {
	s := &Service{log: zap.NewNop().Sugar()}
	heic := []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00")
	fh := &multipart.FileHeader{Size: int64(len(heic))}
	_, err := s.prepareAssetUpload(&memMultipartFile{bytes.NewReader(heic)}, fh, "image/heic")
	if err == nil {
		t.Fatal("prepareAssetUpload must reject HEIC")
	}
	if !imgstrip.IsUnsupportedFormat(err) {
		t.Fatalf("want unsupported-format error, got %v", err)
	}

	_, err = s.prepareAssetUpload(&memMultipartFile{bytes.NewReader(heic)}, fh, "application/octet-stream")
	if err == nil {
		t.Fatal("blog CAS must reject HEIC even without an image Content-Type")
	}
	if !imgstrip.IsUnsupportedFormat(err) {
		t.Fatalf("octet-stream HEIC: want unsupported-format error, got %v", err)
	}
}

func TestPrepareAssetUploadRejectsStripError(t *testing.T) {
	s := &Service{log: zap.NewNop().Sugar()}
	payload := []byte{0xFF}
	fh := &multipart.FileHeader{Size: int64(len(payload))}
	_, err := s.prepareAssetUpload(&memMultipartFile{bytes.NewReader(payload)}, fh, "image/jpeg")
	if err == nil {
		t.Fatal("prepareAssetUpload must return Strip error for invalid jpeg")
	}
}

func TestPreparePathImageStripsWhenSizeOverMetadataLimit(t *testing.T) {
	raw := jpegWithGPSAPP1(t)
	stripped, err := imgstrip.Strip(raw, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}

	s := &Service{log: zap.NewNop().Sugar()}
	fh := &multipart.FileHeader{Size: imageMetadataLimit + 1}
	prepared, err := s.preparePathImage(&memMultipartFile{bytes.NewReader(raw)}, fh, "image/jpeg")
	if err != nil {
		t.Fatalf("preparePathImage: %v", err)
	}
	got, err := io.ReadAll(prepared.reader)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("GPS")) {
		t.Fatal("body must still be stripped when FileHeader.Size > 5MiB")
	}
	if !bytes.Equal(got, stripped) {
		t.Fatal("body must be stripped bytes")
	}
	if prepared.size != int64(len(stripped)) {
		t.Fatalf("size must be stripped length, got %d want %d", prepared.size, len(stripped))
	}
}

func TestPrepareAssetUploadTempPathHashesStripped(t *testing.T) {
	raw := jpegWithGPSAPP1(t)
	stripped, err := imgstrip.Strip(raw, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	sumStrip := sha256.Sum256(stripped)
	stripHex := hex.EncodeToString(sumStrip[:])

	s := &Service{log: zap.NewNop().Sugar()}
	fh := &multipart.FileHeader{Size: memoryUploadLimit + 1}
	prepared, err := s.prepareAssetUpload(&memMultipartFile{bytes.NewReader(raw)}, fh, "image/jpeg")
	if err != nil {
		t.Fatalf("prepareAssetUpload temp path: %v", err)
	}
	if prepared.cleanup != nil {
		defer prepared.cleanup()
	}
	if prepared.checksum != stripHex {
		t.Fatalf("temp-path checksum must be stripped, got %s want %s", prepared.checksum, stripHex)
	}
	got, err := io.ReadAll(prepared.reader)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("GPS")) {
		t.Fatal("temp-path body must not contain GPS")
	}
}

func TestComputeImageMetadataPhoneJPEGFinishesQuickly(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2048, 1536))
	for y := 0; y < 1536; y += 16 {
		for x := 0; x < 2048; x += 16 {
			src.Set(x, y, color.RGBA{R: 200, G: 40, B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	s := &Service{log: zap.NewNop().Sugar()}
	start := time.Now()
	hash, w, h, ok := s.computeImageMetadata("image/jpeg", buf.Bytes())
	elapsed := time.Since(start)
	if !ok || hash == "" || w != 2048 || h != 1536 {
		t.Fatalf("metadata: ok=%v hash=%q %dx%d", ok, hash, w, h)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("blurhash on a 3MP jpeg took %s; phone uploads cannot wait", elapsed)
	}
}
