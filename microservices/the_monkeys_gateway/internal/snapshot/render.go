package snapshot

import (
	"encoding/base64"

	"fmt"

	"io"

	"net/http"

	"os"

	"os/exec"

	"path/filepath"

	"strings"

	"github.com/gin-gonic/gin"

	"github.com/google/uuid"

	"github.com/the-monkeys/the_monkeys/config"

	"go.uber.org/zap"
)

const segmentSeconds = 15.0

type RenderRequest struct {
	TweetID string `json:"tweetId"`

	VideoURL string `json:"videoUrl"`

	Overlay string `json:"overlay"`

	BackgroundColor string `json:"backgroundColor"`

	FrameX float64 `json:"frameX"`

	FrameY float64 `json:"frameY"`

	FrameW float64 `json:"frameW"`

	FrameH float64 `json:"frameH"`

	CanvasW float64 `json:"canvasW"`

	CanvasH float64 `json:"canvasH"`
}

type SnapshotService struct {
	logger *zap.SugaredLogger

	config *config.Config
}

func NewSnapshotService(cfg *config.Config, log *zap.SugaredLogger) *SnapshotService {

	return &SnapshotService{logger: log, config: cfg}

}

func RegisterRoutes(router *gin.Engine, cfg *config.Config, log *zap.SugaredLogger) {

	svc := NewSnapshotService(cfg, log)

	router.POST("/api/snapshot/tweet/video/render", svc.HandleVideoRender)

}

func (s *SnapshotService) HandleVideoRender(c *gin.Context) {

	var req RenderRequest

	if err := c.ShouldBindJSON(&req); err != nil {

		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})

		return

	}

	if req.VideoURL == "" || req.Overlay == "" || req.CanvasW <= 0 || req.CanvasH <= 0 {

		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing render fields"})

		return

	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {

		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "FFmpeg not available"})

		return

	}

	tempDir := filepath.Join(os.TempDir(), "monkeys-render-"+uuid.NewString())

	if err := os.MkdirAll(tempDir, 0o755); err != nil {

		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create workspace"})

		return

	}

	defer os.RemoveAll(tempDir)

	videoPath := filepath.Join(tempDir, "source.mp4")

	if err := downloadFile(req.VideoURL, videoPath); err != nil {

		s.logger.Errorf("download video: %v", err)

		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to fetch source video"})

		return

	}

	overlayData := req.Overlay

	if idx := strings.Index(overlayData, ","); idx != -1 {

		overlayData = overlayData[idx+1:]

	}

	pngBytes, err := base64.StdEncoding.DecodeString(overlayData)

	if err != nil {

		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid overlay data"})

		return

	}

	overlayPath := filepath.Join(tempDir, "overlay.png")

	if err := os.WriteFile(overlayPath, pngBytes, 0o644); err != nil {

		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write overlay"})

		return

	}

	outputPath := filepath.Join(tempDir, "output.mp4")

	if err := s.encodeBranded(videoPath, overlayPath, outputPath, req); err != nil {

		s.logger.Errorf("encode branded video: %v", err)

		c.JSON(http.StatusInternalServerError, gin.H{"error": "Video rendering failed"})

		return

	}

	c.Header("Content-Type", "video/mp4")

	c.Header("Content-Disposition", fmt.Sprintf(

		`attachment; filename="x-post-%s-branded.mp4"`, req.TweetID,
	))

	c.File(outputPath)

}

func (s *SnapshotService) encodeBranded(videoPath, overlayPath, outputPath string, req RenderRequest) error {

	dur, err := probeDuration(videoPath)

	if err != nil {

		return s.encodeSegmented(videoPath, overlayPath, outputPath, req, 0)

	}

	if dur <= segmentSeconds {

		return runEncode(videoPath, overlayPath, outputPath, req, 0, 0)

	}

	return s.encodeSegmented(videoPath, overlayPath, outputPath, req, dur)

}

func (s *SnapshotService) encodeSegmented(videoPath, overlayPath, outputPath string, req RenderRequest, dur float64) error {

	dir := filepath.Dir(outputPath)

	var parts []string

	for start := 0.0; ; start += segmentSeconds {

		if dur > 0 && start >= dur {

			break

		}

		chunk := segmentSeconds

		if dur > 0 && start+chunk > dur {

			chunk = dur - start

		}

		part := filepath.Join(dir, fmt.Sprintf("part_%03d.mp4", len(parts)))

		if err := runEncode(videoPath, overlayPath, part, req, start, chunk); err != nil {

			if len(parts) == 0 {

				return err

			}

			break

		}

		if info, statErr := os.Stat(part); statErr != nil || info.Size() < 1024 {

			_ = os.Remove(part)

			break

		}

		parts = append(parts, part)

		if dur > 0 && start+segmentSeconds >= dur {

			break

		}

	}

	if len(parts) == 0 {

		return fmt.Errorf("no segments encoded")

	}

	if len(parts) == 1 {

		return os.Rename(parts[0], outputPath)

	}

	return concatParts(parts, outputPath)

}

