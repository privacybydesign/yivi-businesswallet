package proofing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// A data request is a session on a flow of kind FlowDataAccess ("see my
// data", GDPR Art. 15) or FlowDataErasure ("delete my data", Art. 17): a
// person asked the customer for their data, and the customer sends them a
// session on that flow like any other. Once the person is proven, the wallet
// does not approve it: it finds the customer's sessions of that person
// (findDataMatches) and holds the session in needs_review. The reviewer sees
// who asks, for what, how surely they were proven and the matches, and their
// approval erases the approved sessions or opens their data for download
// (decideDataRequest).

// FlowKind is what a flow's sessions are for.
type FlowKind string

const (
	// FlowIdentity is an identity check: what every flow was before data
	// requests, and a flow's kind until an admin sets another.
	FlowIdentity    FlowKind = "identity"
	FlowDataAccess  FlowKind = "data_access"
	FlowDataErasure FlowKind = "data_erasure"
)

func (k FlowKind) valid() bool {
	return k == FlowIdentity || k == FlowDataAccess || k == FlowDataErasure
}

// dataRequest reports a kind whose sessions are a person's request for
// their data.
func (k FlowKind) dataRequest() bool { return k == FlowDataAccess || k == FlowDataErasure }

// MatchLevel is how surely a session is the requesting person's.
type MatchLevel string

const (
	// MatchStrong is a session proven with the same document as the request:
	// same name, date of birth, document type, issuing state and expiry.
	MatchStrong MatchLevel = "strong"
	// MatchProbable is the same name and date of birth on another document
	// (or one the session did not describe): likely the same person, not
	// proven.
	MatchProbable MatchLevel = "probable"
	// MatchEmail is an unfinished session (pending, in progress, expired or
	// cancelled) sent to exactly the e-mail address the request was sent to:
	// it holds the name and e-mail the customer typed, and no proofed identity
	// to match on. Listed after the others and only taken when the reviewer
	// ticks it.
	MatchEmail MatchLevel = "email"
)

const (
	// DataExportWindow is how long an approved "see my data" request's data
	// can be downloaded.
	DataExportWindow = 7 * 24 * time.Hour
	// hostedExportWindow is how long the same data downloads through the
	// hosted link, which only its bearer token guards: a day from the
	// approval, within DataExportWindow.
	hostedExportWindow = 24 * time.Hour
	// ErrorReviewRejected is the error code a rejected review records when
	// the reviewer names none, as the engine does.
	ErrorReviewRejected = "MANUAL_REVIEW_REJECTED"
)

var (
	// ErrFlowNoIdentity is a data request flow that does not read the name and
	// date of birth the person's sessions are found by.
	ErrFlowNoIdentity = errors.New("proofing: the flow does not read the name and date of birth")
	// ErrDataFlowForMember is a data request flow offered to, or sent to, a
	// member: only a customer's subjects ask for their data.
	ErrDataFlowForMember = errors.New("proofing: a data request flow is for a customer's subjects")
	// ErrExportUnavailable is an export of a request that is not an approved
	// "see my data" request, or whose download window passed.
	ErrExportUnavailable = errors.New("proofing: no data export is available for this session")
)

// DataMatch is one of the customer's sessions a data request matched.
type DataMatch struct {
	RequestID      uuid.UUID
	FlowName       string
	Status         Status
	Method         string
	AssuranceLevel string
	EIDASLevel     string
	CreatedAt      time.Time
	CompletedAt    *time.Time
	PurgedAt       *time.Time
	Level          MatchLevel
	// Approved is the reviewer's choice; nil until the decision.
	Approved *bool
}

// NewDataMatch is a match found when a data request goes to review.
type NewDataMatch struct {
	RequestID uuid.UUID
	Level     MatchLevel
}

