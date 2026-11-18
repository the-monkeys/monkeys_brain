package constants

import "testing"

func TestIsAllowedIDDocument(t *testing.T) {
	for _, country := range []string{"IN", "US", "AU", "NP", "BT", "FJ", "PS", "TW", "ZZ", ""} {
		for _, doc := range []string{IDDocumentPassport, IDDocumentNationalID, IDDocumentDriversLicense, IDDocumentResidencePermit, "government_id"} {
			if !IsAllowedIDDocument(country, doc) {
				t.Errorf("government document %q rejected for %q", doc, country)
			}
		}
	}
	for _, doc := range []string{"", "social_proof", "professional", "unrecognized_document"} {
		if IsAllowedIDDocument("IN", doc) {
			t.Errorf("unsupported document category %q accepted", doc)
		}
	}
}

func TestIsValidVerificationType(t *testing.T) {
	if !IsValidVerificationType(VerificationTypeIDDocument) {
		t.Fatal("government ID must remain issuable")
	}
	for _, typ := range []string{VerificationTypeSocialProof, "professional", "", "id_doc"} {
		if IsValidVerificationType(typ) {
			t.Errorf("non-government verification %q must not be issuable", typ)
		}
	}
}

func TestVerificationCountries(t *testing.T) {
	if len(verificationCountries) != 250 {
		t.Fatalf("named country coverage: %d", len(verificationCountries))
	}
	for _, code := range []string{"NP", "BT", "PS", "TW", "XK", "FJ", "IN", " us "} {
		if !IsValidVerificationCountry(code) {
			t.Errorf("named country %q unavailable", code)
		}
	}
	for _, code := range []string{"OT", "ZZ", "XX", "UK", "", "IND", "123"} {
		if IsValidVerificationCountry(code) {
			t.Errorf("invented or unnamed country %q accepted", code)
		}
	}
}
