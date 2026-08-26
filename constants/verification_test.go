package constants

import "testing"

// Guards the two rules most likely to regress during edits:
//  1. passports/residence permits are universal, even for unmapped countries
//  2. domestic doc types are honored ONLY where listed
func TestIsAllowedIDDocument(t *testing.T) {
	cases := []struct {
		country, doc string
		want         bool
	}{
		{"IN", IDDocumentPassport, true},
		{"ZZ", IDDocumentPassport, true},       // unmapped country -> universal set
		{"", IDDocumentResidencePermit, true},  // blank country -> universal set
		{"US", IDDocumentNationalID, false},    // no federal card
		{"us", IDDocumentDriversLicense, true}, // case-normalized
		{" IN ", "aadhaar_like", false},        // unknown type never passes
	}
	for _, c := range cases {
		if got := IsAllowedIDDocument(c.country, c.doc); got != c.want {
			t.Errorf("IsAllowedIDDocument(%q, %q) = %v, want %v", c.country, c.doc, got, c.want)
		}
	}
}

func TestIsValidVerificationType(t *testing.T) {
	for _, ok := range []string{VerificationTypeSocialProof, VerificationTypeIDDocument} {
		if !IsValidVerificationType(ok) {
			t.Errorf("IsValidVerificationType(%q) = false, want true", ok)
		}
	}
	if IsValidVerificationType("professional") { // legacy: not issuable anymore
		t.Error("'professional' should no longer be issuable")
	}
	if IsValidVerificationType("") || IsValidVerificationType("id_doc") {
		t.Error("empty/typo'd type must be rejected")
	}
}
