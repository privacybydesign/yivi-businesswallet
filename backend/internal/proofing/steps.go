package proofing

import "time"

// StepState is where an extra step of a session stands. It is the step's
// own: it never changes the identity's outcome or the session's.
type StepState string

const (
	// StepNotStarted is a step whose window has not opened: the identity
	// check did not finish, or was not approved or sent to review.
	StepNotStarted StepState = "not_started"
	// StepOpen takes the subject's input until its window closes.
	StepOpen StepState = "open"
	StepDone StepState = "done"
	// StepInReview waits for a reviewer on its review item.
	StepInReview StepState = "in_review"
	// StepRejected is a step whose review item was rejected or lapsed.
	StepRejected StepState = "rejected"
	// StepNotProvided is a step whose window closed without the subject's input.
	StepNotProvided StepState = "not_provided"
)

// StepKind is which extra step a StepOutcome is.
type StepKind string

const (
	StepAddress    StepKind = "address"
	StepDiplomas   StepKind = "diplomas"
	StepEmployment StepKind = "employment"
)

// StepOutcome is one extra step of a session: its state, until when it takes
// input while open, and the code of a step that did not pass. Reviewed marks
// a done step a reviewer accepted.
type StepOutcome struct {
	Kind     StepKind   `json:"kind"`
	State    StepState  `json:"state"`
	Until    *time.Time `json:"until,omitempty"`
	Code     string     `json:"code,omitempty"`
	Reviewed bool       `json:"reviewed,omitempty"`
}

// extraStep is how a step reads its outcome from what the request already
// holds of it, and its review item: nothing of it is stored twice.
type extraStep struct {
	kind StepKind
	// review is the step's review item kind; empty for a step without one.
	review ReviewKind
	window time.Duration
	// asked is whether the request's flow has the step.
	asked func(Request) bool
	// given is whether the subject gave the step's input.
	given func(Request) bool
	// notProvided and rejected are the step's codes.
	notProvided, rejected string
}

// The codes of a step the subject did not provide, or a reviewer rejected.
const (
	ErrorAddressNotProvided = "ADDRESS_NOT_PROVIDED"
	ErrorAddressRejected    = "ADDRESS_REJECTED"
	ErrorDiplomaNotProvided = "DIPLOMA_NOT_PROVIDED"
	ErrorDiplomaRejected    = "DIPLOMA_REJECTED"
	ErrorEmploymentRejected = "EMPLOYMENT_REJECTED"
)

// extraSteps are the steps whose outcome is their own, in the order the page
// asks for them.
var extraSteps = []extraStep{
	{
		kind: StepAddress, review: ReviewKindAddress, window: AddressWindow,
		asked:       func(r Request) bool { return r.Address.asked() },
		given:       func(r Request) bool { return r.AddressAddedAt != nil },
		notProvided: ErrorAddressNotProvided, rejected: ErrorAddressRejected,
	},
	{
		kind: StepDiplomas, review: ReviewKindDiplomas, window: DiplomaUploadWindow,
		asked:       func(r Request) bool { return r.Diplomas.asked() },
		given:       func(r Request) bool { return r.DiplomasAddedAt != nil },
		notProvided: ErrorDiplomaNotProvided, rejected: ErrorDiplomaRejected,
	},
	{
		kind: StepEmployment, review: ReviewKindEmployment, window: EmploymentWindow,
		asked:       func(r Request) bool { return r.Employment.asked() },
		given:       func(r Request) bool { return r.EmploymentAddedAt != nil },
		notProvided: ErrorEmploymentNotProvided, rejected: ErrorEmploymentRejected,
	},
}

// Steps are the outcomes at now of the extra steps r's flow asks for; none
// once purged, which erased what they read.
func (r Request) Steps(now time.Time) []StepOutcome {
	out := []StepOutcome{}
	if r.PurgedAt != nil {
		return out
	}
	for _, st := range extraSteps {
		if st.asked(r) {
			out = append(out, st.outcome(r, now))
		}
	}
	return out
}

func (st extraStep) outcome(r Request, now time.Time) StepOutcome {
	out := StepOutcome{Kind: st.kind}
	review := r.stepReview(st.review)
	switch {
	case r.CheckedAt == nil:
		out.State = StepNotStarted
	case review == ReviewPending:
		out.State = StepInReview
	case review == ReviewRejected:
		out.State, out.Code = StepRejected, st.rejected
	case review == ReviewLapsed:
		out.State, out.Code = StepRejected, errorReviewLapsed
	case st.given(r):
		out.State, out.Reviewed = StepDone, review == ReviewConfirmed
	case r.stepsOpen(now, st.window):
		out.State, out.Until = StepOpen, r.stepsUntil(st.window)
	default:
		out.State, out.Code = StepNotProvided, st.notProvided
	}
	return out
}
