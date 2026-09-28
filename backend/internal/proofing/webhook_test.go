package proofing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestWebhookSignatureSignsTimestampAndBody(t *testing.T) {
	at := time.Unix(1_790_000_000, 0)
	body := []byte(`{"id":"x"}`)
	got := webhookSignature("whsec_test", at, body)

	mac := hmac.New(sha256.New, []byte("whsec_test"))
	mac.Write([]byte("1790000000." + string(body)))
	want := "t=1790000000,v1=" + hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
	if webhookSignature("whsec_other", at, body) == got {
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

func TestNewWebhookSecretIsRecognisableAndUnique(t *testing.T) {
	a, b := newWebhookSecret(), newWebhookSecret()
	if !strings.HasPrefix(a, webhookSecretPrefix) || a == b {
		t.Errorf("secrets = %q, %q; want two distinct whsec_ secrets", a, b)
	}
	if got := secretLast4(a); got != a[len(a)-4:] {
		t.Errorf("secretLast4 = %q", got)
	}
}
