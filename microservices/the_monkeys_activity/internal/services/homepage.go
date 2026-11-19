package services

import (
	"context"

	"github.com/the-monkeys/the_monkeys/common/homepage"
	"google.golang.org/grpc/metadata"
)

func homepageTrendingRequested(ctx context.Context) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get(homepage.MetadataKey)
	return len(values) == 1 && values[0] == homepage.Version
}
