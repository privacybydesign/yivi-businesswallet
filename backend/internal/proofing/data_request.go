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
// person asked the org for their data, and the org sends them a
// session on that flow like any other. Once the person is proven, the wallet
// does not approve it: it finds the org's external sessions of that person
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
	// it holds the name and e-mail the sender typed, and no proofed identity
	// to match on. Listed after the others and only taken when the reviewer
	// ticks it.
	MatchEmail MatchLevel = "email"
	// MatchName is an unfinished session whose typed name is the requester's
	// full name, word for word (diploma.SameName), sent to another or no
	// address. Taken only when the reviewer ticks it, like MatchEmail.
	MatchName MatchLevel = "name"
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
	// ErrorLegalRetention is the error code of an erasure refused because its
	// org is bound by law to keep the data: its legal retention holds every
	// identity check found.
	ErrorLegalRetention = "LEGAL_RETENTION"
	// automaticReviewer is who decided a data request the org's ErasureHandling
	// or legal retention decided without a reviewer.
	automaticReviewer = "automatic"
	// The reasons an automatic decision records, as a reviewer's would.
	erasedAutomaticallyReason = "Erased automatically: every session found was proven to be this person's."
	keptByLawReason           = "Kept automatically: the organisation is bound by law to keep this data."
)

var (
	// ErrFlowNoIdentity is a flow that does not read the name and date of birth
	// where they are needed: a data request finds the person's sessions by
	// them, and a required diploma's holder is matched against them.
	ErrFlowNoIdentity = errors.New("proofing: the flow does not read the name and date of birth")
	// ErrDataFlowForMember is a data request flow offered to, or sent to, a
	// member: only external people ask for their data.
	ErrDataFlowForMember = errors.New("proofing: a data request flow is for external people")
	// ErrLegalRetention is an erasure that takes a session still within the
	// org's legal retention.
	ErrLegalRetention = errors.New("proofing: the session is within the legal retention")
	// ErrExportUnavailable is an export of a request that is not an approved
	// "see my data" request, or whose download window passed.
	ErrExportUnavailable = errors.New("proofing: no data export is available for this session")
)

// DataMatch is one of the org's external sessions a data request matched.
type DataMatch struct {
	RequestID uuid.UUID
	FlowName  string
	// FlowKind is the matched session's: one of the person's earlier data
	// requests is a match too (Candidates).
	FlowKind       FlowKind
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
	// HeldUntil is when the org's legal retention lets the session be erased;
	// nil when none applies.
	HeldUntil *time.Time
}

