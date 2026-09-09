package eudiholder

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/common/clientmodels"
	"github.com/privacybydesign/irmago/eudi/openid4vp"
	"github.com/privacybydesign/irmago/eudi/openid4vp/dcql"
	"github.com/privacybydesign/irmago/eudi/openid4vp/eudi_sdjwt_dcql"
	"github.com/privacybydesign/irmago/eudi/storage/db"
)

// Present answers an OpenID4VP Authorization Request from the organization's
// held credentials: irmago's DCQL planner finds the candidates in the org's
// storage and builds the disclosure plan, the plan is resolved headlessly (see
// autoSelect), and irmago's SD-JWT code derives the selective-disclosure
// presentation per credential and signs its key-binding JWT — bound to nonce and
// audience (the verifier's client_id) — through the configured holder-key binder,
// software or WSCA. No human picks here: who may let the response go out is the
// caller's (the consent layer's) decision, taken before Present is called.
func (e *Engine) Present(ctx context.Context, orgID uuid.UUID, dcqlQuery []byte, nonce, audience string) (Presentation, error) {
	var query dcql.DcqlQuery
	if err := json.Unmarshal(dcqlQuery, &query); err != nil {
		return Presentation{}, fmt.Errorf("eudiholder: decode dcql_query: %w", err)
	}
	st, err := e.engineFor(ctx, orgID)
	if err != nil {
		return Presentation{}, err
	}
	binder, err := e.presentationKeyBinder(ctx, orgID, st)
	if err != nil {
		return Presentation{}, err
	}
	// No type-metadata fetchers and no revocation checker: the fetchers only
	// describe credentials the wallet does not hold (a human-facing "you could
	// obtain this" hint this headless flow has no use for), and the revocation
	// flag is display state — a revoked credential is refused by the verifier.
	handler := &sdJwtVcQueries{eudi_sdjwt_dcql.NewSdJwtVcDcqlHandler(
		st, db.NewCredentialStore(st.Db()), nil, nil, binder,
		clientmodels.NewCurrentLocale(defaultHolderLocale), nil,
	)}
	planner := dcql.NewDcqlHandler([]dcql.DcqlCredentialQueryHandler{handler})

	result, err := planner.FindCandidates(query)
	if err != nil {
		return Presentation{}, fmt.Errorf("eudiholder: match dcql_query org %s: %w", orgID, err)
	}
	plan, err := planner.BuildDisclosurePlan(query, result, nil, dcql.CollectOwnedHashes(result.QueryResults))
	if err != nil {
		return Presentation{}, fmt.Errorf("eudiholder: plan disclosure org %s: %w", orgID, err)
	}
	selections, err := autoSelect(plan, result.HashToQueryId)
	if err != nil {
		return Presentation{}, err
	}
	prepared, err := planner.PrepareDisclosure(query, selections, nonce, audience)
	if err != nil {
		return Presentation{}, fmt.Errorf("eudiholder: prepare disclosure org %s: %w", orgID, err)
	}

	token := make(openid4vp.VpToken, len(prepared.QueryResponses))
	for _, r := range prepared.QueryResponses {
		token[r.QueryId] = append(token[r.QueryId], r.Credentials...)
	}
	return Presentation{VPToken: token}, nil
}

// autoSelect resolves a disclosure plan without a human: for every required
// choice the first bundle the organization holds is taken, with exactly the claim
// paths the plan derived from the query (data minimisation: the plan already
// bundles only what the verifier asked for and what the issuer signed together).
// Optional choices are skipped — a verifier marks a set optional to say it can
// do without it, and an autonomous wallet does not volunteer data. A required
// choice with nothing held is ErrNoMatchingCredential.
func autoSelect(plan *clientmodels.DisclosurePlan, hashToQueryID map[string]string) ([]dcql.DisclosureSelection, error) {
	var selections []dcql.DisclosureSelection
	for _, pick := range plan.DisclosureChoicesOverview {
		if pick.Optional {
			continue
		}
		if len(pick.OwnedOptions) == 0 {
			return nil, ErrNoMatchingCredential
		}
		for _, cred := range pick.OwnedOptions[0].Credentials {
			queryID, ok := hashToQueryID[cred.Hash]
			if !ok {
				return nil, fmt.Errorf("eudiholder: plan names credential %q outside the query", cred.Hash)
			}
			paths := make([][]any, 0, len(cred.Attributes))
			for _, attr := range cred.Attributes {
				if len(attr.ClaimPath) > 0 {
					paths = append(paths, attr.ClaimPath)
				}
			}
			selections = append(selections, dcql.DisclosureSelection{
				QueryId:        queryID,
				CredentialHash: cred.Hash,
				ClaimPaths:     paths,
			})
		}
	}
	if len(selections) == 0 {
		return nil, ErrNoMatchingCredential
	}
	return selections, nil
}

// sdJwtVcQueries routes every SD-JWT VC credential query to irmago's EUDI-store
// handler. irmago's own CanHandleCredentialQuery hands a vct shaped like an IRMA
// scheme identifier (three dot-separated parts, e.g. nl.kvk.registration) to the
// wallet's IRMA store instead — a store this backend does not have, while the
// organization credentials it issues are named exactly that way. Here there is
// one store, so the format alone decides.
type sdJwtVcQueries struct {
	*eudi_sdjwt_dcql.SdJwtVcDcqlHandler
}

func (h *sdJwtVcQueries) CanHandleCredentialQuery(query dcql.CredentialQuery) bool {
	return query.Format == FormatSDJWTVC || query.Format == legacyFormatSDJWTVC
}

// legacyFormatSDJWTVC is the pre-1.0 OpenID4VP format identifier some verifiers
// still send; irmago accepts both.
const legacyFormatSDJWTVC = "vc+sd-jwt"
