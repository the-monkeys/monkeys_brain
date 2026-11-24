package database

import (
	"fmt"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"github.com/the-monkeys/the_monkeys/common/moderation"
	"google.golang.org/grpc"
)

func RegisterModerationServer(server *grpc.Server, db EventDB) error {
	concrete, ok := db.(*eventDB)
	if !ok {
		return fmt.Errorf("moderation requires the PostgreSQL event store")
	}
	pb.RegisterModerationServiceServer(server, moderation.NewServer(moderation.NewStore(concrete.db)))
	return nil
}
