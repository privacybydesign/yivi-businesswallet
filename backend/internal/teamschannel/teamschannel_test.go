package teamschannel

import (
	"errors"
	"strings"
	"testing"
)

// The two kinds of URL the Teams settings accept, in the shapes Microsoft actually
// hands out: a connector's incoming webhook on the tenant's own subdomain, and a
// Power Automate trigger with its signature in the query and https' own port
// written out in full.
func TestNormalizeWebhookURLAcceptsBothKindsOfTeamsWebhook(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
	}{
		"an office 365 connector": {
			raw:  "  https://contoso.webhook.office.com/webhookb2/abc-def@ghi-jkl/IncomingWebhook/0123/mno-pqr  ",
			want: "https://contoso.webhook.office.com/webhookb2/abc-def@ghi-jkl/IncomingWebhook/0123/mno-pqr",
		},
		"a power automate workflow": {
			raw:  "https://prod-27.westeurope.logic.azure.com:443/workflows/9f8e/triggers/manual/paths/invoke?api-version=2016-06-01&sig=s3cr3t",
			want: "https://prod-27.westeurope.logic.azure.com/workflows/9f8e/triggers/manual/paths/invoke?api-version=2016-06-01&sig=s3cr3t",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizeWebhookURL(tc.raw)
			if err != nil {
				t.Fatalf("NormalizeWebhookURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("NormalizeWebhookURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A Power Automate trigger URL keeps its credential in the query, not the path, so
// dropping the query would store a URL that cannot post — and one that reads as
// saved fine until the first notification is silently refused.
func TestNormalizeWebhookURLKeepsTheQuerySignature(t *testing.T) {
	got, err := NormalizeWebhookURL(
		"https://prod-27.westeurope.logic.azure.com:443/workflows/9f8e/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=s3cr3tsignature")
	if err != nil {
		t.Fatalf("NormalizeWebhookURL: %v", err)
	}
	for _, want := range []string{"sig=s3cr3tsignature", "sp=%2Ftriggers%2Fmanual%2Frun", "sv=1.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("NormalizeWebhookURL() = %q, want the query kept (%q missing)", got, want)
		}
	}
}

// DNS is case-insensitive, so a webhook pasted with a capitalised host is the same
// webhook. It is stored lowercased, and without the redundant :443, so what is kept
// is one shape whatever was pasted.
func TestNormalizeWebhookURLCanonicalisesTheHost(t *testing.T) {
	want := "https://contoso.webhook.office.com/webhookb2/abc/IncomingWebhook/0123/def"
	for _, raw := range []string{
		"https://CONTOSO.WEBHOOK.OFFICE.COM/webhookb2/abc/IncomingWebhook/0123/def",
		"https://Contoso.Webhook.Office.com/webhookb2/abc/IncomingWebhook/0123/def",
		"https://contoso.webhook.office.com:443/webhookb2/abc/IncomingWebhook/0123/def",
	} {
		got, err := NormalizeWebhookURL(raw)
		if err != nil {
			t.Fatalf("NormalizeWebhookURL(%q): %v", raw, err)
		}
		if got != want {
			t.Errorf("NormalizeWebhookURL(%q) = %q, want %q", raw, got, want)
		}
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

// Posting is the one outbound request an org admin gets to aim, so anything that is
// not one of Microsoft's own webhook hosts is refused at save time rather than
// requested later.
func TestNormalizeWebhookURLRejectsAnythingButATeamsHost(t *testing.T) {
	cases := map[string]string{
		"plain http":            "http://contoso.webhook.office.com/webhookb2/abc/IncomingWebhook/0/d",
		"the suffix as a label": "https://contoso.webhook.office.com.example.org/webhookb2/abc",
		"a lookalike host":      "https://webhook-office.com/webhookb2/abc/IncomingWebhook/0/d",
		"the bare suffix":       "https://webhook.office.com/webhookb2/abc/IncomingWebhook/0/d",
		"the bare azure suffix": "https://logic.azure.com/workflows/9f8e/triggers/manual/paths/invoke",
		"another azure service": "https://contoso.blob.core.windows.net/container/blob",
		"internal address":      "https://169.254.169.254/latest/meta-data/",
		"loopback":              "https://127.0.0.1/webhookb2/abc/IncomingWebhook/0/d",
		"a port of its own":     "https://contoso.webhook.office.com:8080/webhookb2/abc",
		"credentials in url":    "https://user:pass@contoso.webhook.office.com/webhookb2/abc",
		"host only":             "https://contoso.webhook.office.com",
		"host and slash":        "https://contoso.webhook.office.com/",
		"query but no path":     "https://prod-27.westeurope.logic.azure.com/?sig=s3cr3t",
		"not a url at all":      "paste your webhook here",
		"scheme relative":       "//contoso.webhook.office.com/webhookb2/abc",
		"a file url":            "file:///etc/passwd",
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
// the URL is the tenant's secret and a rejected paste is often a near miss.
func TestInvalidWebhookErrorRepeatsNoInput(t *testing.T) {
	secret := "webhookb2-supersecret-token"
	_, err := NormalizeWebhookURL("https://evil.example.org/" + secret)
	if err == nil {
		t.Fatal("NormalizeWebhookURL accepted a non-Microsoft host")
	}
	if got := err.Error(); strings.Contains(got, secret) {
		t.Errorf("error = %q, want no part of the submitted value", got)
	}
}

// The copy that tells an admin which URL to paste is derived from the pinned host
// list, so adding a host cannot leave the API error naming the old set.
func TestWebhookHostsDescriptionNamesEveryPinnedHost(t *testing.T) {
	got := WebhookHostsDescription()
	for _, suffix := range webhookHostSuffixes {
		host := strings.TrimPrefix(suffix, ".")
		if !strings.Contains(got, host) {
			t.Errorf("WebhookHostsDescription() = %q, want %q named", got, host)
		}
	}
	if strings.Contains(got, "..") || strings.HasPrefix(got, ".") {
		t.Errorf("WebhookHostsDescription() = %q, want the suffixes' leading dots dropped", got)
	}
}
