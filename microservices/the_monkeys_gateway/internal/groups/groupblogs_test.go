package groups

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

func TestIsActiveMember(t *testing.T) {
	if isActiveMember(nil) {
		t.Fatal("nil")
	}
	if isActiveMember(&pb.AuthorizeGroupResp{IsMember: true, MemberStatus: "left"}) {
		t.Fatal("left")
	}
	if !isActiveMember(&pb.AuthorizeGroupResp{IsMember: true, MemberStatus: "active"}) {
		t.Fatal("active")
	}
}
