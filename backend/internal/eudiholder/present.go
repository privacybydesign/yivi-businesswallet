package eudiholder

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"github.com/privacybydesign/irmago/eudi/openid4vp"
)

// ErrPresentNotImplemented is returned by the irmago engine's Present until the
// DCQL match, selective disclosure and KB-JWT signing land (issue #112). The seam
// exists so the inbound OpenID4VP slice (internal/openid4vppresenter) is written
// and tested against a fixed call shape now; the stub returns a canned
// presentation so that slice runs end to end in dev / CI.
var ErrPresentNotImplemented = errors.New("eudiholder: Present is not implemented yet (#112)")

// Presentation is the outcome of Present: the vp_token an OpenID4VP Authorization
// Response carries, keyed by DCQL credential-query id (OpenID4VP 1.0 §8.1). Each
// entry is one or more presentations of the matching held credential — for
// dc+sd-jwt the compact SD-JWT VC with its key-binding JWT appended.
type Presentation struct {
	VPToken openid4vp.VpToken
}

// PresentationFormats is what the holder engine can present, in the shape the
// wallet-metadata document publishes (OpenID4VP 1.0 §11.1 vp_formats_supported).
// It is derived from the engine's own signing contract, not asserted next to it:
// both the software binder and the WSCA binder sign through irmago's
// HolderSigner.SignES256, and the SD-JWT VCs the engine accepts are issued under
// the same algorithm (irmago eudi/sdjwt signs with jwt.SigningMethodES256).
type PresentationFormats struct {
	// SDJWTVC is the dc+sd-jwt entry; nil formats are not published.
	SDJWTVC *SDJWTVCFormat
}

// SDJWTVCFormat mirrors the dc+sd-jwt vp_formats_supported entry.
type SDJWTVCFormat struct {
	SDJWTAlgValues []string `json:"sd-jwt_alg_values"`
	KBJWTAlgValues []string `json:"kb-jwt_alg_values"`
}

// FormatSDJWTVC is the OpenID4VP format identifier for IETF SD-JWT VC.
const FormatSDJWTVC = "dc+sd-jwt"

// Formats reports the presentation formats the holder engine supports. The
// algorithm is read from the JOSE library irmago signs with, so this cannot
// drift from what a key-binding JWT actually carries.
func Formats() PresentationFormats {
	alg := jwt.SigningMethodES256.Alg()
	return PresentationFormats{
		SDJWTVC: &SDJWTVCFormat{
			SDJWTAlgValues: []string{alg},
			KBJWTAlgValues: []string{alg},
		},
	}
}

// dcqlCredentialIDs lists the credential-query ids of a DCQL query, in order.
// The stub uses it to shape a canned vp_token; the real engine matches on the
// full query (#112).
func dcqlCredentialIDs(dcqlQuery []byte) ([]string, error) {
	var q struct {
		Credentials []struct {
			ID string `json:"id"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(dcqlQuery, &q); err != nil {
		return nil, fmt.Errorf("eudiholder: decode dcql_query: %w", err)
	}
	ids := make([]string, 0, len(q.Credentials))
	for _, c := range q.Credentials {
		if c.ID == "" {
			return nil, errors.New("eudiholder: dcql_query credential without id")
		}
		ids = append(ids, c.ID)
	}
	return ids, nil
}
