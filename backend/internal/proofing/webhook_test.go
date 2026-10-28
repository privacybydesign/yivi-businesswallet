package proofing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWebhookSignature(t *testing.T) {
	at := time.Unix(1_790_000_000, 0)
	body := []byte(`{"id":"x"}`)
	got := webhookSignature([]string{"whsec_test"}, at, body)

	mac := hmac.New(sha256.New, []byte("whsec_test"))
	mac.Write([]byte("1790000000." + string(body)))
	want := "t=1790000000,v1=" + hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
	if webhookSignature([]string{"whsec_other"}, at, body) == got {
		t.Error("another secret gave the same signature")
	}
}

// Eight attempts spread over about a day, then the delivery is failed.
func TestWebhookRetriesSpanADay(t *testing.T) {
	if MaxWebhookAttempts != 8 {
		t.Errorf("MaxWebhookAttempts = %d, want 8", MaxWebhookAttempts)
	}
	var total time.Duration
	for attempt := 1; ; attempt++ {
		delay, ok := nextAttemptDelay(attempt)
		if !ok {
			if attempt != MaxWebhookAttempts {
				t.Errorf("retries stop after attempt %d, want %d", attempt, MaxWebhookAttempts)
			}
			break
		}
		total += delay
	}
	if total < 23*time.Hour || total > 25*time.Hour {
		t.Errorf("retries span %v, want about 24h", total)
	}
}

func TestNewWebhookSecret(t *testing.T) {
	a, b := newWebhookSecret(), newWebhookSecret()
	if !strings.HasPrefix(a, webhookSecretPrefix) || a == b {
		t.Errorf("secrets = %q, %q; want two distinct whsec_ secrets", a, b)
	}
	if got := secretLast4(a); got != a[len(a)-4:] {
		t.Errorf("secretLast4 = %q", got)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"type":"session.verified"}`)
	signed := webhookSignature([]string{"whsec_a"}, now, body)
	for name, tc := range map[string]struct {
		secret, header string
		body           []byte
		ok             bool
	}{
		"valid":        {"whsec_a", signed, body, true},
		"other secret": {"whsec_b", signed, body, false},
		"other body":   {"whsec_a", signed, []byte(`{}`), false},
		"stale":        {"whsec_a", webhookSignature([]string{"whsec_a"}, now.Add(-webhookMaxSkew-time.Second), body), body, false},
		"no v1":        {"whsec_a", "t=1800000000", body, false},
		"empty":        {"whsec_a", "", body, false},
	} {
		err := verifyWebhookSignature(tc.secret, tc.header, tc.body, now)
		if (err == nil) != tc.ok {
			t.Errorf("%s: verifyWebhookSignature = %v, want ok %v", name, err, tc.ok)
		}
	}
}

// webhookMaxSkew is how far a received delivery's timestamp may be from now.
const webhookMaxSkew = 5 * time.Minute

// verifyWebhookSignature checks a SignatureHeader value against body under
// secret, as a receiver does: a timestamp within webhookMaxSkew and any v1
// HMAC matching.
func verifyWebhookSignature(secret, header string, body []byte, now time.Time) error {
	var ts string
	var v1s []string
	for part := range strings.SplitSeq(header, ",") {
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "t":
			ts = value
		case "v1":
			v1s = append(v1s, value)
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	at := time.Unix(unix, 0)
	if skew := now.Sub(at); skew > webhookMaxSkew || skew < -webhookMaxSkew {
		return ErrBadSignature
	}
	want := webhookSignature([]string{secret}, at, body)
	for _, got := range v1s {
		if hmac.Equal([]byte(want), []byte("t="+ts+",v1="+got)) {
			return nil
		}
	}
	return ErrBadSignature
}

// ErrBadSignature is a webhook delivery whose signature does not verify.
var ErrBadSignature = errors.New("proofing: bad webhook signature")
