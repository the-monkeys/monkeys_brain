package services

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestVerificationPolicyRequiresGovernmentID(t *testing.T) {
	svc := &UserSvc{}
	for _, typ := range []string{"social_proof", "professional", ""} {
		_, err := svc.SubmitVerificationRequest(context.Background(), &pb.SubmitVerificationReq{Username: "local-test", VerificationType: typ, AdditionalInfo: "social account"})
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "government-issued ID") {
			t.Fatalf("type %q: %v", typ, err)
		}
	}
}

func TestVerificationPolicyKeepsRequiredUploadsForAllGovernmentDocuments(t *testing.T) {
	svc := &UserSvc{}
	for _, country := range []string{"NP", "US", "PS", "FJ"} {
		for _, doc := range []string{"passport", "national_id", "drivers_license", "residence_permit", "government_id"} {
			_, err := svc.SubmitVerificationRequest(context.Background(), &pb.SubmitVerificationReq{Username: "local-test", VerificationType: "id_document", Country: country, IdDocumentType: doc})
			if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "selfie and id_front uploads are required") {
				t.Fatalf("%s/%s must reach upload validation: %v", country, doc, err)
			}
		}
	}
}

func TestVerificationPolicyRetainsHistoricalRecords(t *testing.T) {
	for _, typ := range []string{"social_proof", "professional", "id_document"} {
		got := toPbVerificationRequest(&models.VerificationRequest{Id: "historical", Username: "owner", VerificationType: typ, Status: "approved", AdditionalInfo: sql.NullString{String: "existing note", Valid: true}})
		if got.Id != "historical" || got.Username != "owner" || got.VerificationType != typ || got.Status != "approved" || got.AdditionalInfo != "existing note" {
			t.Fatalf("historical record changed: %+v", got)
		}
	}
}

func TestVerificationPolicyRejectsUnnamedCountries(t *testing.T) {
	svc := &UserSvc{}
	for _, country := range []string{"OT", "ZZ", "XX", "UK"} {
		_, err := svc.SubmitVerificationRequest(context.Background(), &pb.SubmitVerificationReq{Username: "local-test", VerificationType: "id_document", Country: country, IdDocumentType: "government_id"})
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "issuing country") {
			t.Fatalf("unnamed country %q must fail country validation: %v", country, err)
		}
	}
}
