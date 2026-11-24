// Package statuslist encodes IETF Token Status Lists (draft-ietf-oauth-status-list).
package statuslist

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
)

// TokenTyp is the JWT typ of a Status List Token, and ContentType how it is served.
const (
	TokenTyp    = "statuslist+jwt"
	ContentType = "application/statuslist+jwt"
)

// Encode packs statuses (list-local index → value) into a list of size entries at
// bits per entry, least significant bits first, and returns the zlib-compressed
// base64url lst.
func Encode(statuses map[int64]byte, size int64, bits int) (string, error) {
	perByte := int64(8 / bits)
	packed := make([]byte, size/perByte)
	for idx, v := range statuses {
		if idx < 0 || idx >= size {
			return "", fmt.Errorf("statuslist: index %d outside the list", idx)
		}
		packed[idx/perByte] |= v << (uint(idx%perByte) * uint(bits))
	}
	var buf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(packed); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}