type dataRequestStore interface {
	FlowKind(ctx context.Context, orgID uuid.UUID, flowID string) (FlowKind, error)
	AllFlowKinds(ctx context.Context, orgID uuid.UUID) (map[string]FlowKind, error)
	SaveFlowKind(ctx context.Context, orgID uuid.UUID, flowID string, kind FlowKind) (FlowKind, error)
	Candidates(ctx context.Context, req Request) ([]Request, error)
	EmailCandidates(ctx context.Context, req Request) ([]Request, error)
	SaveMatches(ctx context.Context, req Request, matches []NewDataMatch) error
	Matches(ctx context.Context, req Request) ([]DataMatch, error)
	MatchedRequests(ctx context.Context, req Request) ([]Request, error)
	RecordDecision(ctx context.Context, req Request, approved []uuid.UUID, exportUntil *time.Time) error
	RecordExported(ctx context.Context, req Request, sessions int) error
}

// flowKind is the flow's kind; without a store every flow is an identity check.
func (s *Service) flowKind(ctx context.Context, orgID uuid.UUID, flowID string) (FlowKind, error) {
	if s.dataRequests == nil {
		return FlowIdentity, nil
	}
	return s.dataRequests.FlowKind(ctx, orgID, flowID)
}

func (s *Service) allFlowKinds(ctx context.Context, orgID uuid.UUID) (map[string]FlowKind, error) {
	if s.dataRequests == nil {
		return map[string]FlowKind{}, nil
	}
	return s.dataRequests.AllFlowKinds(ctx, orgID)
}

// FlowKind returns one of the org's flows' kind.
func (s *Service) FlowKind(ctx context.Context, org Org, flowID string) (FlowKind, error) {
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return "", err
	}
	return s.flowKind(ctx, org.ID, flowID)
}

// SaveFlowKind sets one of the org's flows' kind. A data request kind needs a
// flow that reads the name and date of birth, and one members may not send:
// only a customer's subjects ask for their data. Requests already sent keep
// the kind they were sent with.
func (s *Service) SaveFlowKind(ctx context.Context, org Org, flowID string, kind FlowKind) (FlowKind, error) {
	if s.dataRequests == nil {
		return "", errors.New("proofing: no data request store configured")
	}
	if !kind.valid() {
		return "", fmt.Errorf("%w: kind is identity, data_access or data_erasure", ErrInvalidInput)
	}
	flows, err := s.Flows(ctx, org, FlowsAll)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == flowID })
	if i < 0 {
		return "", ErrFlowNotFound
	}
	if kind.dataRequest() {
		if !readsIdentity(flows[i].Flow) {
			return "", ErrFlowNoIdentity
		}
		if flows[i].Allowed {
			return "", ErrDataFlowForMember
		}
	}
	return s.dataRequests.SaveFlowKind(ctx, org.ID, flowID, kind)
}

// checkMemberFlowKinds refuses a members' selection holding a data request flow.
func (s *Service) checkMemberFlowKinds(ctx context.Context, orgID uuid.UUID, flowIDs []string) error {
	kinds, err := s.allFlowKinds(ctx, orgID)
	if err != nil {
		return err
	}
	for _, id := range flowIDs {
		if kinds[id].dataRequest() {
			return ErrDataFlowForMember
		}
	}
	return nil
}

// SubjectIdentity is who a session proofed: what a data request's matches are
// found by and compared on.
type SubjectIdentity struct {
	GivenName    string
	FamilyName   string
	BirthDate    string
	DocumentType string
	IssuingState string
	ExpiryDate   string
}

func subjectIdentityOf(id proofingprovider.Identity) SubjectIdentity {
	out := SubjectIdentity{GivenName: id.GivenName, FamilyName: id.FamilyName, BirthDate: id.BirthDate}
	if id.Evidence != nil {
		out.DocumentType, out.IssuingState, out.ExpiryDate = id.Evidence.DocumentType, id.Evidence.IssuingState, id.Evidence.ExpiryDate
	}
	return out
}

