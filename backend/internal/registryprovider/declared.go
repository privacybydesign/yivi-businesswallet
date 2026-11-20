package registryprovider

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	maxLegalNameLength = 200
	outcomeDeclared    = "declared"
	euidPrefixNL       = "NLNHR."
)

var kvkNumberPattern = regexp.MustCompile(`^[0-9]{8}$`)

// DeclaredRegistry accepts a company as the requester states it, for a
// deployment with no connection to KVK. It consults nothing: the KVK number is
// only checked for its form, and the name and the requester's standing are their
// own statement (RegistrationAttestation.Declared). Who the requester is still
// comes from their wallet disclosure.
type DeclaredRegistry struct {
	db       database.DB
	recorder audit.Recorder
}

func NewDeclaredRegistry(db database.DB, recorder audit.Recorder) *DeclaredRegistry {
	return &DeclaredRegistry{db: db, recorder: recorder}
}

func (*DeclaredRegistry) Ping(context.Context) error { return nil }

func (r *DeclaredRegistry) Consult(ctx context.Context, req ConsultRequest) (RegistrationAttestation, error) {
	if !kvkNumberPattern.MatchString(req.KVKNumber) {
		recordDecision(ctx, r.db, r.recorder, req, decision{
			action: audit.KVKRegistrationNotValidated, outcome: outcome(false), reason: reasonUnknownKVK,
		})
		return RegistrationAttestation{}, ErrUnknownKVK
	}
	legalName := strings.TrimSpace(req.LegalName)
	if legalName == "" || len(legalName) > maxLegalNameLength {
		return RegistrationAttestation{}, ErrLegalNameRequired
	}
	recordDecision(ctx, r.db, r.recorder, req, decision{
		action: audit.KVKRegistrationDeclared, legalName: legalName, outcome: outcomeDeclared,
	})
	return RegistrationAttestation{
		KVKNumber: req.KVKNumber,
		LegalName: legalName,
		EUID:      euidPrefixNL + req.KVKNumber,
		// The requester is the only representative on record, and not as a
		// director: no register said so.
		Representatives: []Representative{{
			Kind:        KindOverig,
			GivenNames:  req.GivenNames,
			FamilyName:  req.FamilyName,
			DateOfBirth: req.DateOfBirth,
			Authority:   AuthoritySole,
		}},
		RequesterIsRepresentative: true,
		Declared:                  true,
		IssuedAt:                  time.Now().UTC(),
	}, nil
}
