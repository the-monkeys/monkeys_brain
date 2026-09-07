package services

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

func TestGroupVisibleToViewer(t *testing.T) {
	cases := []struct {
		name string
		g    *pb.Group
		want bool
	}{
		{
			name: "published public stranger",
			g:    &pb.Group{Status: "published", Visibility: "public"},
			want: true,
		},
		{
			name: "draft stranger",
			g:    &pb.Group{Status: "draft", Visibility: "public"},
			want: false,
		},
		{
			name: "draft organizer",
			g:    &pb.Group{Status: "draft", Visibility: "public", ViewerRole: "organizer"},
			want: true,
		},
		{
			name: "private published non-member",
			g:    &pb.Group{Status: "published", Visibility: "private"},
			want: false,
		},
		{
			name: "private published member",
			g:    &pb.Group{Status: "published", Visibility: "private", ViewerMemberStatus: "active", ViewerRole: "member"},
			want: true,
		},
		{
			name: "unlisted published by link",
			g:    &pb.Group{Status: "published", Visibility: "unlisted"},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := groupVisibleToViewer(tc.g); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
