package services

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/constants"
)

func TestGroupJoinAction(t *testing.T) {
	if got := groupJoinAction("pending"); got != constants.GROUP_JOIN_REQUESTED {
		t.Fatalf("pending=%q", got)
	}
	if got := groupJoinAction("active"); got != constants.GROUP_MEMBER_JOINED {
		t.Fatalf("active=%q", got)
	}
	if got := groupJoinAction("left"); got != "" {
		t.Fatalf("left=%q", got)
	}
}
