package openid4vppresenter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
	"github.com/privacybydesign/irmago/eudi/openid4vp"
)

// VerifyingValidator is the production Request Object validator: irmago's
// relying-party validator — the same code the Yivi wallet runs — verifies the
// JAR's signature against the x5c leaf, the leaf's chain against the configured
// verifier trust (eudiholder.NewVerifierTrust) with the client_id's DNS name as
// the required SAN, revocation, and, for a Yivi-issued relying-party
// certificate, that the DCQL query stays within the credentials the certificate
// authorizes the verifier to ask for. On top of that the protocol constraints
// this slice can answer are applied (checkFields).
type VerifyingValidator struct {
	inner  *openid4vp.RequestorCertificateStoreVerifierValidator
	policy Policy
}

func NewVerifyingValidator(trust eudijwt.X509VerificationContext, policy Policy) *VerifyingValidator {
	return &VerifyingValidator{
		inner:  openid4vp.NewRequestorCertificateStoreVerifierValidator(trust, &openid4vp.DefaultQueryValidatorFactory{}),
		policy: policy,
	}
}

// The prefixes irmago's x509 validator authenticates.
var verifyingClientIDPrefixes = []string{
	strings.TrimSuffix(string(openid4vp.ClientIdentifierPrefix_X509SanDns), ":"),
	strings.TrimSuffix(string(openid4vp.ClientIdentifierPrefix_X509Hash), ":"),
}

func (*VerifyingValidator) ClientIDPrefixes() []string { return verifyingClientIDPrefixes }

// identityLocale is the language the verifier's legal name is resolved in for
// the org picker; the name is shown to a member, the picker is not localised
// per member yet.
const identityLocale = "en"

func (v *VerifyingValidator) Validate(_ context.Context, clientID string, requestObject []byte) (RequestObject, error) {
	raw := strings.TrimSpace(string(requestObject))
	req, leaf, requestor, err := v.inner.ParseAndVerifyAuthorizationRequest(raw)
	if err != nil {
		return RequestObject{}, fmt.Errorf("%w: %w", ErrInvalidRequestObject, err)
	}
	// Who is asking: the certified legal name when the certificate or the request
	// carries one, else the certificate's subject, else the client_id's DNS name.
	identity := ""
	if requestor != nil {
		identity = requestor.Organization.LegalName[identityLocale]
		if identity == "" {
			for _, name := range requestor.Organization.LegalName {
				identity = name
				break
			}
		}
	}
	if identity == "" && leaf != nil {
		identity = leaf.Subject.CommonName
	}
	if identity == "" {
		identity = strings.TrimPrefix(clientID, string(openid4vp.ClientIdentifierPrefix_X509SanDns))
	}

	dcqlQuery, err := json.Marshal(req.DcqlQuery)
	if err != nil {
		return RequestObject{}, fmt.Errorf("%w: dcql_query: %w", ErrInvalidRequestObject, err)
	}
	f := requestFields{
		clientID:     req.ClientId,
		responseType: req.ResponseType,
		responseMode: string(req.ResponseMode),
		responseURI:  req.ResponseUri,
		nonce:        req.Nonce,
		state:        req.State,
		dcqlQuery:    dcqlQuery,
	}
	if req.ClientMetadata != nil && req.ClientMetadata.Jwks != nil {
		keys, err := json.Marshal(req.ClientMetadata.Jwks)
		if err != nil {
			return RequestObject{}, fmt.Errorf("%w: client_metadata.jwks: %w", ErrInvalidRequestObject, err)
		}
		f.encryptionKeys = keys
	}
	return checkFields(v.policy, clientID, identity, raw, f)
}
