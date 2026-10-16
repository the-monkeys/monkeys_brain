package staffaccess

// Diagnostic identifiers carry no credentials and never grant permissions.
const (
	RequestIDContextKey  = "staff_request_id"
	RequestIDMetadataKey = "x-monkeys-staff-request-id"
)
