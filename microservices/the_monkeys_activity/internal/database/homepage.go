package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/the-monkeys/the_monkeys/common/homepage"
	"google.golang.org/grpc/metadata"
)

func homepageTrendingRequested(ctx context.Context) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get(homepage.MetadataKey)
	return len(values) == 1 && values[0] == homepage.Version
}

// Inspect status only for the homepage contract. Ordinary activity callers keep
// accepting the same legacy response shapes and cache behavior as before.
func validateHomepageTrendingSearch(timedOut, shards json.RawMessage) error {
	var timeout *bool
	if err := json.Unmarshal(timedOut, &timeout); err != nil || timeout == nil || *timeout {
		return fmt.Errorf("homepage activity search is incomplete")
	}
	var status struct {
		Failed *int `json:"failed"`
	}
	if err := json.Unmarshal(shards, &status); err != nil || status.Failed == nil || *status.Failed != 0 {
		return fmt.Errorf("homepage activity search is incomplete")
	}
	return nil
}