// findDataMatches reads who a data request proofed and stores the customer's
// sessions of that person, for the review. A session's kept proofed name
// rules it out without reading the engine when the person's family name is
// not in it. A proof without a name or date of birth matches nothing.
func (s *Service) findDataMatches(ctx context.Context, tenant proofingprovider.Tenant, req Request) error {
	if s.dataRequests == nil || req.session == nil {
		return nil
	}
	proof, err := s.ips.SessionIdentity(ctx, tenant, req.session.ID, req.session.Token)
	if err != nil {
		return fmt.Errorf("proofing: data request %s identity: %w", req.ID, err)
	}
	requester := subjectIdentityOf(proof)
	matches := []NewDataMatch{}
	if requester.FamilyName != "" && requester.BirthDate != "" {
		candidates, err := s.dataRequests.Candidates(ctx, req)
		if err != nil {
			return err
		}
		for _, c := range candidates {
			if c.session == nil || (c.ProofedName != "" && !nameHolds(c.ProofedName, requester.FamilyName)) {
				continue
			}
			held, err := s.ips.SessionIdentity(ctx, requestTenant(c), c.session.ID, c.session.Token)
			if errors.Is(err, proofingprovider.ErrNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("proofing: data request %s match identity request %s: %w", req.ID, c.ID, err)
			}
			if level, ok := matchLevel(requester, held); ok {
				matches = append(matches, NewDataMatch{RequestID: c.ID, Level: level})
			}
		}
	}
	emailed, err := s.emailMatches(ctx, req, matches)
	if err != nil {
		return err
	}
	return s.dataRequests.SaveMatches(ctx, req, append(matches, emailed...))
}

// emailMatches are req's MatchEmail matches: the customer's unfinished
// sessions sent to the address req was sent to, apart from those already
// matched on identity. None when req has no address.
func (s *Service) emailMatches(ctx context.Context, req Request, matched []NewDataMatch) ([]NewDataMatch, error) {
	if strings.TrimSpace(req.SubjectEmail) == "" {
		return nil, nil
	}
	candidates, err := s.dataRequests.EmailCandidates(ctx, req)
	if err != nil {
		return nil, err
	}
	out := []NewDataMatch{}
	for _, c := range candidates {
		if !slices.ContainsFunc(matched, func(m NewDataMatch) bool { return m.RequestID == c.ID }) {
			out = append(out, NewDataMatch{RequestID: c.ID, Level: MatchEmail})
		}
	}
	return out, nil
}

// nameHolds reports whether every word of familyName is in name, compared as
// diploma.Normalize does (case, accents and punctuation aside).
func nameHolds(name, familyName string) bool {
	words := strings.Fields(diploma.Normalize(name))
	for _, w := range strings.Fields(diploma.Normalize(familyName)) {
		if !slices.Contains(words, w) {
			return false
		}
	}
	return true
}

// matchLevel compares the identity a session holds with the requester's: the
// same full name, every given name and the family name in order
// (diploma.SameName), and the same date of birth is a match, MatchStrong when
// the session was proven with the same document. Nothing looser: a match can
// be erased. The document number is never read.
func matchLevel(requester SubjectIdentity, held proofingprovider.Identity) (MatchLevel, bool) {
	if !samePerson(requester.GivenName, requester.FamilyName, requester.BirthDate, held.GivenName+" "+held.FamilyName, held.BirthDate) {
		return "", false
	}
	if sameDocument(requester, subjectIdentityOf(held)) {
		return MatchStrong, true
	}
	return MatchProbable, true
}

// samePerson reports whether a person with these given and family names,
// born on birthDate, is the one named fullName born on otherBirthDate: every
// word of the name in order (diploma.SameName) and the same date.
func samePerson(givenName, familyName, birthDate, fullName, otherBirthDate string) bool {
	if familyName == "" || birthDate == "" || otherBirthDate == "" {
		return false
	}
	a, errA := diploma.ParseDate(birthDate)
	b, errB := diploma.ParseDate(otherBirthDate)
	return errA == nil && errB == nil && a.Equal(b) && diploma.SameName(givenName+" "+familyName, fullName)
}

func sameDocument(a, b SubjectIdentity) bool {
	return a.DocumentType != "" && a.IssuingState != "" && a.ExpiryDate != "" &&
		strings.EqualFold(a.DocumentType, b.DocumentType) && strings.EqualFold(a.IssuingState, b.IssuingState) &&
		a.ExpiryDate == b.ExpiryDate
}

