package teamschannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// webhookPath is the shape a connector's URL takes, and the secret half of it: the
// tests below assert it never reaches an error or a log line.
const webhookPath = "/webhookb2/T0SECRET@B0SECRET/IncomingWebhook/0123/tokentokentoken"

// installTestLogger captures what the channel logs, which is the whole of what a
// tolerated misconfiguration produces.
func installTestLogger(buf *bytes.Buffer) func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	return func() { slog.SetDefault(prev) }
}

// stubWebhooks stands in for *Store. The channel posts whatever is stored, so a
// test can hand it the URL of a local server.
type stubWebhooks struct {
	url string
	err error
}

func (s *stubWebhooks) webhookFor(context.Context, uuid.UUID) (string, error) {
	return s.url, s.err
}

type stubDirectory struct {
	org organization.Organization
	err error
}

func (s stubDirectory) GetByID(context.Context, uuid.UUID) (organization.Organization, error) {
	return s.org, s.err
}

func acmeDirectory() stubDirectory {
	return stubDirectory{org: organization.Organization{
		ID: uuid.New(), Name: "Acme B.V.", Slug: "acme",
	}}
}

// teamsStub answers like a Teams endpoint: a connector's "1" on success, or the
// status it refused with.
func teamsStub(t *testing.T, status int, answer string) (*httptest.Server, *[]string) {
	t.Helper()
	posted := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*posted = append(*posted, string(body))
		if got := r.Header.Get("Content-Type"); got != contentTypeJSON {
			t.Errorf("Content-Type = %q, want %q", got, contentTypeJSON)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	return server, posted
}

func newChannel(url string) *Channel {
	return New(&stubWebhooks{url: url}, acmeDirectory(), "https://wallet.example.org/", email.LocaleEN)
}

func event() notifications.Event {
	return notifications.Event{
		OrgID:    uuid.New(),
		Action:   audit.MembershipInvited,
		Metadata: map[string]any{"after": map[string]any{"email": "sam@example.org"}},
	}
}

func TestNotifyPostsTheEventToTheWebhook(t *testing.T) {
	server, posted := teamsStub(t, http.StatusOK, "1")
	channel := newChannel(server.URL + webhookPath)

	if err := channel.Notify(context.Background(), event()); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if len(*posted) != 1 {
		t.Fatalf("posted %d messages, want 1", len(*posted))
	}
	var got message
	if err := json.Unmarshal([]byte((*posted)[0]), &got); err != nil {
		t.Fatalf("decode posted message: %v", err)
	}
	name, _ := email.EventLabel(audit.MembershipInvited, email.LocaleEN)
	rendered := cardText(got)
	for _, want := range []string{
		name, "Acme B.V.", "email: sam@example.org",
		"https://wallet.example.org/acme/audit-log",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("posted card = %q, want it to contain %q", rendered, want)
		}
	}
}

// A Power Automate workflow answers an accepted trigger 202 with no body, so a
// channel that only took 200 for delivered would report every real workflow post as
// a refusal.
func TestNotifyAcceptsEveryTwoHundredStatus(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent} {
		server, _ := teamsStub(t, status, "")
		channel := newChannel(server.URL + webhookPath)

		if err := channel.Notify(context.Background(), event()); err != nil {
			t.Errorf("Notify with status %d = %v, want it taken as delivered", status, err)
		}
	}
}

func TestChannelIDIsTeams(t *testing.T) {
	channel := newChannel("")
	if got := channel.ID(); got != notifications.ChannelTeams {
		t.Errorf("ID() = %q, want %q", got, notifications.ChannelTeams)
	}
}

// An org that subscribed to Teams without a usable webhook is a misconfiguration to
// warn about, not an error to log per event: it would otherwise be one ERROR line
// per event, every pass, for a setting only an admin can fix.
func TestNotifyToleratesAnUnconfiguredOrg(t *testing.T) {
	var logged bytes.Buffer
	defer installTestLogger(&logged)()

	channel := New(&stubWebhooks{err: ErrNotConfigured}, acmeDirectory(),
		"https://wallet.example.org", email.LocaleEN)

	e := event()
	for range 2 {
		if err := channel.Notify(context.Background(), e); err != nil {
			t.Fatalf("Notify: %v", err)
		}
	}

	if got := strings.Count(logged.String(), "no webhook is configured"); got != 1 {
		t.Errorf("logged the misconfiguration %d times, want once per organization", got)
	}
}

