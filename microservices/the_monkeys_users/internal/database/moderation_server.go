package database

import (
	"fmt"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"github.com/the-monkeys/the_monkeys/common/moderation"
	"google.golang.org/grpc"
)

// RegisterModerationServer shares the primary user SQL pool; it does not open
// another connection pool or create a separate service process.
func RegisterModerationServer(server *grpc.Server, db UserDb) error {
	concrete, ok := db.(*uDBHandler)
	if server == nil || !ok || concrete == nil || concrete.db == nil {
		return fmt.Errorf("moderation requires a gRPC server and the primary PostgreSQL user store")
	}
	pb.RegisterModerationServiceServer(server, moderation.NewServer(moderation.NewStore(concrete.db)))
	return nil
}
