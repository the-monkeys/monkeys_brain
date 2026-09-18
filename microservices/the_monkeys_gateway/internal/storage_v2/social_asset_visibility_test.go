package storage_v2

import (
	"testing"

	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
)

func TestSocialAssetVisibleRejectsUnknownAsset(t *testing.T) {
	if socialAssetVisible(nil, true, func(string) bool { return true }) {
		t.Fatal("unknown asset was accepted")
	}
}

func TestSocialAssetVisibleRejectsCrossUserAsset(t *testing.T) {
	res := &pb.ResolveAssetReadResp{UnpublishedBlogIds: []string{"owner-blog"}}
	if socialAssetVisible(res, true, func(blogID string) bool {
		return blogID == "requester-blog"
	}) {
		t.Fatal("cross-user asset was accepted")
	}
}

func TestSocialAssetVisibleAcceptsOwnVisibleAsset(t *testing.T) {
	res := &pb.ResolveAssetReadResp{UnpublishedBlogIds: []string{"owner-blog"}}
	if !socialAssetVisible(res, true, func(blogID string) bool {
		return blogID == "owner-blog"
	}) {
		t.Fatal("own visible asset was rejected")
	}
}
