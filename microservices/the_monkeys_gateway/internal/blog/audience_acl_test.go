package blog

import (
	"testing"

	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

func TestIsActiveGroupMember(t *testing.T) {
	if isActiveGroupMember(nil) {
		t.Fatal("nil is not a member")
	}
	if isActiveGroupMember(&grouppb.AuthorizeGroupResp{IsMember: true, MemberStatus: "pending"}) {
		t.Fatal("pending is not active")
	}
	if !isActiveGroupMember(&grouppb.AuthorizeGroupResp{IsMember: true, MemberStatus: "active"}) {
		t.Fatal("active member")
	}
}
