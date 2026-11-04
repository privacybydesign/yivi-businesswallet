package idscan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// statusServer answers every transaction status read with body.
func statusServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkToken(t, r)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// faceStatus is a transaction in state whose document scan passed, with the
// face check task at attempts in taskState.
func faceStatus(state, task, taskState string, attempts int) string {
	liveness, match := `{"type":"face_liveness","state":"completed","attempts":1}`, `{"type":"face_match","state":"pending","attempts":0}`
	current := `{"type":"` + task + `","state":"` + taskState + `","attempts":` + strconv.Itoa(attempts) + `}`
	if task == TaskFaceLiveness {
		liveness = current
	} else {
		match = current
	}
	return `{"state":"` + state + `","tasks":[{"type":"document_scan","state":"completed","attempts":2},` + liveness + `,` + match + `]}`
}

func TestTransactionStatus(t *testing.T) {
	spent := &Failure{Task: TaskFaceLiveness, State: StateMaximumAttemptsExceeded, Attempts: faceAttemptsLimit}
	tests := map[string]struct {
		body string
		want Status
	}{
		"face check still trying": {
			faceStatus(StatePending, TaskFaceLiveness, StatePending, faceAttemptsLimit-1), Status{State: StatePending},
		},
		"liveness out of attempts while pending": {
			faceStatus(StatePending, TaskFaceLiveness, StatePending, faceAttemptsLimit), Status{State: StateFailed, Failure: spent},
		},
		"match out of attempts while pending": {
			faceStatus(StatePending, TaskFaceMatch, StatePending, faceAttemptsLimit),
			Status{State: StateFailed, Failure: &Failure{Task: TaskFaceMatch, State: StateMaximumAttemptsExceeded, Attempts: faceAttemptsLimit}},
		},
		"document out of attempts": {
			`{"state":"failed","tasks":[{"type":"document_scan","state":"maximum_attempts_exceeded","attempts":10,"documentScans":[{"code":"unknown_type_front"}]},{"type":"face_liveness","state":"pending","attempts":0}]}`,
			Status{State: StateFailed, Failure: &Failure{Task: TaskDocumentScan, State: StateMaximumAttemptsExceeded, Attempts: 10, Code: "unknown_type_front"}},
		},
		"completed": {
			faceStatus(StateCompleted, TaskFaceMatch, StateCompleted, 1), Status{State: StateCompleted},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := statusServer(t, tc.body)
			got, err := New(testKeyID, testSecret, srv.URL, srv.Client()).TransactionStatus(context.Background(), "trx-1")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.want.State || (got.Failure == nil) != (tc.want.Failure == nil) ||
				(got.Failure != nil && *got.Failure != *tc.want.Failure) {
				t.Errorf("status = %+v (failure %+v), want %+v (failure %+v)", got, got.Failure, tc.want, tc.want.Failure)
			}
		})
	}
}
