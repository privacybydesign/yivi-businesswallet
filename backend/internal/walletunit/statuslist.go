package walletunit

import (
	"strconv"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/statuslist"
)

// Token Status List (draft-ietf-oauth-status-list-20, the version CS-05 8.2 pins).
const (
	// statusBits is 1: WE BUILD CS-10 (ISS-RV-13) knows only valid and invalid.
	statusBits = 1
	// statusListSize entries per list: 16 KiB uncompressed at 1 bit each, and
	// mostly zeros, so a few hundred bytes on the wire.
	statusListSize = 1 << 17
	// statusListCount lists share one index space; an index picks its list.
	statusListCount = 16
	// statusListTokenTTL bounds how long a fetched list may be used, and ttl how
	// long a relying party may cache it before fetching again.
	statusListTokenTTL = 24 * time.Hour
	statusListCacheTTL = time.Hour

	// statusListPath is the public form of the GET /wallet-unit/status-lists/{list}
	// route, which relying parties fetch unauthenticated.
	statusListPath = "/api/v1/wallet-unit/status-lists/"
)

// Status values (draft-ietf-oauth-status-list-20 §7.1).
const (
	statusValid   byte = 0
	statusInvalid byte = 1
)

// statusRef is the status_list reference a BWIA or SKA carries for global index idx.
func statusRef(baseURL string, idx int64) map[string]any {
	return map[string]any{
		"idx": idx % statusListSize,
		"uri": statusListURI(baseURL, idx/statusListSize),
	}
}

func statusListURI(baseURL string, list int64) string {
	return baseURL + statusListPath + strconv.FormatInt(list, 10)
}

// encodeStatusList packs statuses (list-local index → value) into one list.
func encodeStatusList(statuses map[int64]byte) (string, error) {
	return statuslist.Encode(statuses, statusListSize, statusBits)
}
