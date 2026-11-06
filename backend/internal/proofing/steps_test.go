package proofing

import (
	"testing"
	"time"
)

// Each step the flow asks for reads its own outcome from what the request
// holds of it, in the order the page asks for them.
func TestStepsReadEachStep(t *testing.T) {
	now := time.Now()
	checked := now.Add(-2 * time.Hour)
	added := checked.Add(time.Minute)
	req := Request{
		CheckedAt: &checked, Address: AddressRequired, Diplomas: DiplomasRequired, Employment: EmploymentRequired,
		AddressAddedAt: &added, AddressReview: ReviewConfirmed,
		EmploymentAddedAt: &added, EmploymentReview: ReviewPending,
	}
	want := []StepOutcome{
		{Kind: StepAddress, State: StepDone, Reviewed: true},
		{Kind: StepDiplomas, State: StepNotProvided, Code: ErrorDiplomaNotProvided},
		{Kind: StepEmployment, State: StepInReview},
	}
	got := req.Steps(now)
	if len(got) != len(want) {
		t.Fatalf("Steps = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Steps[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}

	req.DiplomasAddedAt = &added
	if got := req.Steps(now); got[1].State != StepDone {
		t.Errorf("diplomas with one kept = %s; want done", got[1].State)
	}
	req.PurgedAt = &now
	if got := req.Steps(now); len(got) != 0 {
		t.Errorf("Steps once purged = %+v; want none", got)
	}
}
