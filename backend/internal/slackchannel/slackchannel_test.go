package slackchannel

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeWebhookURLAcceptsASlackWebhook(t *testing.T) {
	got, err := NormalizeWebhookURL("  https://hooks.slack.com/services/T000/B000/xxxxx  ")
	if err != nil {
		t.Fatalf("NormalizeWebhookURL: %v", err)
	}
	if want := "https://hooks.slack.com/services/T000/B000/xxxxx"; got != want {
		t.Errorf("NormalizeWebhookURL() = %q, want %q", got, want)
	}
}

// Clearing the webhook is submitted as an empty string, which is not an invalid
// URL: the store reads it as "remove the stored one".
func TestNormalizeWebhookURLAcceptsAnEmptyValue(t *testing.T) {
	got, err := NormalizeWebhookURL("   ")
	if err != nil {
		t.Fatalf("NormalizeWebhookURL: %v", err)
	}
	if got != "" {
		t.Errorf("NormalizeWebhookURL() = %q, want the empty string", got)
	}
}

// Posting is the one outbound request an org admin gets to aim, so anything that
// is not Slack's own host is refused at save time rather than requested later.
func TestNormalizeWebhookURLRejectsAnythingButSlack(t *testing.T) {
	cases := map[string]string{
		"plain http":         "http://hooks.slack.com/services/T000/B000/xxxxx",
		"another host":       "https://hooks.slack.com.example.org/services/T000/B000/xxx",
		"internal address":   "https://169.254.169.254/latest/meta-data/",
		"loopback":           "https://127.0.0.1/services/T000/B000/xxxxx",
		"a port of its own":  "https://hooks.slack.com:8080/services/T000/B000/xxxxx",
		"credentials in url": "https://user:pass@hooks.slack.com/services/T000/B000/xxx",
		"host only":          "https://hooks.slack.com",
		"host and slash":     "https://hooks.slack.com/",
		"not a url at all":   "paste your webhook here",
		"scheme relative":    "//hooks.slack.com/services/T000/B000/xxxxx",
		"a file url":         "file:///etc/passwd",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizeWebhookURL(raw)
			if !errors.Is(err, ErrInvalidWebhookURL) {
				t.Fatalf("NormalizeWebhookURL(%q) = %q, %v; want ErrInvalidWebhookURL", raw, got, err)
			}
		})
	}
}

// The error is rendered into an API response, so it must not echo the value back:
// the URL is the workspace's secret and a rejected paste is often a near miss.
func TestInvalidWebhookErrorRepeatsNoInput(t *testing.T) {
	secret := "T000-B000-supersecret"
	_, err := NormalizeWebhookURL("https://evil.example.org/" + secret)
	if err == nil {
		t.Fatal("NormalizeWebhookURL accepted a non-Slack host")
	}
	if got := err.Error(); strings.Contains(got, secret) {
		t.Errorf("error = %q, want no part of the submitted value", got)
	}
}