// held reports a session an erasure may not take yet.
func (m DataMatch) held(now time.Time) bool {
	return m.HeldUntil != nil && now.Before(*m.HeldUntil)
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
	Candidates(ctx context.Context, req Request, requester SubjectIdentity) ([]Request, error)
	RecordIdentityKey(ctx context.Context, req Request, id SubjectIdentity) error
	UnfinishedCandidates(ctx context.Context, req Request) ([]Request, error)
	TryLockMatching(ctx context.Context, id uuid.UUID) (release func(), ok bool, err error)
	MatchChecks(ctx context.Context, req Request) (map[uuid.UUID]MatchLevel, error)
	RecordMatchCheck(ctx context.Context, req Request, candidateID uuid.UUID, level MatchLevel) error
	SaveMatches(ctx context.Context, req Request, matches []NewDataMatch) error
	Matches(ctx context.Context, req Request) ([]DataMatch, error)
	MatchedRequests(ctx context.Context, req Request) ([]Request, error)
	RecordDecision(ctx context.Context, req Request, approved []uuid.UUID, exportUntil *time.Time, kept *int, held *DataHold) error
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
// only external people ask for their data. Requests already sent keep
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
		if flows[i].Default {
			return "", fmt.Errorf("%w: the default flow must be an identity check", ErrInvalidInput)
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

// findDataMatches reads who a data request proofed and stores the org's
// sessions of that person, for the review. Only the sessions with the
// person's family name and date of birth are read from the engine
// (Candidates), and a candidate read once is not read again (candidateMatch).
// A proof without a name or date of birth matches nothing.
func (s *Service) findDataMatches(ctx context.Context, tenant proofingprovider.Tenant, req Request) error {
	if s.dataRequests == nil || req.session == nil {
		return nil
	}
	proof, err := s.ips.SessionIdentity(ctx, tenant, req.session.ID, req.session.Token)
	if err != nil {
		return fmt.Errorf("proofing: data request %s identity: %w", req.ID, err)
	}
	requester := subjectIdentityOf(proof)
	if err := s.dataRequests.RecordIdentityKey(ctx, req, requester); err != nil {
		return err
	}
	matches := []NewDataMatch{}
	if requester.FamilyName != "" && requester.BirthDate != "" {
		candidates, err := s.dataRequests.Candidates(ctx, req, requester)
		if err != nil {
			return err
		}
		checked, err := s.dataRequests.MatchChecks(ctx, req)
		if err != nil {
			return err
		}
		for _, c := range candidates {
			if c.session == nil {
				continue
			}
			level, ok := checked[c.ID]
			if !ok {
				if level, err = s.candidateMatch(ctx, req, requester, c); err != nil {
					return err
				}
			}
			if level != "" {
				matches = append(matches, NewDataMatch{RequestID: c.ID, Level: level})
			}
		}
	}
	unfinished, err := s.unfinishedMatches(ctx, req, requester, matches)
	if err != nil {
		return err
	}
	return s.dataRequests.SaveMatches(ctx, req, append(matches, unfinished...))
}

// candidateMatch reads candidate c's identity from the engine and keeps how it
// matches req's requester ("" for not at all, or no longer held), so a retried
// search reads c no more.
func (s *Service) candidateMatch(ctx context.Context, req Request, requester SubjectIdentity, c Request) (MatchLevel, error) {
	var level MatchLevel
	held, err := s.ips.SessionIdentity(ctx, requestTenant(c), c.session.ID, c.session.Token)
	switch {
	case errors.Is(err, proofingprovider.ErrNotFound):
	case err != nil:
		return "", fmt.Errorf("proofing: data request %s match identity request %s: %w", req.ID, c.ID, err)
	default:
		level, _ = matchLevel(requester, held)
	}
	if err := s.dataRequests.RecordMatchCheck(ctx, req, c.ID, level); err != nil {
		return "", err
	}
	return level, nil
}

// recordIdentityKey keeps the identity match key of who an external person's session
// proofed once it is decided or goes to review, so a later data request reads
// only that person's sessions from the engine. A session already in review
// has its key, a proven data request's is kept by its search
// (findDataMatches), and one rejected as someone other than the person it was
// sent to is never a candidate.
func (s *Service) recordIdentityKey(ctx context.Context, tenant proofingprovider.Tenant, req Request, sess *ipsSession,
	next Status, res proofingprovider.Result,
) error {
	if s.dataRequests == nil || !req.External() || req.Status == StatusNeedsReview ||
		res.ErrorCode == errorIdentityMismatch || (next != StatusApproved && next != StatusRejected && next != StatusNeedsReview) ||
		(req.FlowKind.dataRequest() && next != StatusRejected) {
		return nil
	}
	held, err := s.ips.SessionIdentity(ctx, tenant, sess.ID, sess.Token)
	switch {
	case errors.Is(err, proofingprovider.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("proofing: identity match key request %s: %w", req.ID, err)
	}
	return s.dataRequests.RecordIdentityKey(ctx, req, subjectIdentityOf(held))
}

// unfinishedMatches are req's matches among the org's unfinished
// sessions, apart from those already matched on identity: MatchEmail when sent
// to the address req was sent to, case aside, else MatchName when its typed
// name is the requester's.
func (s *Service) unfinishedMatches(ctx context.Context, req Request, requester SubjectIdentity, matched []NewDataMatch) ([]NewDataMatch, error) {
	candidates, err := s.dataRequests.UnfinishedCandidates(ctx, req)
	if err != nil {
		return nil, err
	}
	email := strings.TrimSpace(req.SubjectEmail)
	out := []NewDataMatch{}
	for _, c := range candidates {
		if slices.ContainsFunc(matched, func(m NewDataMatch) bool { return m.RequestID == c.ID }) {
			continue
		}
		switch {
		case email != "" && strings.EqualFold(strings.TrimSpace(c.SubjectEmail), email):
			out = append(out, NewDataMatch{RequestID: c.ID, Level: MatchEmail})
		case requester.FamilyName != "" && diploma.SameName(requester.GivenName+" "+requester.FamilyName, c.SubjectName):
			out = append(out, NewDataMatch{RequestID: c.ID, Level: MatchName})
		}
	}
	return out, nil
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

// DataMatches are the org's sessions a data request matched, for its
// review; none for any other request.
func (s *Service) DataMatches(ctx context.Context, orgID, id uuid.UUID) (Request, []DataMatch, error) {
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, nil, err
	}
	if !req.External() || !req.FlowKind.dataRequest() || s.dataRequests == nil {
		return req, []DataMatch{}, nil
	}
	matches, err := s.dataRequests.Matches(ctx, req)
	return req, matches, err
}

// decideDataRequest is DecideReview for a data request, which the wallet
// holds in review itself (the engine approved the person). Approving takes the
// approved matches (approvedMatches): an erasure purges each of those
// sessions, then the request's own personal data (it held who asked
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
		if approved, err = approvedMatches(matches, in.RequestIDs, req.FlowKind, s.now()); err != nil {
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
	var kept *int
	var hold *DataHold
	if in.Approve && req.FlowKind == FlowDataErasure {
		n := keptMatches(matches, approved)
		kept = &n
	}
	// What the law still keeps of the person, for their page: on an approval
	// it is among the kept, on a refusal it is why.
	if req.FlowKind == FlowDataErasure {
		hold = heldMatches(matches, s.now())
	}

	decided, code := StatusApproved, ""
	if !in.Approve {
		decided, code = StatusRejected, in.ErrorCode
		if code == "" {
			code = ErrorReviewRejected
		}
	}

	if err := s.dataRequests.RecordDecision(ctx, req, approved, exportUntil, kept, hold); err != nil {
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

// handleErasure decides a proven "delete my data" request as the org's
// ErasureHandling says, once it has reached review: ErasureAutomatic erases its
// sure matches at once and decides it when no unsure one is left, or leaves
// those to a reviewer; ErasureReview leaves all of it to a reviewer. It holds
// the review lock, as a reviewer's decision does, so the two never decide at
// once. A session the org's legal retention still holds is left alone, as no
// erasure takes one. A request that fails here waits in review: a reviewer
// decides it as any other.
func (s *Service) handleErasure(ctx context.Context, req Request) (Request, error) {
	if req.FlowKind != FlowDataErasure || !req.External() || s.dataRequests == nil {
		return req, nil
	}
	settings, err := s.orgSettings.Get(ctx, req.OrganizationID)
	if err != nil {
		return req, err
	}
	if settings.Session.ErasureHandling != ErasureAutomatic {
		return req, nil
	}
	release, err := s.requests.LockReview(ctx, req.ID)
	if err != nil {
		return req, err
	}
	defer release()
	fresh, err := s.requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil {
		return req, err
	}
	if fresh.Status != StatusNeedsReview || fresh.session == nil || fresh.session.EndedAt != nil || fresh.PurgedAt != nil {
		return fresh, nil
	}
	matches, err := s.dataRequests.Matches(ctx, fresh)
	if err != nil {
		return fresh, err
	}
	now := s.now()
	sure, unsure := []uuid.UUID{}, false
	for _, m := range matches {
		switch {
		case m.held(now):
			// The law keeps it: no one erases it yet.
		case sureMatch(m.Level):
			sure = append(sure, m.RequestID)
		case m.PurgedAt == nil:
			unsure = true
		}
	}
	if !unsure {
		return s.decideDataRequest(ctx, fresh, automaticReviewer,
			ReviewInput{Approve: true, RequestIDs: sure, Reason: erasedAutomaticallyReason})
	}
	// The sure ones are the person's and go now; the rest wait for a reviewer.
	if err := s.purgeMatches(ctx, fresh, sure); err != nil {
		return fresh, err
	}
	return s.requests.Get(ctx, fresh.OrganizationID, fresh.ID)
}

// declineHeldErasure declines a proven "delete my data" request at once when
// the org's legal retention holds every identity check still found: nothing it
// asked for could be erased, so nobody need decide. The person's earlier data
// requests among the matches do not count: each holds only who asked, and goes
// with its own purge, so it is no reason to send the request to a reviewer. It
// holds the review lock, as a reviewer's decision does, so the two never decide
// at once. A request that fails here waits in review: a reviewer decides it as
// any other, and approvedMatches takes no session the legal retention holds.
func (s *Service) declineHeldErasure(ctx context.Context, req Request) (Request, error) {
	if req.FlowKind != FlowDataErasure || req.Status != StatusNeedsReview || s.dataRequests == nil {
		return req, nil
	}
	settings, err := s.orgSettings.Get(ctx, req.OrganizationID)
	if err != nil || !settings.LegalRetention.set() {
		return req, err
	}
	release, err := s.requests.LockReview(ctx, req.ID)
	if err != nil {
		return req, err
	}
	defer release()
	fresh, err := s.requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil {
		return req, err
	}
	if fresh.Status != StatusNeedsReview || fresh.session == nil || fresh.session.EndedAt != nil || fresh.PurgedAt != nil {
		return fresh, nil
	}
	matches, err := s.dataRequests.Matches(ctx, fresh)
	if err != nil {
		return fresh, err
	}
	now, found := s.now(), 0
	for _, m := range matches {
		if m.PurgedAt != nil || m.FlowKind.dataRequest() {
			continue
		}
		if !m.held(now) {
			return fresh, nil
		}
		found++
	}
	if found == 0 {
		return fresh, nil
	}
	return s.decideDataRequest(ctx, fresh, automaticReviewer,
		ReviewInput{ErrorCode: ErrorLegalRetention, Reason: keptByLawReason})
}

// withLegalNotice sets the LegalNotice of an erasure that left sessions the
// legal retention holds (DataHold): one it declined, or an approved one, whose
// person is told what was kept by law, why, and until when (GDPR Art. 12(4)).
// It is left as it is once the hold passed (the pruner erased what it held),
// without a legal retention, and for an erasure a reviewer refused otherwise.
func (s *Service) withLegalNotice(ctx context.Context, req Request) (Request, error) {
	hold := req.DataHold
	if req.FlowKind != FlowDataErasure || hold == nil || !s.now().Before(hold.Until) ||
		(req.Status != StatusApproved && req.ErrorCode != ErrorLegalRetention) {
		return req, nil
	}
	settings, err := s.orgSettings.Get(ctx, req.OrganizationID)
	if err != nil || !settings.LegalRetention.set() {
		return req, err
	}
	req.LegalNotice = &LegalNotice{
		Sessions: hold.Sessions, Until: hold.Until,
		ReasonEN: settings.LegalRetention.ReasonEN, ReasonNL: settings.LegalRetention.ReasonNL,
	}
	return req, nil
}

// approvedMatches checks a reviewer's approved sessions against the matches.
// nil approves the ones taken without a tick: for an erasure the sure ones
// (sureMatch: read off a verified chip as the requester), for "see my data"
// the ones proven with the same document. Any other match is taken only by a
// reviewer's own tick. An erasure takes no session still within the legal
// retention: nil leaves it out, and a tick on one is ErrLegalRetention. A match
// an automatic erasure purged already is approved whatever the list says: it is
// erased.
func approvedMatches(matches []DataMatch, approved []uuid.UUID, kind FlowKind, now time.Time) ([]uuid.UUID, error) {
	erasable := func(m DataMatch) bool { return kind != FlowDataErasure || !m.held(now) }
	out := []uuid.UUID{}
	if approved == nil {
		out = untickedMatches(matches, kind, erasable)
	}
	for _, id := range approved {
		i := slices.IndexFunc(matches, func(m DataMatch) bool { return m.RequestID == id })
		if i < 0 {
			return nil, fmt.Errorf("%w: session %s is not one of this request's matches", ErrInvalidInput, id)
		}
		if !erasable(matches[i]) {
			return nil, ErrLegalRetention
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if kind == FlowDataErasure {
		out = withPurgedMatches(out, matches)
	}
	return out, nil
}

// untickedMatches are the matches a decision without a list approves: the
// ones proven with the same document, and for an erasure the sure ones too,
// each only when erasable.
func untickedMatches(matches []DataMatch, kind FlowKind, erasable func(DataMatch) bool) []uuid.UUID {
	out := []uuid.UUID{}
	for _, m := range matches {
		if (m.Level == MatchStrong || (kind == FlowDataErasure && sureMatch(m.Level))) && erasable(m) {
			out = append(out, m.RequestID)
		}
	}
	return out
}

// withPurgedMatches adds to approved each match purged already, which an
// erasure has erased whatever the list says.
func withPurgedMatches(approved []uuid.UUID, matches []DataMatch) []uuid.UUID {
	for _, m := range matches {
		if m.PurgedAt != nil && !slices.Contains(approved, m.RequestID) {
			approved = append(approved, m.RequestID)
		}
	}
	return approved
}

// sureMatch reports a match level that is the requester's for certain: the
// same name and date of birth read off a verified chip, on the same document
// or another. An e-mail address or a typed name is not.
func sureMatch(level MatchLevel) bool { return level == MatchStrong || level == MatchProbable }

// keptMatches counts the matches an erasure approving approved leaves: not
// approved, and not purged already.
func keptMatches(matches []DataMatch, approved []uuid.UUID) int {
	kept := 0
	for _, m := range matches {
		if m.PurgedAt == nil && !slices.Contains(approved, m.RequestID) {
			kept++
		}
	}
	return kept
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

// DataExportSession is one session in a DataExport. Only a MatchStrong one,
// proven with the requester's own document, carries personal data (Personal:
// its address, Identity and Diplomas): a probable, e-mail or name match the
// reviewer ticked may still be another person's, so it exports its facts
// alone.
type DataExportSession struct {
	Request  Request
	Level    MatchLevel
	Personal bool
	// Addressed is a Personal session whose contact (the name and address the
	// sender typed) is the requester's too: approved for the one person it
	// was sent to (addressedTo).
	Addressed bool
	Identity  *proofingprovider.Identity
	Diplomas  []Diploma
}

// addressedTo reports whether whoever proofed req is the person it was sent
// to: approved on a request for one known person (ExpectsSubject). Any other
// session proves who took it, not who it was addressed to.
func addressedTo(req Request) bool {
	return req.ExpectsSubject && req.Status == StatusApproved
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
	approved, strong := []uuid.UUID{}, []uuid.UUID{}
	levels := map[uuid.UUID]MatchLevel{}
	for _, m := range matches {
		if m.Approved != nil && *m.Approved {
			approved = append(approved, m.RequestID)
			levels[m.RequestID] = m.Level
			if m.Level == MatchStrong {
				strong = append(strong, m.RequestID)
			}
		}
	}
	reqs, err := s.dataRequests.MatchedRequests(ctx, req)
	if err != nil {
		return DataExport{}, err
	}
	diplomas, err := s.RequestDiplomas(ctx, strong)
	if err != nil {
		return DataExport{}, err
	}
	out := DataExport{RequestID: req.ID, ExportedAt: s.now(), Sessions: []DataExportSession{}}
	for _, m := range reqs {
		if !slices.Contains(approved, m.ID) {
			continue
		}
		session := DataExportSession{Request: m, Level: levels[m.ID], Personal: levels[m.ID] == MatchStrong}
		session.Addressed = session.Personal && addressedTo(m)
		if session.Personal {
			session.Diplomas = diplomas[m.ID]
		}
		if session.Personal && m.PurgedAt == nil && m.session != nil && m.Status.Settled() {
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

// ExternalDataExport is an approved "see my data" request's data for the
// API caller that sent it.
func (s *Service) ExternalDataExport(ctx context.Context, orgID, id uuid.UUID) (DataExport, error) {
	req, err := s.requests.GetExternal(ctx, orgID, id)
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
