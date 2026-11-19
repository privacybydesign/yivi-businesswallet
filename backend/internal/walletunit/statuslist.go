package walletunit

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"
)

// Token Status List (draft-ietf-oauth-status-list-20, the version CS-05 8.2 pins).
const (
	// statusBits is 2 so an entry can say suspended as well as invalid.
	statusBits = 2
	// statusListSize entries per list: 32 KiB uncompressed at 2 bits each, and
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
	statusValid     byte = 0
	statusInvalid   byte = 1
	statusSuspended byte = 2
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

// encodeStatusList packs statuses (list-local index → value) at statusBits per
// entry, least significant bits first, and returns the zlib-compressed base64url lst.
func encodeStatusList(statuses map[int64]byte) (string, error) {
	const perByte = 8 / statusBits
	bits := make([]byte, statusListSize/perByte)
	for idx, v := range statuses {
		if idx < 0 || idx >= statusListSize {
			return "", fmt.Errorf("walletunit: status index %d outside the list", idx)
		}
		bits[idx/perByte] |= v << (uint(idx%perByte) * statusBits)
	}
	var buf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(bits); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}
