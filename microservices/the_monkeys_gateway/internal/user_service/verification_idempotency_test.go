package user_service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"google.golang.org/grpc"
)

type replayVerificationClient struct {
	pb.UserServiceClient
	result  *pb.VerificationRequest
	err     error
	request *pb.GetVerificationReq
}

func (f *replayVerificationClient) GetMyVerification(_ context.Context, req *pb.GetVerificationReq, _ ...grpc.CallOption) (*pb.VerificationRequest, error) {
	f.request = req
	return f.result, f.err
}
func TestVerificationReplayStoresOnlyResourceReference(t *testing.T) {
	input := []byte(`{"id":"request-1","username":"alice","selfie_checksum":"proof","selfie_object_key":"private-path","additional_info":"sensitive"}`)
	reference, err := verificationReference(input)
	if err != nil || string(reference) != `{"id":"request-1"}` {
		t.Fatalf("private proof entered replay reference: %s %v", reference, err)
	}
	for _, invalid := range []string{`{}`, `null`, `{"id":""}`, `not-json`, `{"id":"` + strings.Repeat("x", 129) + `"}`} {
		if _, err = verificationReference([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid reference %s", invalid)
		}
	}
}
func TestVerificationReplayChecksOwnerAndExactResource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		result    *pb.VerificationRequest
		err       error
		wantError bool
	}{
		{"owned", &pb.VerificationRequest{Id: "request-1", Username: "alice", Status: "pending", SelfieChecksum: "proof"}, nil, false},
		{"other owner", &pb.VerificationRequest{Id: "request-1", Username: "bob", SelfieChecksum: "secret"}, nil, true},
		{"different resource", &pb.VerificationRequest{Id: "request-2", Username: "alice"}, nil, true},
		{"missing", nil, nil, true},
		{"service failure", nil, errors.New("unavailable"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &replayVerificationClient{result: tc.result, err: tc.err}
			usc := &UserServiceClient{Client: client}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/api/v1/user/verification", nil)
			c.Set("userName", "alice")
			data, err := usc.resolveVerificationReplay(c, []byte(`{"id":"request-1"}`))
			if (err != nil) != tc.wantError {
				t.Fatalf("ownership result data=%s err=%v", data, err)
			}
			if tc.wantError && len(data) > 0 {
				t.Fatal("private data returned after ownership or service error")
			}
			if !tc.wantError {
				var got pb.VerificationRequest
				if json.Unmarshal(data, &got) != nil || got.Id != "request-1" || got.SelfieChecksum != "proof" {
					t.Fatal("response fields lost")
				}
			}
			if client.request.RequestId != "request-1" || client.request.Username != "alice" || client.request.Latest {
				t.Fatal("lookup could replay a different or unscoped request")
			}
		})
	}
}
