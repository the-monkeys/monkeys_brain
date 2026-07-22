package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamp "google.golang.org/protobuf/types/known/timestamppb"
)

func toPbBusinessCard(c *models.BusinessCard) *pb.BusinessCard {
	card := &pb.BusinessCard{
		Id:                  c.Id,
		AccountId:           c.AccountId,
		Name:                c.Name,
		TemplateId:          c.TemplateId,
		ThemeId:             c.ThemeId,
		CardState:           c.CardState,
		IsDefault:           c.IsDefault,
		AvatarAssetChecksum: c.AvatarAssetChecksum.String,
		LogoAssetChecksum:   c.LogoAssetChecksum.String,
	}
	if c.CreatedAt.Valid {
		card.CreatedAt = timestamp.New(c.CreatedAt.Time)
	}
	if c.UpdatedAt.Valid {
		card.UpdatedAt = timestamp.New(c.UpdatedAt.Time)
	}
	return card
}

func cardNullString(s string) sql.NullString {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// validateCardState fails fast (before the DB round-trip) with a precise
// InvalidArgument, mirroring the chk_business_cards_card_state_object CHECK.
func validateCardState(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return status.Error(codes.InvalidArgument, "card_state is required")
	}
	if !json.Valid([]byte(trimmed)) {
		return status.Error(codes.InvalidArgument, "card_state must be valid JSON")
	}
	if trimmed[0] != '{' {
		return status.Error(codes.InvalidArgument, "card_state must be a JSON object")
	}
	return nil
}

func (us *UserSvc) CreateBusinessCard(ctx context.Context, req *pb.CreateBusinessCardReq) (*pb.BusinessCard, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "card name is required")
	}
	if strings.TrimSpace(req.TemplateId) == "" || strings.TrimSpace(req.ThemeId) == "" {
		return nil, status.Error(codes.InvalidArgument, "template_id and theme_id are required")
	}
	if err := validateCardState(req.CardState); err != nil {
		return nil, err
	}

	card := &models.BusinessCard{
		AccountId:           req.AccountId,
		Name:                name,
		TemplateId:          strings.TrimSpace(req.TemplateId),
		ThemeId:             strings.TrimSpace(req.ThemeId),
		CardState:           req.CardState,
		IsDefault:           req.IsDefault,
		AvatarAssetChecksum: cardNullString(req.AvatarAssetChecksum),
		LogoAssetChecksum:   cardNullString(req.LogoAssetChecksum),
	}

	created, err := us.dbConn.CreateBusinessCard(card)
	if err != nil {
		us.log.Errorf("create business card for %s failed: %v", req.AccountId, err)
		return nil, status.Error(codes.Internal, "could not create the business card")
	}
	return toPbBusinessCard(created), nil
}

func (us *UserSvc) GetBusinessCard(ctx context.Context, req *pb.GetBusinessCardReq) (*pb.BusinessCard, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "card id is required")
	}

	card, err := us.dbConn.GetBusinessCard(req.AccountId, req.Id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "business card not found")
		}
		us.log.Errorf("get business card %s failed: %v", req.Id, err)
		return nil, status.Error(codes.Internal, "could not fetch the business card")
	}
	return toPbBusinessCard(card), nil
}

func (us *UserSvc) ListBusinessCards(ctx context.Context, req *pb.ListBusinessCardsReq) (*pb.ListBusinessCardsRes, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}

	cards, err := us.dbConn.ListBusinessCards(req.AccountId, int(req.Limit), int(req.Offset))
	if err != nil {
		us.log.Errorf("list business cards for %s failed: %v", req.AccountId, err)
		return nil, status.Error(codes.Internal, "could not list the business cards")
	}

	res := &pb.ListBusinessCardsRes{Cards: make([]*pb.BusinessCard, 0, len(cards))}
	for i := range cards {
		res.Cards = append(res.Cards, toPbBusinessCard(&cards[i]))
	}
	return res, nil
}

func (us *UserSvc) UpdateBusinessCard(ctx context.Context, req *pb.UpdateBusinessCardReq) (*pb.BusinessCard, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "card id is required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "card name is required")
	}
	if strings.TrimSpace(req.TemplateId) == "" || strings.TrimSpace(req.ThemeId) == "" {
		return nil, status.Error(codes.InvalidArgument, "template_id and theme_id are required")
	}
	if err := validateCardState(req.CardState); err != nil {
		return nil, err
	}

	card := &models.BusinessCard{
		Id:                  strings.TrimSpace(req.Id),
		AccountId:           req.AccountId,
		Name:                name,
		TemplateId:          strings.TrimSpace(req.TemplateId),
		ThemeId:             strings.TrimSpace(req.ThemeId),
		CardState:           req.CardState,
		IsDefault:           req.IsDefault,
		AvatarAssetChecksum: cardNullString(req.AvatarAssetChecksum),
		LogoAssetChecksum:   cardNullString(req.LogoAssetChecksum),
	}

	updated, err := us.dbConn.UpdateBusinessCard(card)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "business card not found")
		}
		us.log.Errorf("update business card %s failed: %v", req.Id, err)
		return nil, status.Error(codes.Internal, "could not update the business card")
	}
	return toPbBusinessCard(updated), nil
}

func (us *UserSvc) DeleteBusinessCard(ctx context.Context, req *pb.DeleteBusinessCardReq) (*pb.DeleteBusinessCardRes, error) {
	if strings.TrimSpace(req.AccountId) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account identity")
	}
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "card id is required")
	}

	if err := us.dbConn.DeleteBusinessCard(req.AccountId, req.Id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "business card not found")
		}
		us.log.Errorf("delete business card %s failed: %v", req.Id, err)
		return nil, status.Error(codes.Internal, "could not delete the business card")
	}
	return &pb.DeleteBusinessCardRes{Status: "success", Message: "business card deleted"}, nil
}