func TestNotifyReportsARefusedWebhook(t *testing.T) {
	server, _ := teamsStub(t, http.StatusNotFound, "Webhook not found")
	channel := newChannel(server.URL + webhookPath)

	err := channel.Notify(context.Background(), event())

	var delivery *DeliveryError
	if !errors.As(err, &delivery) {
		t.Fatalf("Notify = %v, want a *DeliveryError", err)
	}
	if !strings.Contains(delivery.Reason, "404") {
		t.Errorf("Reason = %q, want the status this side read", delivery.Reason)
	}
}

// Neither endpoint kind documents a closed set of refusals to allowlist, so no byte
// of the answer is repeated — not the body, and not the reason phrase after the
// status code, which the far side also writes.
func TestNoPartOfTheAnswerIsRepeated(t *testing.T) {
	page := "<html>Access denied by the egress proxy, ref 41ba</html>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A status line whose reason phrase is the far side's to choose, too.
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	channel := newChannel(server.URL + webhookPath)

	var delivery *DeliveryError
	if err := channel.Notify(context.Background(), event()); !errors.As(err, &delivery) {
		t.Fatalf("Notify = %v, want a *DeliveryError", err)
	}
	if !strings.Contains(delivery.Reason, answerNotRepeated) {
		t.Errorf("Reason = %q, want it to say the answer is not repeated", delivery.Reason)
	}
	for _, unwanted := range []string{"Access denied", "egress proxy", "41ba", "<html>"} {
		if strings.Contains(delivery.Reason, unwanted) {
			t.Errorf("Reason = %q, want none of the answer repeated (found %q)", delivery.Reason, unwanted)
		}
	}
	if !strings.Contains(delivery.Reason, "403") {
		t.Errorf("Reason = %q, want the status code this side read", delivery.Reason)
	}
}

// The status is the numeric code, not resp.Status: the reason phrase after the code
// is written by whatever answered, and this reason reaches a log line and an admin's
// screen. That is what leaves redact only the transport error to strip.
func TestRefusalReasonCarriesTheCodeAndNothingElse(t *testing.T) {
	got := refusalReason(http.StatusBadGateway)
	if !strings.HasPrefix(got, "status 502") {
		t.Errorf("refusalReason(502) = %q, want it to start with the code", got)
	}
	if !strings.Contains(got, answerNotRepeated) {
		t.Errorf("refusalReason(502) = %q, want it to say the answer is not repeated", got)
	}
}

// redact still has the transport error to strip, so it is pinned directly on every
// shape it claims: a reason assembled here later must not be the round that finds
// out. Both halves matter — a connector keeps its secret in the path, a Power
// Automate trigger in the query signature.
func TestRedactReplacesEveryShapeItCovers(t *testing.T) {
	for _, webhook := range []string{
		"https://contoso.webhook.office.com" + webhookPath,
		"https://prod-27.westeurope.logic.azure.com/workflows/9f8e/triggers/manual/paths/invoke?sig=s3cr3tsignature",
	} {
		secret := strings.TrimPrefix(webhook, "https://")
		secret = secret[strings.Index(secret, "/"):]
		for _, tc := range []struct {
			name   string
			quoted string
		}{
			{"the url verbatim", webhook},
			{"the decoded path and query", secret},
			{"the separators percent-encoded", strings.ReplaceAll(secret, "/", "%2F")},
			{"the separators encoded in lower case", strings.ReplaceAll(secret, "/", "%2f")},
			{"the separators json-escaped", strings.ReplaceAll(webhook, "/", `\/`)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := redact("Post "+tc.quoted+": dial tcp: i/o timeout", webhook)
				if !strings.Contains(got, redactedWebhook) {
					t.Errorf("redact(%q) = %q, want the url replaced", tc.quoted, got)
				}
				for _, shape := range secretShapes(secret) {
					if strings.Contains(got, shape) {
						t.Errorf("redact(%q) = %q, want no webhook secret left (found %q)", tc.quoted, got, shape)
					}
				}
			})
		}
	}
}

