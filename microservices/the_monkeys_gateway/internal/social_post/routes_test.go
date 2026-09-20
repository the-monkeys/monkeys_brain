package social_post

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_social_post/pb"
)

type importVerifierStub struct {
	visible bool
}

func (v importVerifierStub) VerifySocialAssetReference(*gin.Context, string) bool {
	return v.visible
}

func TestVerifyImportSourceRejectsUnverifiableReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	if verifyImportSource(ctx, importVerifierStub{visible: false}, "cross-user-ref") {
		t.Fatal("unverifiable source asset was accepted")
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unverifiable source asset returned %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestVerifyImportSourceAcceptsVisibleReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	if !verifyImportSource(ctx, importVerifierStub{visible: true}, "owned-ref") {
		t.Fatal("visible source asset was rejected")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("visible source asset wrote status %d, want untouched recorder status %d", recorder.Code, http.StatusOK)
	}
}

func TestPlatformToStringMapping(t *testing.T) {
	if platformToString[pb.Platform_PLATFORM_X] != "x" {
		t.Fatalf("expected 'x', got %q", platformToString[pb.Platform_PLATFORM_X])
	}
	if platformToString[pb.Platform_PLATFORM_LINKEDIN] != "linkedin" {
		t.Fatalf("expected 'linkedin', got %q", platformToString[pb.Platform_PLATFORM_LINKEDIN])
	}
	if platformToString[pb.Platform_PLATFORM_INSTAGRAM] != "instagram" {
		t.Fatalf("expected 'instagram', got %q", platformToString[pb.Platform_PLATFORM_INSTAGRAM])
	}
	if platformToString[pb.Platform_PLATFORM_FACEBOOK] != "facebook" {
		t.Fatalf("expected 'facebook', got %q", platformToString[pb.Platform_PLATFORM_FACEBOOK])
	}
	if platformToString[pb.Platform_PLATFORM_YOUTUBE] != "youtube" {
		t.Fatalf("expected 'youtube', got %q", platformToString[pb.Platform_PLATFORM_YOUTUBE])
	}
	if platformToString[pb.Platform_PLATFORM_TIKTOK] != "tiktok" {
		t.Fatalf("expected 'tiktok', got %q", platformToString[pb.Platform_PLATFORM_TIKTOK])
	}
}

func TestPostDTOIncludesStatusAndState(t *testing.T) {
	p := &pb.SocialPost{
		Id:       "post-123",
		BaseText: "hello monkeys",
		State:    "draft",
		Renditions: []*pb.Rendition{
			{
				Id:              "rend-1",
				SocialAccountId: "acc-1",
				Platform:        pb.Platform_PLATFORM_X,
				TextOverride:    "custom x text",
			},
		},
	}
	dto := toPostDTO(p)
	if dto == nil {
		t.Fatal("expected non-nil DTO")
	}
	if dto.State != "draft" || dto.Status != "draft" {
		t.Fatalf("expected state and status to be 'draft', got state=%q, status=%q", dto.State, dto.Status)
	}
	if len(dto.Renditions) != 1 || dto.Renditions[0].Platform != "x" {
		t.Fatalf("expected rendition platform to be 'x', got %v", dto.Renditions)
	}
}

func TestToPostResponseDTO(t *testing.T) {
	resp := &pb.PostResponse{
		Post: &pb.SocialPost{
			Id:       "post-456",
			BaseText: "scheduled monkeys",
			State:    "scheduled",
			Renditions: []*pb.Rendition{
				{
					Id:              "rend-2",
					SocialAccountId: "acc-2",
					Platform:        pb.Platform_PLATFORM_LINKEDIN,
				},
			},
		},
		Violations: []*pb.FieldViolation{
			{
				Field:   "text",
				RuleId:  "max_len",
				Message: "too long",
			},
		},
	}
	dto := toPostResponseDTO(resp)
	post, ok := dto["post"].(*SocialPostDTO)
	if !ok || post == nil {
		t.Fatalf("expected *SocialPostDTO in 'post', got %v", dto["post"])
	}
	if post.State != "scheduled" || post.Status != "scheduled" {
		t.Fatalf("expected post state and status 'scheduled', got state=%q, status=%q", post.State, post.Status)
	}
	if len(post.Renditions) != 1 || post.Renditions[0].Platform != "linkedin" {
		t.Fatalf("expected platform 'linkedin', got %v", post.Renditions)
	}
	violations, ok := dto["violations"].([]FieldViolationDTO)
	if !ok || len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %v", dto["violations"])
	}
	if violations[0].RuleID != "max_len" {
		t.Fatalf("expected violation rule 'max_len', got %q", violations[0].RuleID)
	}

	data, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	var unmarshaled map[string]interface{}
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	postMap := unmarshaled["post"].(map[string]interface{})
	if postMap["state"] != "scheduled" || postMap["status"] != "scheduled" {
		t.Fatalf("expected state and status in json: %v", postMap)
	}
}

