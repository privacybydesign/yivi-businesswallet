package regula_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
)

func TestGetLiveness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/liveness" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Query().Get("transactionId") {
		case "tx 1":
			_, _ = w.Write([]byte(`{"code":0,"status":0,"tag":"ips-s1","transactionId":"tx 1"}`))
		case "unlive":
			_, _ = w.Write([]byte(`{"code":243,"status":1,"tag":"ips-s1"}`))
		case "missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	c := regula.New(srv.URL + "/")
	ctx := context.Background()

	tx, err := c.GetLiveness(ctx, "tx 1")
	if err != nil || !tx.Confirmed() || tx.Tag != "ips-s1" {
		t.Fatalf("GetLiveness(tx 1) = %+v, %v; want confirmed with tag ips:s1", tx, err)
	}
	tx, err = c.GetLiveness(ctx, "unlive")
	if err != nil || tx.Confirmed() || tx.Code != 243 {
		t.Fatalf("GetLiveness(unlive) = %+v, %v; want unconfirmed, code 243", tx, err)
	}
	if _, err := c.GetLiveness(ctx, "missing"); !errors.Is(err, regula.ErrTransactionNotFound) {
		t.Fatalf("GetLiveness(missing) err = %v, want ErrTransactionNotFound", err)
	}
	if _, err := c.GetLiveness(ctx, "boom"); err == nil || errors.Is(err, regula.ErrTransactionNotFound) {
		t.Fatalf("GetLiveness(boom) err = %v, want a status error", err)
	}
}

func TestMatch(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/match" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s (%s)", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"code":0,"results":[{"similarity":0.93}],"detections":[
			{"imageIndex":1,"status":0,"faces":[{"crop":"UkVGQ1JPUA=="}]},
			{"imageIndex":2,"status":0,"faces":[{"crop":"TElWRUNST1A="}]}]}`))
	}))
	defer srv.Close()

	res, err := regula.New(srv.URL).Match(context.Background(), "UkVG", "tx1")
	if err != nil || res.Similarity != 0.93 || res.LiveCrop != "TElWRUNST1A=" {
		t.Fatalf("Match = %+v, %v; want 0.93 with the live image's crop", res, err)
	}
	images := got["images"].([]any)
	ref, live := images[0].(map[string]any), images[1].(map[string]any)
	if ref["type"] != float64(2) || ref["data"] != "UkVG" || live["type"] != float64(3) || live["livenessTransactionId"] != "tx1" {
		t.Fatalf("match request images = %v", images)
	}
	params, _ := got["outputImageParams"].(map[string]any)
	if crop, _ := params["crop"].(map[string]any); crop == nil || crop["type"] != float64(0) {
		t.Fatalf("match request outputImageParams = %v, want a 3:4 crop", got["outputImageParams"])
	}
}

func TestMatchWithoutResultsIsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"results":[]}`))
	}))
	defer srv.Close()
	if res, err := regula.New(srv.URL).Match(context.Background(), "UkVG", "tx1"); err != nil || res != (regula.MatchResult{}) {
		t.Fatalf("Match = %+v, %v; want zero, nil", res, err)
	}
}

func TestMatchServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if _, err := regula.New(srv.URL).Match(context.Background(), "UkVG", "tx1"); err == nil {
		t.Fatal("Match on 502: want an error")
	}
}

func TestDeleteLiveness(t *testing.T) {
	status := http.StatusNoContent
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v2/liveness" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		deleted = r.URL.Query().Get("transactionId")
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := regula.New(srv.URL)

	if err := c.DeleteLiveness(context.Background(), "tx1"); err != nil || deleted != "tx1" {
		t.Fatalf("DeleteLiveness = %v (deleted %q)", err, deleted)
	}
	status = http.StatusOK
	if err := c.DeleteLiveness(context.Background(), "tx1"); err != nil {
		t.Fatalf("DeleteLiveness on 200 = %v, want nil", err)
	}
	status = http.StatusNotFound
	if err := c.DeleteLiveness(context.Background(), "tx1"); err == nil {
		t.Fatal("DeleteLiveness on 404: want an error")
	}
}

func TestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if _, err := regula.New(srv.URL).GetLiveness(context.Background(), "tx1"); err == nil || errors.Is(err, regula.ErrTransactionNotFound) {
		t.Fatalf("GetLiveness on a closed server err = %v, want a transport error", err)
	}
}

func TestContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := regula.New(srv.URL).GetLiveness(ctx, "tx1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetLiveness with a cancelled context err = %v, want context.Canceled", err)
	}
}

func TestDeleteLivenessByTag(t *testing.T) {
	status := http.StatusNoContent
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v2/liveness" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		query = r.URL.RawQuery
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := regula.New(srv.URL)

	if err := c.DeleteLivenessByTag(context.Background(), "ips-tref_ab12"); err != nil || query != "tag=ips-tref_ab12" {
		t.Fatalf("DeleteLivenessByTag = %v (query %q), want only the tag", err, query)
	}
	status = http.StatusNotFound
	if err := c.DeleteLivenessByTag(context.Background(), "ips-tref_ab12"); err != nil {
		t.Errorf("a tag with nothing left (404) = %v, want nil", err)
	}
	status = http.StatusInternalServerError
	if err := c.DeleteLivenessByTag(context.Background(), "ips-tref_ab12"); err == nil {
		t.Error("DeleteLivenessByTag on 500: want an error")
	}
	if err := c.DeleteLivenessByTag(context.Background(), ""); err == nil {
		t.Error("an empty tag would delete by nothing: want an error")
	}
}