// DataMatches are the customer's sessions a data request matched, for its
// review; none for any other request.
func (s *Service) DataMatches(ctx context.Context, orgID, id uuid.UUID) (Request, []DataMatch, error) {
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, nil, err
	}
	if req.CustomerID == nil || !req.FlowKind.dataRequest() || s.dataRequests == nil {
		return req, []DataMatch{}, nil
	}
	matches, err := s.dataRequests.Matches(ctx, req)
	return req, matches, err
}

// decideDataRequest is DecideReview for a data request, which the wallet
// holds in review itself (the engine approved the person). Approving takes the
// approved matches (every one when approved is nil): an erasure purges each of
// those sessions, then the request's own personal data (it held who asked
// only for the review); an access request opens their data for
// DataExportWindow. Rejecting records ErrorReviewRejected unless the reviewer
// named a code. A session the engine itself holds in review is decided there
// too.
func (s *Service) decideDataRequest(ctx context.Context, req Request, reviewer string, in ReviewInput) (Request, error) {
	// The reviewer's choice is checked before anything is decided anywhere.
	matches, err := s.dataRequests.Matches(ctx, req)
	if err != nil {
		return Request{}, err
	}

	approved := []uuid.UUID{}
	if in.Approve {
		if approved, err = approvedMatches(matches, in.RequestIDs); err != nil {
			return Request{}, err
		}
	}

	tenant := requestTenant(req)
	held, err := s.ips.SessionStatus(ctx, tenant, req.session.ID, req.session.Token)
	if err != nil && !errors.Is(err, proofingprovider.ErrNotFound) {
		return Request{}, fmt.Errorf("proofing: decide data request %s: %w", req.ID, err)
	}

	if err == nil && held.Status == proofingprovider.StatusNeedsReview {
		decision := proofingprovider.ReviewDecision{
			Approve: in.Approve, ErrorCode: in.ErrorCode, Reason: in.Reason, Reviewer: reviewer,
		}
		if err := s.ips.DecideReview(ctx, tenant, req.session.ID, req.session.Token, decision); err != nil {
			return Request{}, fmt.Errorf("proofing: decide data request %s at the engine: %w", req.ID, err)
		}
	}

	if in.Approve && req.FlowKind == FlowDataErasure {
		if err := s.purgeMatches(ctx, req, approved); err != nil {
			return Request{}, err
		}
	}

	var exportUntil *time.Time
	if in.Approve && req.FlowKind == FlowDataAccess {
		until := s.now().Add(DataExportWindow)
		exportUntil = &until
	}

	decided, code := StatusApproved, ""
	if !in.Approve {
		decided, code = StatusRejected, in.ErrorCode
		if code == "" {
			code = ErrorReviewRejected
		}
	}

	if err := s.dataRequests.RecordDecision(ctx, req, approved, exportUntil); err != nil {
		return Request{}, err
	}

	if err := s.requests.RecordReviewDecision(ctx, req, decided, in.Reason, code); err != nil {
		return Request{}, err
	}

	if err := s.requests.RecordOutcome(ctx, req, req.session.ID, decided, proofingprovider.Result{
		Method: req.Method, AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel, ErrorCode: code,
		Name: req.ProofedName,
	}); err != nil {
		return Request{}, err
	}

	out, err := s.requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil {
		return Request{}, err
	}

	if in.Approve && req.FlowKind == FlowDataErasure {
		if err := s.purge(ctx, out); err != nil {
			return Request{}, err
		}
		return s.requests.Get(ctx, req.OrganizationID, req.ID)
	}

	return out, nil
}