func TestToListPostsResponseDTO(t *testing.T) {
	resp := &pb.ListPostsResponse{
		Posts: []*pb.SocialPost{
			{
				Id:       "p1",
				BaseText: "post 1",
				State:    "draft",
			},
		},
		NextPageToken: "next-token-123",
	}
	dto := toListPostsResponseDTO(resp)
	items, okItems := dto["items"].([]SocialPostDTO)
	posts, okPosts := dto["posts"].([]SocialPostDTO)
	if !okItems || len(items) != 1 {
		t.Fatalf("expected 1 item, got %v", dto["items"])
	}
	if !okPosts || len(posts) != 1 {
		t.Fatalf("expected 1 post, got %v", dto["posts"])
	}
	if dto["next_page_token"] != "next-token-123" {
		t.Fatalf("expected next_page_token 'next-token-123', got %v", dto["next_page_token"])
	}
}

func TestToListAccountsResponseDTO(t *testing.T) {
	resp := &pb.ListAccountsResponse{
		Accounts: []*pb.SocialAccount{
			{
				Id:          "acc-x",
				Platform:    pb.Platform_PLATFORM_X,
				DisplayName: "X User",
				Handle:      "@user",
				Status:      "active",
				Validation: &pb.ValidationMetadata{
					Platform:          pb.Platform_PLATFORM_X,
					MaxTextCharacters: 280,
				},
			},
		},
	}
	dto := toListAccountsResponseDTO(resp)
	accounts, ok := dto["accounts"].([]SocialAccountDTO)
	if !ok || len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %v", dto["accounts"])
	}
	if accounts[0].Platform != "x" {
		t.Fatalf("expected platform 'x', got %q", accounts[0].Platform)
	}
	if accounts[0].Validation == nil || accounts[0].Validation.Platform != "x" || accounts[0].Validation.MaxTextCharacters != 280 {
		t.Fatalf("expected validation metadata with platform 'x' and max 280, got %v", accounts[0].Validation)
	}
}

func TestToListMediaResponseDTO(t *testing.T) {
	resp := &pb.ListMediaAssetsResponse{
		Assets: []*pb.MediaAsset{
			{
				Id:          "asset-1",
				ObjectKey:   "key-1",
				ContentType: "image/png",
				ByteSize:    1024,
				MediaKind:   "image",
			},
		},
		NextPageToken: "media-next",
	}
	dto := toListMediaResponseDTO(resp)
	assets, okAssets := dto["assets"].([]MediaAssetDTO)
	items, okItems := dto["items"].([]MediaAssetDTO)
	if !okAssets || len(assets) != 1 {
		t.Fatalf("expected 1 asset, got %v", dto["assets"])
	}
	if !okItems || len(items) != 1 {
		t.Fatalf("expected 1 item, got %v", dto["items"])
	}
	if dto["next_page_token"] != "media-next" {
		t.Fatalf("expected next_page_token 'media-next', got %v", dto["next_page_token"])
	}
}