// Overlay PNG is the full branded frame (with alpha hole); video sits beneath it.

func buildFilter(req RenderRequest) string {
	cw, ch := int(req.CanvasW), int(req.CanvasH)
	fx, fy := int(req.FrameX), int(req.FrameY)
	fw, fh := int(req.FrameW), int(req.FrameH)
	bg := "0x000000"
	if strings.HasPrefix(req.BackgroundColor, "#") && len(req.BackgroundColor) == 7 {
		bg = "0x" + req.BackgroundColor[1:]
	}

	return fmt.Sprintf(
		"color=c=%s:s=%dx%d[bg];"+
			"[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d[v];"+
			"[bg][v]overlay=%d:%d[under];"+
			"[1:v]scale=%d:%d,format=rgba[ov];"+
			"[under][ov]overlay=0:0:format=auto:shortest=1[out]",
		bg, cw, ch, fw, fh, fw, fh, fx, fy, cw, ch,
	)
}

func runEncode(videoPath, overlayPath, outputPath string, req RenderRequest, ss, duration float64) error {

	args := []string{

		"-nostdin", "-hide_banner", "-loglevel", "warning",

		"-threads", "1", "-filter_threads", "1",
	}

	if ss > 0 {

		args = append(args, "-ss", fmt.Sprintf("%.2f", ss))

	}

	args = append(args, "-i", videoPath, "-i", overlayPath)

	if duration > 0 {

		args = append(args, "-t", fmt.Sprintf("%.2f", duration))

	}

	args = append(args,

		"-filter_complex", buildFilter(req),

		"-map", "[out]",

		"-map", "0:a?",

		"-c:v", "libx264",

		"-preset", "ultrafast",

		"-crf", "24",

		"-pix_fmt", "yuv420p",

		"-x264-params", "threads=1:ref=1:rc-lookahead=0:bframes=0:me=dia:subme=0",

		"-c:a", "aac",

		"-b:a", "96k",

		"-movflags", "+faststart",

		"-y", outputPath,
	)

	cmd := exec.Command("ffmpeg", args...)

	cmd.Env = append(os.Environ(), "OMP_NUM_THREADS=1")

	out, err := cmd.CombinedOutput()

	if err != nil {

		tail := string(out)

		if len(tail) > 2000 {

			tail = tail[len(tail)-2000:]

		}

		return fmt.Errorf("ffmpeg: %w\n%s", err, tail)

	}

	return nil

}

func probeDuration(path string) (float64, error) {

	out, err := exec.Command("ffprobe",

		"-v", "error",

		"-show_entries", "format=duration",

		"-of", "default=noprint_wrappers=1:nokey=1",

		path,
	).Output()

	if err != nil {
		return 0, err
	}
	var d float64
	_, err = fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d)
	return d, err

}

func concatParts(parts []string, outputPath string) error {

	listPath := filepath.Join(filepath.Dir(outputPath), "concat.txt")

	lines := make([]string, len(parts))

	for i, p := range parts {

		lines[i] = "file '" + strings.ReplaceAll(p, "'", `'\''`) + "'"

	}

	if err := os.WriteFile(listPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {

		return err

	}

	cmd := exec.Command("ffmpeg",

		"-nostdin", "-hide_banner", "-loglevel", "error",

		"-f", "concat", "-safe", "0", "-i", listPath,

		"-c", "copy", "-movflags", "+faststart",

		"-y", outputPath,
	)

	out, err := cmd.CombinedOutput()

	if err != nil {

		return fmt.Errorf("concat: %w\n%s", err, string(out))

	}

	return nil

}

func downloadFile(url string, dest string) error {

	resp, err := http.Get(url)

	if err != nil {

		return err

	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {

		return fmt.Errorf("bad status: %s", resp.Status)

	}

	out, err := os.Create(dest)

	if err != nil {

		return err

	}

	defer out.Close()

	_, err = io.Copy(out, resp.Body)

	return err

}
