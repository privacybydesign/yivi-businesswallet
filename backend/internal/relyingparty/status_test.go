package relyingparty

import (
	"context"
	"errors"
	"testing"

	"github.com/privacybydesign/irmago/eudi/credentials/statuslist"
)

type fixedStatus struct {
	status statuslist.Status
	err    error
}

func (f fixedStatus) Check(context.Context, statuslist.Reference) (statuslist.Status, error) {
	return f.status, f.err
}

// A presented credential with a status list passes only when its entry reads
// valid; one revoked, suspended or whose list cannot be read is refused (CS-10
// RP-RV-03). Without a status list there is nothing to check.
func TestCheckStatus(t *testing.T) {
	t.Parallel()
	listed := &statuslist.StatusClaim{StatusList: &statuslist.Reference{Index: 7, URI: "https://issuer.test/lists/1"}}
	cases := []struct {
		name    string
		checker StatusChecker
		claim   *statuslist.StatusClaim
		refused bool
	}{
		{"valid", fixedStatus{status: statuslist.StatusValid}, listed, false},
		{"revoked", fixedStatus{status: statuslist.StatusInvalid}, listed, true},
		{"suspended", fixedStatus{status: statuslist.StatusSuspended}, listed, true},
		{"list unreadable", fixedStatus{err: statuslist.ErrFetch}, listed, true},
		{"no status list", fixedStatus{err: errors.New("must not be asked")}, nil, false},
		{"no checker", nil, listed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := checkStatus(context.Background(), c.checker, c.claim)
			if (err != nil) != c.refused {
				t.Fatalf("checkStatus = %v, refused %v", err, c.refused)
			}
		})
	}
}
