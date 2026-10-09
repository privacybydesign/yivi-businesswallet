package proofing

// HTTP header names and values the proofing routes set or read.
const (
	headerAuthorization      = "Authorization"
	headerWWWAuthenticate    = "WWW-Authenticate"
	headerRetryAfter         = "Retry-After"
	headerContentType        = "Content-Type"
	headerContentDisposition = "Content-Disposition"
	headerCacheControl       = "Cache-Control"
	headerCSP                = "Content-Security-Policy"
	headerIdempotentReplayed = "Idempotent-Replayed"
	contentTypeJSON          = "application/json"
)
