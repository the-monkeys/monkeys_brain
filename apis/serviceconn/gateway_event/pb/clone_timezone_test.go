package pb_test

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCloneTimezoneWireRoundTrip(t *testing.T) {
	req := &pb.CloneEventReq{Slug: "source"}
	field := req.ProtoReflect().Descriptor().Fields().ByNumber(6)
	if field == nil || field.Name() != "timezone" || field.Kind() != protoreflect.StringKind {
		t.Fatal("CloneEventReq needs string timezone at field 6; regenerate after the proto change")
	}
	req.ProtoReflect().Set(field, protoreflect.ValueOfString("Asia/Kolkata"))
	wire, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.CloneEventReq
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.ProtoReflect().Get(field).String(); got != "Asia/Kolkata" {
		t.Fatalf("clone timezone lost over protobuf: %q", got)
	}
}