// The reason reaches both a log line and an admin's screen, and the URL is the
// tenant's secret — net/http puts it in every transport error, and an answer from
// something other than Microsoft (a proxy) can quote it back.
func TestDeliveryErrorsCarryNoWebhookURL(t *testing.T) {
	t.Run("a proxy quoting the request url", func(t *testing.T) {
		// Not Microsoft answering, but something in front of it repeating the request it
		// refused — in a shape of its own, so the stored URL is not matched verbatim.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Access denied for POST https://contoso.webhook.office.com" + r.URL.Path))
		}))
		defer server.Close()

		err := newChannel(server.URL+webhookPath).Notify(context.Background(), event())
		assertNoSecret(t, err, webhookPath)
	})

	t.Run("a proxy quoting the query signature", func(t *testing.T) {
		// A Power Automate URL's credential is the query, so an answer that echoes the
		// query string carries it just as a path echo would.
		query := "?api-version=2016-06-01&sig=s3cr3tsignature"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("denied: query=" + r.URL.RawQuery))
		}))
		defer server.Close()

		err := newChannel(server.URL+"/workflows/9f8e/triggers/manual/paths/invoke"+query).
			Notify(context.Background(), event())
		assertNoSecret(t, err, "s3cr3tsignature")
	})

	t.Run("a gateway answering a JSON error document", func(t *testing.T) {
		// A JSON answer escapes the separators as "\/", which is a substring of none of
		// the percent-encoded forms.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentTypeJSON)
			w.WriteHeader(http.StatusForbidden)
			quoted := strings.ReplaceAll("https://contoso.webhook.office.com"+r.URL.Path, "/", `\/`)
			_, _ = w.Write([]byte(`{"error":"denied","url":"` + quoted + `"}`))
		}))
		defer server.Close()

		err := newChannel(server.URL+webhookPath).Notify(context.Background(), event())
		assertNoSecret(t, err, webhookPath)
	})

	t.Run("an unreachable host", func(t *testing.T) {
		// A closed port: Do fails, and its *url.Error names the whole URL.
		closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closed.Close()

		err := newChannel(closed.URL+webhookPath).Notify(context.Background(), event())
		assertNoSecret(t, err, webhookPath)
	})
}

// secretShapes lists the spellings of the webhook secret an assertion has to look
// for. It is deliberately longer than what any single guard replaces: whatever this
// enumerates is all a test can ever notice, which is why a refusal repeats no answer
// at all rather than relying on it.
func secretShapes(secret string) []string {
	trimmed := strings.Trim(secret, "/")
	return []string{
		trimmed,
		strings.ReplaceAll(trimmed, "/", "%2F"),
		strings.ReplaceAll(trimmed, "/", "%2f"),
		strings.ReplaceAll(trimmed, "/", `\/`),
		strings.ReplaceAll(trimmed, "/", "&#47;"),
		strings.ReplaceAll(trimmed, "/", "%252F"),
	}
}

// assertNoSecret fails if the webhook's secret survived into the error in any shape
// an answer can quote it in. Checking the decoded form alone passes over an encoded
// quote, which carries the same credential.
func assertNoSecret(t *testing.T, err error, secret string) {
	t.Helper()
	var delivery *DeliveryError
	if !errors.As(err, &delivery) {
		t.Fatalf("err = %v, want a *DeliveryError", err)
	}
	for _, shape := range secretShapes(secret) {
		if strings.Contains(delivery.Error(), shape) {
			t.Errorf("error = %q, want the webhook redacted (found %q)", delivery.Error(), shape)
		}
	}
}

func TestSendTestPostsTheSpecimen(t *testing.T) {
	server, posted := teamsStub(t, http.StatusOK, "1")
	channel := newChannel(server.URL + webhookPath)

	if err := channel.SendTest(context.Background(), uuid.New(), "Acme B.V."); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if len(*posted) != 1 || !strings.Contains((*posted)[0], testMessageTitle) {
		t.Errorf("posted %v, want one test notification", *posted)
	}
}

// The test action is the admin asking whether it works, so "not configured" is their
// answer rather than a warning in a log they cannot read.
func TestSendTestReportsAnUnconfiguredOrg(t *testing.T) {
	channel := New(&stubWebhooks{err: ErrNotConfigured}, acmeDirectory(),
		"https://wallet.example.org", email.LocaleEN)

	err := channel.SendTest(context.Background(), uuid.New(), "Acme B.V.")
	if !errors.Is(err, ErrNotConfigured) {
		t.Errorf("SendTest = %v, want ErrNotConfigured", err)
	}
}

// A redirect could only move the post — with the organization's event metadata in
// its body — to a host other than the one the saved URL was pinned to.
func TestPostDoesNotFollowRedirects(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the post followed a redirect off Microsoft's host")
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	var delivery *DeliveryError
	err := newChannel(server.URL+webhookPath).Notify(context.Background(), event())
	if !errors.As(err, &delivery) {
		t.Errorf("Notify = %v, want the redirect reported as a refusal", err)
	}
}