func TestToValidationMetadataResponseDTO(t *testing.T) {
	resp := &pb.ListValidationMetadataResponse{
		Platforms: []*pb.ValidationMetadata{
			{
				Platform:          pb.Platform_PLATFORM_INSTAGRAM,
				MaxTextCharacters: 2200,
				AllowedMediaKinds: []string{"image", "video"},
			},
		},
	}
	dto := toValidationMetadataResponseDTO(resp)
	platforms, ok := dto["platforms"].([]ValidationMetadataDTO)
	if !ok || len(platforms) != 1 {
		t.Fatalf("expected 1 platform, got %v", dto["platforms"])
	}
	if platforms[0].Platform != "instagram" {
		t.Fatalf("expected platform 'instagram', got %q", platforms[0].Platform)
	}
	if platforms[0].MaxTextCharacters != 2200 {
		t.Fatalf("expected max text chars 2200, got %d", platforms[0].MaxTextCharacters)
	}
}

func TestToRenditionDTOIncludesScheduledOverrides(t *testing.T) {
	rend := &pb.Rendition{
		Id:               "rend-override-1",
		SocialAccountId:  "acc-123",
		Platform:         pb.Platform_PLATFORM_X,
		TextOverride:     "override text",
		ScheduledAt:      "2026-10-01T12:00:00Z",
		ScheduleTimezone: "America/New_York",
		State:            "draft",
		Version:          1,
	}
	dto := toRenditionDTO(rend)
	if dto == nil {
		t.Fatal("expected non-nil RenditionDTO")
	}
	if dto.ScheduledAtOverride != "2026-10-01T12:00:00Z" {
		t.Fatalf("expected ScheduledAtOverride %q, got %q", "2026-10-01T12:00:00Z", dto.ScheduledAtOverride)
	}
	if dto.ScheduleTimezoneOverride != "America/New_York" {
		t.Fatalf("expected ScheduleTimezoneOverride %q, got %q", "America/New_York", dto.ScheduleTimezoneOverride)
	}
	if dto.ScheduledAt != "2026-10-01T12:00:00Z" {
		t.Fatalf("expected ScheduledAt %q, got %q", "2026-10-01T12:00:00Z", dto.ScheduledAt)
	}
	if dto.ScheduleTimezone != "America/New_York" {
		t.Fatalf("expected ScheduleTimezone %q, got %q", "America/New_York", dto.ScheduleTimezone)
	}
}

func TestToSocialAccountDTOIncludesMockAndAvatar(t *testing.T) {
	acc := &pb.SocialAccount{
		Id:          "acc-test-1",
		Platform:    pb.Platform_PLATFORM_X,
		DisplayName: "Test X",
		Handle:      "test_x",
		Status:      "active",
		IsMock:      true,
		AvatarUrl:   "https://example.com/avatar.png",
	}
	dto := toSocialAccountDTO(acc)
	if dto == nil {
		t.Fatal("expected non-nil SocialAccountDTO")
	}
	if !dto.IsMock {
		t.Fatal("expected IsMock to be true")
	}
	if dto.AvatarURL != "https://example.com/avatar.png" {
		t.Fatalf("expected AvatarURL %q, got %q", "https://example.com/avatar.png", dto.AvatarURL)
	}
	if dto.Platform != "x" {
		t.Fatalf("expected Platform 'x', got %q", dto.Platform)
	}
}

func TestToListAccountsResponseDTOIncludesMockAccounts(t *testing.T) {
	resp := &pb.ListAccountsResponse{
		Accounts: []*pb.SocialAccount{
			{
				Id:          "acc-1",
				Platform:    pb.Platform_PLATFORM_X,
				DisplayName: "X Mock",
				Handle:      "x_mock",
				Status:      "active",
				IsMock:      true,
			},
			{
				Id:          "acc-2",
				Platform:    pb.Platform_PLATFORM_INSTAGRAM,
				DisplayName: "Insta Real",
				Handle:      "insta_real",
				Status:      "active",
				IsMock:      false,
			},
		},
	}
	dto := toListAccountsResponseDTO(resp)
	accounts, ok := dto["accounts"].([]SocialAccountDTO)
	if !ok || len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %v", dto["accounts"])
	}
	if !accounts[0].IsMock {
		t.Fatal("expected account 0 to be mock")
	}
	if accounts[1].IsMock {
		t.Fatal("expected account 1 not to be mock")
	}
}
