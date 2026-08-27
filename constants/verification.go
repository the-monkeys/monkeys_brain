package constants

import (
	"strings"
	"time"
)

// Issuable verification request types (verification_requests.verification_type).
// The legacy 'professional' type from schema/000004 remains readable but
// is no longer issuable through the API.
const (
	VerificationTypeSocialProof = "social_proof"
	VerificationTypeIDDocument  = "id_document"
)

// Decision lifecycle (verification_requests.status). Mirrored by the
// chk_verification_status CHECK constraint added in schema/000013.
const (
	VerificationStatusPending     = "pending"
	VerificationStatusUnderReview = "under_review"
	VerificationStatusApproved    = "approved"
	VerificationStatusRejected    = "rejected"
)

// Government ID document types accepted for id_document verification
// (verification_requests.id_document_type).
const (
	IDDocumentPassport        = "passport"
	IDDocumentNationalID      = "national_id"
	IDDocumentDriversLicense  = "drivers_license"
	IDDocumentResidencePermit = "residence_permit"
)

// Upload slots for the private-bucket asset endpoint
// (POST /api/v2/storage/verifications, multipart field kind=<one of these>).
const (
	VerificationKindSelfie  = "selfie"
	VerificationKindIDFront = "id_front"
	VerificationKindIDBack  = "id_back"
)

// Policy limits shared by the gateway upload handlers and the users
// service's submission validation. One source of truth keeps both sides
// of the checksum handoff consistent.
const (
	// MaxAdditionalInfoLen caps the free-text context submitted with a
	// verification request.
	MaxAdditionalInfoLen = 2000

	// VerificationAssetMaxBytes caps each uploaded document image at 10 MiB.
	VerificationAssetMaxBytes = 10 << 20

	// VerificationPresignTTL bounds reviewer-facing presigned asset URLs.
	// Deliberately short: verification documents are PII.
	VerificationPresignTTL = 10 * time.Minute

	// VerificationPurgeAfter is the minimum age after a terminal decision
	// before document objects may be purged from object storage.
	VerificationPurgeAfter = 30 * 24 * time.Hour
)

// AllowedIDDocuments maps ISO 3166-1 alpha-2 codes to the DOMESTIC id
// types commonly issued there. Passports and residence permits are valid
// for every country (see universalIDDocs) and are deliberately NOT
// repeated per entry — this keeps the table small and the fallback rule
// obvious.
//
// This is a pragmatic starter set (~40 launch regions); extend entries as
// review volume demands. A country without an entry falls back to the
// universal set only — conservative, but never rejects a passport holder.
var AllowedIDDocuments = map[string][]string{
	"AR": {IDDocumentNationalID, IDDocumentDriversLicense}, // DNI
	"AU": {IDDocumentDriversLicense},                       // no federal ID card
	"BD": {IDDocumentNationalID, IDDocumentDriversLicense}, // NID
	"BR": {IDDocumentNationalID, IDDocumentDriversLicense}, // RG / CNH
	"CA": {IDDocumentDriversLicense},                       // provincial ID ≈ DL scope
	"CH": {IDDocumentNationalID, IDDocumentDriversLicense},
	"CL": {IDDocumentNationalID, IDDocumentDriversLicense}, // RUN
	"CN": {IDDocumentNationalID, IDDocumentResidencePermit},
	"CO": {IDDocumentNationalID, IDDocumentDriversLicense}, // cédula
	"DE": {IDDocumentNationalID, IDDocumentDriversLicense}, // Personalausweis
	"EG": {IDDocumentNationalID, IDDocumentDriversLicense},
	"ES": {IDDocumentNationalID, IDDocumentDriversLicense}, // DNI
	"FR": {IDDocumentNationalID, IDDocumentDriversLicense}, // CNIE
	"GB": {IDDocumentDriversLicense},                       // no national ID card
	"GH": {IDDocumentNationalID, IDDocumentDriversLicense}, // Ghana Card
	"ID": {IDDocumentNationalID, IDDocumentDriversLicense}, // KTP
	"IE": {IDDocumentDriversLicense},
	"IL": {IDDocumentNationalID, IDDocumentDriversLicense}, // Teudat Zehut
	"IN": {IDDocumentNationalID, IDDocumentDriversLicense}, // Aadhaar / DL
	"IT": {IDDocumentNationalID, IDDocumentDriversLicense},
	"JP": {IDDocumentNationalID, IDDocumentDriversLicense}, // My Number / licence
	"KE": {IDDocumentNationalID, IDDocumentDriversLicense},
	"KR": {IDDocumentNationalID, IDDocumentDriversLicense}, // RRN
	"MA": {IDDocumentNationalID, IDDocumentDriversLicense},
	"MX": {IDDocumentNationalID, IDDocumentDriversLicense}, // CURP / INE
	"MY": {IDDocumentNationalID, IDDocumentDriversLicense}, // MyKad
	"NG": {IDDocumentNationalID, IDDocumentDriversLicense}, // NIN
	"NL": {IDDocumentNationalID, IDDocumentDriversLicense},
	"NZ": {IDDocumentDriversLicense},
	"PH": {IDDocumentNationalID, IDDocumentDriversLicense}, // PhilSys
	"PK": {IDDocumentNationalID, IDDocumentDriversLicense}, // CNIC
	"PL": {IDDocumentNationalID, IDDocumentDriversLicense},
	"SA": {IDDocumentNationalID, IDDocumentDriversLicense},
	"SE": {IDDocumentNationalID, IDDocumentDriversLicense}, // personnummer card
	"SG": {IDDocumentNationalID, IDDocumentDriversLicense}, // NRIC
	"TH": {IDDocumentNationalID, IDDocumentDriversLicense},
	"TR": {IDDocumentNationalID, IDDocumentDriversLicense}, // TC Kimlik
	"UA": {IDDocumentNationalID, IDDocumentDriversLicense},
	"US": {IDDocumentDriversLicense}, // state DL/ID; no federal card
	"VN": {IDDocumentNationalID, IDDocumentDriversLicense},
	"ZA": {IDDocumentNationalID, IDDocumentDriversLicense}, // Smart ID
}

// universalIDDocs are accepted for every country, including countries
// absent from AllowedIDDocuments. Residence permits ride this lane because
// they evidence lawful residence rather than citizenship — many legitimate
// verifiers hold one instead of a domestic document.
var universalIDDocs = map[string]struct{}{
	IDDocumentPassport:        {},
	IDDocumentResidencePermit: {},
}

// IsAllowedIDDocument reports whether docType is reviewable for the given
// country. Input is normalized defensively; an unknown or unmapped
// country degrades gracefully to the universal set rather than erroring.
func IsAllowedIDDocument(country, docType string) bool {
	if _, ok := universalIDDocs[docType]; ok {
		return true
	}
	for _, d := range AllowedIDDocuments[strings.ToUpper(strings.TrimSpace(country))] {
		if d == docType {
			return true
		}
	}
	return false
}

// IsValidVerificationType reports whether t is issuable today. Legacy
// rows of other types stay readable; they just cannot be created again.
func IsValidVerificationType(t string) bool {
	return t == VerificationTypeSocialProof || t == VerificationTypeIDDocument
}
