package idscan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// transactionStatus reads a transaction's status from a server answering body.
func transactionStatus(t *testing.T, body string) Status {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkToken(t, r)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	got, err := New(testKeyID, testSecret, srv.URL, srv.Client()).TransactionStatus(context.Background(), "trx-1")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The task a pending transaction is at, as ID Scan answered on 2026-11-04:
// it leaves a pending face_match out once the liveness check passed.
func TestTransactionStatusRunning(t *testing.T) {
	tests := map[string]struct {
		body string
		want *Running
	}{
		"at the photo": {
			`{"state":"pending","tasks":[{"type":"document_scan","state":"pending","attempts":3},{"type":"face_liveness","state":"pending","attempts":0}]}`,
			&Running{Task: TaskDocumentScan, Attempts: 3},
		},
		"at the liveness check": {
			`{"state":"pending","tasks":[{"type":"document_scan","state":"completed","attempts":2},{"type":"face_liveness","state":"pending","attempts":4}]}`,
			&Running{Task: TaskFaceLiveness, Attempts: 4},
		},
		"at the face match": {
			`{"state":"pending","tasks":[{"type":"document_scan","state":"completed","attempts":2},{"type":"face_liveness","state":"completed","attempts":1}]}`,
			&Running{Task: TaskFaceMatch},
		},
		"completed": {
			`{"state":"completed","tasks":[{"type":"document_scan","state":"completed","attempts":2}]}`,
			nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := transactionStatus(t, tc.body)
			if !reflect.DeepEqual(got.Running, tc.want) {
				t.Errorf("running = %+v, want %+v", got.Running, tc.want)
			}
		})
	}
}

// A liveness check that used the tries the photo left fails the
// transaction, as ID Scan's page then shows an error while its API leaves it
// pending. A good front of a two-sided document is no failed photo. The
// photos are those of tests of 2026-11-04 and 2026-11-05.
func TestTransactionStatusSpentLiveness(t *testing.T) {
	const (
		back         = `"front_side_provided_back_side_required"`
		good         = `null`
		unknownBack  = `"unknown_type_back"`
		unknownFront = `"unknown_type_front"`
		quality      = `"image_quality_requirements_not_met"`
	)
	status := func(liveness int, photos ...string) string {
		scans := make([]string, len(photos))
		for i, code := range photos {
			scans[i] = `{"code":` + code + `}`
		}
		return `{"state":"pending","tasks":[` +
			`{"type":"document_scan","state":"completed","attempts":` + strconv.Itoa(len(photos)) +
			`,"documentScans":[` + strings.Join(scans, ",") + `]},` +
			`{"type":"face_liveness","state":"pending","attempts":` + strconv.Itoa(liveness) + `}]}`
	}
	tests := map[string]struct {
		body  string
		spent bool
	}{
		"both sides at first try, liveness at 9":  {status(9, back, good), false},
		"both sides at first try, liveness at 10": {status(10, back, good), true},
		"2 failed backs, liveness at 7":           {status(7, back, unknownBack, back, unknownBack, good), false},
		"2 failed backs, liveness at 8":           {status(8, back, unknownBack, back, unknownBack, good), true},
		"3 failed photos, liveness at 7":          {status(7, unknownBack, quality, back, unknownBack, back, good), true},
		"2 failed fronts, liveness at 4":          {status(4, unknownFront, good, unknownFront, back), false},
		"one-sided at first try, liveness at 10":  {status(10, good), true},
		"photo still running": {
			`{"state":"pending","tasks":[{"type":"document_scan","state":"pending","attempts":9},{"type":"face_liveness","state":"pending","attempts":0}]}`, false,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := transactionStatus(t, tc.body)
			spent := got.State == StateFailed && got.Failure != nil &&
				got.Failure.Task == TaskFaceLiveness && got.Failure.State == StateMaximumAttemptsExceeded
			if spent != tc.spent {
				t.Errorf("status = %+v (failure %+v), want spent %v", got, got.Failure, tc.spent)
			}
		})
	}
}

// A failed transaction names the task that ended without passing, and for the
// document scan its last attempt's error code.
func TestTransactionStatusFailed(t *testing.T) {
	got := transactionStatus(t, `{"state":"failed","tasks":[{"type":"document_scan","state":"maximum_attempts_exceeded","attempts":10,"documentScans":[{"code":"unknown_type_front"}]},{"type":"face_liveness","state":"pending","attempts":0}]}`)
	want := Failure{Task: TaskDocumentScan, State: StateMaximumAttemptsExceeded, Attempts: 10, Code: "unknown_type_front"}
	if got.State != StateFailed || got.Failure == nil || *got.Failure != want {
		t.Errorf("status = %+v (failure %+v), want failed at %+v", got, got.Failure, want)
	}
}
