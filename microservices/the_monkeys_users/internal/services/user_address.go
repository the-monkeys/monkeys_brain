package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamp "google.golang.org/protobuf/types/known/timestamppb"
)

func toPbUserAddress(a *models.UserAddress) *pb.UserAddress {
	addr := &pb.UserAddress{
		Id:         a.Id,
		AccountId:  a.AccountId,
		Label:      a.Label,
		Line1:      a.Line1,
		Line2:      a.Line2.String,
		City:       a.City.String,
		State:      a.State.String,
		PostalCode: a.PostalCode.String,
		Country:    a.Country.String,
		IsDefault:  a.IsDefault,
	}
	if a.CreatedAt.Valid {
		addr.CreatedAt = timestamp.New(a.CreatedAt.Time)
	}
	if a.UpdatedAt.Valid {
		addr.UpdatedAt = timestamp.New(a.UpdatedAt.Time)
	}
	return addr
}

func (us *UserSvc) CreateUserAddress(ctx context.Context, req *pb.CreateUserAddressReq) (*pb.UserAddress, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		return nil, status.Error(codes.InvalidArgument, "address label is required")
	}
	line1 := strings.TrimSpace(req.Line1)
	if line1 == "" {
		return nil, status.Error(codes.InvalidArgument, "address line1 is required")
	}

	addr := &models.UserAddress{
		AccountId:  req.AccountId,
		Label:      label,
		Line1:      line1,
		Line2:      cardNullString(req.Line2),
		City:       cardNullString(req.City),
		State:      cardNullString(req.State),
		PostalCode: cardNullString(req.PostalCode),
		Country:    cardNullString(req.Country),
		IsDefault:  req.IsDefault,
	}

	created, err := us.dbConn.CreateUserAddress(addr)
	if err != nil {
		us.log.Errorf("create user address for %s failed: %v", req.AccountId, err)
		return nil, status.Error(codes.Internal, "could not create the address")
	}
	return toPbUserAddress(created), nil
}

func (us *UserSvc) GetUserAddress(ctx context.Context, req *pb.GetUserAddressReq) (*pb.UserAddress, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "address id is required")
	}

	addr, err := us.dbConn.GetUserAddress(req.AccountId, req.Id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "address not found")
		}
		us.log.Errorf("get user address %s failed: %v", req.Id, err)
		return nil, status.Error(codes.Internal, "could not fetch the address")
	}
	return toPbUserAddress(addr), nil
}

func (us *UserSvc) ListUserAddresses(ctx context.Context, req *pb.ListUserAddressesReq) (*pb.ListUserAddressesRes, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}

	addresses, err := us.dbConn.ListUserAddresses(req.AccountId)
	if err != nil {
		us.log.Errorf("list user addresses for %s failed: %v", req.AccountId, err)
		return nil, status.Error(codes.Internal, "could not fetch the addresses")
	}

	res := &pb.ListUserAddressesRes{Addresses: make([]*pb.UserAddress, 0, len(addresses))}
	for i := range addresses {
		res.Addresses = append(res.Addresses, toPbUserAddress(&addresses[i]))
	}
	return res, nil
}

func (us *UserSvc) UpdateUserAddress(ctx context.Context, req *pb.UpdateUserAddressReq) (*pb.UserAddress, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "address id is required")
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		return nil, status.Error(codes.InvalidArgument, "address label is required")
	}
	line1 := strings.TrimSpace(req.Line1)
	if line1 == "" {
		return nil, status.Error(codes.InvalidArgument, "address line1 is required")
	}

	addr := &models.UserAddress{
		Id:         strings.TrimSpace(req.Id),
		AccountId:  req.AccountId,
		Label:      label,
		Line1:      line1,
		Line2:      cardNullString(req.Line2),
		City:       cardNullString(req.City),
		State:      cardNullString(req.State),
		PostalCode: cardNullString(req.PostalCode),
		Country:    cardNullString(req.Country),
		IsDefault:  req.IsDefault,
	}

	updated, err := us.dbConn.UpdateUserAddress(addr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "address not found")
		}
		us.log.Errorf("update user address %s failed: %v", req.Id, err)
		return nil, status.Error(codes.Internal, "could not update the address")
	}
	return toPbUserAddress(updated), nil
}

func (us *UserSvc) DeleteUserAddress(ctx context.Context, req *pb.DeleteUserAddressReq) (*pb.DeleteUserAddressRes, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "address id is required")
	}

	if err := us.dbConn.DeleteUserAddress(req.AccountId, req.Id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "address not found")
		}
		us.log.Errorf("delete user address %s failed: %v", req.Id, err)
		return nil, status.Error(codes.Internal, "could not delete the address")
	}
	return &pb.DeleteUserAddressRes{Status: "success", Message: "address deleted"}, nil
}