// approvedMatches checks a reviewer's approved sessions against the matches;
// nil approves only the MatchStrong ones, proven with the same document: a
// MatchProbable or MatchEmail one is taken only by a reviewer's own tick.
func approvedMatches(matches []DataMatch, approved []uuid.UUID) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.RequestID)
	}
	if approved == nil {
		strong := []uuid.UUID{}
		for _, m := range matches {
			if m.Level == MatchStrong {
				strong = append(strong, m.RequestID)
			}
		}
		return strong, nil
	}
	out := []uuid.UUID{}
	for _, id := range approved {
		if !slices.Contains(ids, id) {
			return nil, fmt.Errorf("%w: session %s is not one of this request's matches", ErrInvalidInput, id)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// purgeMatches purges each approved match not purged already; a purge that
// fails stops the decision, and a retry purges what is left.
func (s *Service) purgeMatches(ctx context.Context, req Request, approved []uuid.UUID) error {
	reqs, err := s.dataRequests.MatchedRequests(ctx, req)
	if err != nil {
		return err
	}
	for _, m := range reqs {
		if m.PurgedAt != nil || !slices.Contains(approved, m.ID) {
			continue
		}
		if err := s.purge(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// DataExport is the data an approved "see my data" request found: each
// approved session as the wallet holds it. Images are named, not included.
type DataExport struct {
	RequestID  uuid.UUID
	ExportedAt time.Time
	Sessions   []DataExportSession
}

// DataExportSession is one session in a DataExport.
type DataExportSession struct {
	Request  Request
	Identity *proofingprovider.Identity
	Diplomas []Diploma
}

// dataExport builds req's export while it is open, and never once req is
// erased: audited identity_proofing.data_exported by the actor in ctx.
func (s *Service) dataExport(ctx context.Context, req Request) (DataExport, error) {
	if s.dataRequests == nil || req.FlowKind != FlowDataAccess || req.Status != StatusApproved || req.PurgedAt != nil ||
		req.DataExportUntil == nil || !s.now().Before(*req.DataExportUntil) {
		return DataExport{}, ErrExportUnavailable
	}
	matches, err := s.dataRequests.Matches(ctx, req)
	if err != nil {
		return DataExport{}, err
	}
	approved := []uuid.UUID{}
	for _, m := range matches {
		if m.Approved != nil && *m.Approved {
			approved = append(approved, m.RequestID)
		}
	}
	reqs, err := s.dataRequests.MatchedRequests(ctx, req)
	if err != nil {
		return DataExport{}, err
	}
	diplomas, err := s.RequestDiplomas(ctx, approved)
	if err != nil {
		return DataExport{}, err
	}
	out := DataExport{RequestID: req.ID, ExportedAt: s.now(), Sessions: []DataExportSession{}}
	for _, m := range reqs {
		if !slices.Contains(approved, m.ID) {
			continue
		}
		session := DataExportSession{Request: m, Diplomas: diplomas[m.ID]}
		if m.PurgedAt == nil && m.session != nil && m.Status.Settled() {
			held, err := s.ips.SessionIdentity(ctx, requestTenant(m), m.session.ID, m.session.Token)
			switch {
			case errors.Is(err, proofingprovider.ErrNotFound):
			case err != nil:
				return DataExport{}, fmt.Errorf("proofing: export identity request %s: %w", m.ID, err)
			default:
				session.Identity = &held
			}
		}
		out.Sessions = append(out.Sessions, session)
	}
	if err := s.dataRequests.RecordExported(ctx, req, len(out.Sessions)); err != nil {
		return DataExport{}, err
	}
	return out, nil
}

// AdminDataExport is an approved "see my data" request's data for an org
// admin, to hand the person who asked.
func (s *Service) AdminDataExport(ctx context.Context, orgID, id uuid.UUID) (DataExport, error) {
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return DataExport{}, err
	}
	return s.dataExport(ctx, req)
}

// CustomerDataExport is an approved "see my data" request's data for the
// customer that sent it.
func (s *Service) CustomerDataExport(ctx context.Context, scope CustomerScope, id uuid.UUID) (DataExport, error) {
	req, err := s.requests.GetForCustomer(ctx, scope, id)
	if err != nil {
		return DataExport{}, err
	}
	return s.dataExport(ctx, req)
}

// HostedDataExport is an approved "see my data" request's data for the
// person, through the hosted link they proved themselves on.
func (s *Service) HostedDataExport(ctx context.Context, token string) (DataExport, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return DataExport{}, err
	}
	if openExport(req, s.now()) == nil {
		return DataExport{}, ErrExportUnavailable
	}
	return s.dataExport(audit.ContextWithActor(ctx, hostedSubjectActor), req)
}
