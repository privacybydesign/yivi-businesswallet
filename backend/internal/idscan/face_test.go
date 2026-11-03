package idscan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestCreateTransactionTasks(t *testing.T) {
	for name, tc := range map[string]struct {
		face  bool
		tasks []any
	}{
		"document only":   {false, []any{taskDocumentScan}},
		"checks the face": {true, []any{taskDocumentScan, taskFaceLiveness, taskFaceMatch}},
	} {
		t.Run(name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checkToken(t, r)
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode: %v", err)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"trx-1","state":"pending","url":"https://scan.example/s/1"}`))
			}))
			t.Cleanup(srv.Close)

			if _, err := New(testKeyID, testSecret, srv.URL, srv.Client()).CreateTransaction(context.Background(), LocaleDutch, ScanOptions{Face: tc.face}); err != nil {
				t.Fatal(err)
			}
			if tasks, _ := got["tasks"].([]any); !slices.Equal(tasks, tc.tasks) {
				t.Errorf("tasks = %v, want %v", got["tasks"], tc.tasks)
			}
		})
	}
}

// faceScan is a document scan's tasks with the face checks after it, the
// liveness check in livenessState and the match in matchState.
func faceScan(state, livenessState, matchState string) string {
	return `{"state":"` + state + `","tasks":[
		{"type":"document_scan","state":"completed","score":0.97,"files":[
			{"category":"document","type":"PASSPORT","name":"Netherlands - Passport (2021)","url":"{base}/downloads/front"},
			{"category":"image","type":"portrait","url":"{base}/downloads/portrait"}]},
		{"type":"face_liveness","state":"` + livenessState + `","files":[
			{"category":"video","url":"{base}/downloads/video"},
			{"category":"image","url":"{base}/downloads/selfie"}]},
		{"type":"face_match","state":"` + matchState + `","score":0.877}]}`
}

func TestResultFace(t *testing.T) {
	srv := resultServer(t, faceScan(stateCompleted, stateCompleted, stateCompleted), pngBytes(t))
	res, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed || res.Face == nil {
		t.Fatalf("result = completed %v, face %+v; want completed with a face", res.Completed, res.Face)
	}
	if res.Face.MatchScore == nil || *res.Face.MatchScore != 0.877 {
		t.Errorf("face match score = %v, want 0.877", res.Face.MatchScore)
	}
	if res.Face.Selfie == nil || !bytes.HasSuffix(res.Face.Selfie.Data, []byte("selfie")) {
		t.Error("selfie is not the liveness check's still")
	}
	if !bytes.HasSuffix(res.Front.Data, []byte("front")) || res.Back != nil {
		t.Error("the document's portrait counted as a side")
	}
}

// A scan whose face checks did not all pass, failed or still running, has no
// result yet.
func TestResultFaceNotCompleted(t *testing.T) {
	// The states ID Scan ends a task in that did not pass.
	const (
		stateAborted                 = "aborted"
		stateMaximumAttemptsExceeded = "maximum_attempts_exceeded"
	)
	for name, body := range map[string]string{
		"out of attempts":    faceScan("failed", stateMaximumAttemptsExceeded, "pending"),
		"stopped":            faceScan("failed", stateAborted, "pending"),
		"liveness running":   faceScan("pending", "pending", "pending"),
		"match not done":     faceScan("pending", stateCompleted, "pending"),
		"match out of tries": faceScan("failed", stateCompleted, stateMaximumAttemptsExceeded),
	} {
		t.Run(name, func(t *testing.T) {
			srv := resultServer(t, body, pngBytes(t))
			res, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1")
			if err != nil {
				t.Fatal(err)
			}
			if res.Completed || res.Face != nil {
				t.Fatalf("result = completed %v, face %+v; want not completed, no face", res.Completed, res.Face)
			}
		})
	}
}

func TestResultFaceWithoutSelfie(t *testing.T) {
	const noStill = `{"state":"completed","tasks":[
		{"type":"document_scan","state":"completed","files":[{"category":"document","type":"PASSPORT","url":"{base}/downloads/front"}]},
		{"type":"face_liveness","state":"completed","files":[{"category":"video","url":"{base}/downloads/video"}]},
		{"type":"face_match","state":"completed","score":0.95}]}`
	srv := resultServer(t, noStill, pngBytes(t))
	_, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1")
	if !errors.Is(err, ErrNoSelfie) {
		t.Fatalf("err = %v, want ErrNoSelfie", err)
	}
}
