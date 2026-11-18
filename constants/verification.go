package constants

import (
	"strings"
	"time"
)

// Verification request types. Social proof is retained for historical records only.
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
	IDDocumentGovernmentID    = "government_id"
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

// AllowedIDDocuments contains historical country hints retained for source compatibility.
// Deprecated: this map is not the current acceptance policy. Use IsAllowedIDDocument;
// every supported government-issued category is reviewable for every named country.
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

// acceptedGovernmentIDDocs are reviewable for every country. The reviewer
// checks the issuing authority and authenticity; selecting a category never
// approves an account automatically.
var acceptedGovernmentIDDocs = map[string]struct{}{
	IDDocumentPassport:        {},
	IDDocumentNationalID:      {},
	IDDocumentDriversLicense:  {},
	IDDocumentResidencePermit: {},
	IDDocumentGovernmentID:    {},
}

// IsAllowedIDDocument accepts supported government-issued document categories
// without nationality-based exclusions. Country shape is validated at submission.
func IsAllowedIDDocument(_ string, docType string) bool {
	_, ok := acceptedGovernmentIDDocs[docType]
	return ok
}

// IsValidVerificationType controls new submissions only. Existing social-proof
// and professional records remain readable and retain their review status.
func IsValidVerificationType(t string) bool {
	return t == VerificationTypeIDDocument
}

// verificationCountries contains the 249 ISO country/territory codes plus XK
// (Kosovo), matching the named application selector. Historical rows are not modified.
var verificationCountries = func() map[string]struct{} {
	result := make(map[string]struct{}, 250)
	for _, code := range strings.Fields("AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS XK YE YT ZA ZM ZW") {
		result[code] = struct{}{}
	}
	return result
}()

func IsValidVerificationCountry(country string) bool {
	_, ok := verificationCountries[strings.ToUpper(strings.TrimSpace(country))]
	return ok
}
