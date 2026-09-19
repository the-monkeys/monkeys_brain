package snapshot

import (
	"strings"
	"testing"
)

func TestBuildFilterCompositesOverlayOnTopOfVideo(t *testing.T) {
	req := RenderRequest{
		TweetID:         "12345",
		VideoURL:        "https://video.twimg.com/test.mp4",
		BackgroundColor: "#4b5e68",
		FrameX:          64,
		FrameY:          200,
		FrameW:          952,
		FrameH:          535,
		CanvasW:         1080,
		CanvasH:         1350,
	}

	filter := buildFilter(req)

	// The video ([v]) must be placed beneath the overlay ([ov]), so the overlay's
	// punched alpha hole and rounded borders sit ON TOP of the video.
	// Video should overlay onto background first:
	if !strings.Contains(filter, "[bg][v]overlay=64:200") {
		t.Fatalf("expected video [v] to be composited over [bg] at frame coordinates, got: %s", filter)
	}

	// Overlay [ov] must be composited on top of the under-layer at 0:0:
	if !strings.Contains(filter, "overlay=0:0:format=auto:shortest=1[out]") {
		t.Fatalf("expected overlay [ov] to be composited on top with shortest=1, got: %s", filter)
	}
}
