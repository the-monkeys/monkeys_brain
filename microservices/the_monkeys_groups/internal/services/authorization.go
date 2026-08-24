package services

import (
	"context"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

// Authorize resolves the caller's standing on a group in a single round trip.
//
// It backs the gateway's fast-reject guard layer: the gateway caches the result
// briefly to reject unauthorized or invisible requests before they reach the
// mutation RPCs. It is advisory only — every write path re-checks the caller's
// permission inside its own transaction, so a request that slips past a stale
// cache entry is still refused at the row.
//
// Authorization is keyed on account_id, the immutable per-user identifier that
// is stable across every service. Usernames are display handles and must never
// be used to decide access.
func (s *GroupService) Authorize(ctx context.Context, req *pb.AuthorizeGroupReq) (*pb.AuthorizeGroupResp, error) {
	return s.db.AuthorizeGroup(ctx, req)
}
