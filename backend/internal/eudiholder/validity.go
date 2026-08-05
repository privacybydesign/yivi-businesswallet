package eudiholder

import (
	"time"

	"github.com/privacybydesign/irmago/eudi/credentials/statuslist"
)

// HeldValidity is a held credential's validity state as the holder engine already
// has it stored — nothing is fetched over the network to answer this. ExpiresAt is
// the credential's own exp claim (nil when the credential does not expire); Revoked
// reports that the last observed Token Status List bit for the credential read
// anything other than valid.
type HeldValidity struct {
	ExpiresAt *time.Time
	Revoked   bool
}

// statusRevoked applies irmago's own revocation policy (eudi/services'
// RevocationService) to a stored status bit: a credential counts as usable only
// while its bit reads VALID, so INVALID, SUSPENDED and application-specific
// statuses all read as revoked. UNKNOWN is the exception — it means "no status
// information" (never checked, or no status-list reference at all), not a bad
// status, so it is not revoked.
func statusRevoked(lastKnown uint8) bool {
	status := statuslist.Status(lastKnown)
	return status != statuslist.StatusValid && status != statuslist.StatusUnknown
}
