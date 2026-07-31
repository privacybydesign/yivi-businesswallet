package slackchannel

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

// slackStub answers like an incoming webhook: "ok" on success, or the status and
// short refusal Slack sends back.
func slackStub(t *testing.T, status int, answer string) (*httptest.Server, *[]string) {
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

func event() notifications.Event {
	return notifications.Event{
		OrgID:    uuid.New(),
		Action:   audit.MembershipInvited,
		Metadata: map[string]any{"after": map[string]any{"email": "sam@example.org"}},
	}
}

func TestNotifyPostsTheEventToTheWebhook(t *testing.T) {
	server, posted := slackStub(t, http.StatusOK, "ok")
	channel := New(&stubWebhooks{url: server.URL + "/services/T000/B000/xxx"},
		acmeDirectory(), "https://wallet.example.org/", email.LocaleEN)

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
	for _, want := range []string{
		name, "Acme B.V.", "email: sam@example.org",
		"https://wallet.example.org/acme/audit-log",
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("posted text = %q, want it to contain %q", got.Text, want)
		}
	}
}

func TestChannelIDIsSlack(t *testing.T) {
	channel := New(&stubWebhooks{}, acmeDirectory(), "https://wallet.example.org", email.LocaleEN)
	if got := channel.ID(); got != notifications.ChannelSlack {
		t.Errorf("ID() = %q, want %q", got, notifications.ChannelSlack)
	}
}

// An org that subscribed to Slack without a usable webhook is a misconfiguration
// to warn about, not an error to log per event: it would otherwise be one ERROR
// line per event, every pass, for a setting only an admin can fix.
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
	server, _ := slackStub(t, http.StatusNotFound, "no_service")
	channel := New(&stubWebhooks{url: server.URL + "/services/T000/B000/xxx"},
		acmeDirectory(), "https://wallet.example.org", email.LocaleEN)

	err := channel.Notify(context.Background(), event())

	var delivery *DeliveryError
	if !errors.As(err, &delivery) {
		t.Fatalf("Notify = %v, want a *DeliveryError", err)
	}
	if !strings.Contains(delivery.Reason, "no_service") {
		t.Errorf("Reason = %q, want Slack's own refusal in it", delivery.Reason)
	}
}

// The reason reaches both a log line and an admin's screen, and the URL is the
// workspace's secret — net/http puts it in every transport error, and an answer
// from something other than Slack (a proxy) can quote it back.
func TestDeliveryErrorsCarryNoWebhookURL(t *testing.T) {
	secret := "/services/T0SECRET/B0SECRET/tokentokentoken"

	t.Run("a proxy quoting the request url", func(t *testing.T) {
		// Not Slack answering, but something in front of it repeating the request it
		// refused — in a shape of its own, so the stored URL is not matched verbatim.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Access denied for POST https://" + webhookHost + r.URL.Path))
		}))
		defer server.Close()
		channel := New(&stubWebhooks{url: server.URL + secret}, acmeDirectory(),
			"https://wallet.example.org", email.LocaleEN)

		err := channel.Notify(context.Background(), event())
		assertNoSecret(t, err, secret)
	})

	t.Run("a proxy quoting the path percent-encoded", func(t *testing.T) {
		// The same intermediary, writing the path the way a rewriting proxy commonly
		// does: separators percent-encoded, so nothing matches the stored URL or its
		// decoded path and the whole token would ride along.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("denied: path=" + strings.ReplaceAll(r.URL.Path, "/", "%2F")))
		}))
		defer server.Close()
		channel := New(&stubWebhooks{url: server.URL + secret}, acmeDirectory(),
			"https://wallet.example.org", email.LocaleEN)

		err := channel.Notify(context.Background(), event())
		assertNoSecret(t, err, secret)
	})

	t.Run("a proxy encoding in lower case", func(t *testing.T) {
		// %2F and %2f are the same escape, and which one an intermediary writes is its
		// own choice.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("denied: path=" + strings.ReplaceAll(r.URL.Path, "/", "%2f")))
		}))
		defer server.Close()
		channel := New(&stubWebhooks{url: server.URL + secret}, acmeDirectory(),
			"https://wallet.example.org", email.LocaleEN)

		err := channel.Notify(context.Background(), event())
		assertNoSecret(t, err, secret)
	})

	t.Run("an unreachable host", func(t *testing.T) {
		// A closed port: Do fails, and its *url.Error names the whole URL.
		closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closed.Close()
		channel := New(&stubWebhooks{url: closed.URL + secret}, acmeDirectory(),
			"https://wallet.example.org", email.LocaleEN)

		err := channel.Notify(context.Background(), event())
		assertNoSecret(t, err, secret)
	})
}

// assertNoSecret fails if the webhook's path survived into the error in any of
// the shapes an answer can quote it in. Checking the decoded path alone passes
// over an encoded quote, which carries the same credential.
func assertNoSecret(t *testing.T, err error, secret string) {
	t.Helper()
	var delivery *DeliveryError
	if !errors.As(err, &delivery) {
		t.Fatalf("err = %v, want a *DeliveryError", err)
	}
	path := strings.Trim(secret, "/")
	for _, shape := range []string{
		path,
		strings.ReplaceAll(path, "/", "%2F"),
		strings.ReplaceAll(path, "/", "%2f"),
	} {
		if strings.Contains(delivery.Error(), shape) {
			t.Errorf("error = %q, want the webhook url redacted (found %q)", delivery.Error(), shape)
		}
	}
}

func TestSendTestPostsTheSpecimen(t *testing.T) {
	server, posted := slackStub(t, http.StatusOK, "ok")
	channel := New(&stubWebhooks{url: server.URL + "/services/T000/B000/xxx"},
		acmeDirectory(), "https://wallet.example.org", email.LocaleEN)

	if err := channel.SendTest(context.Background(), uuid.New(), "Acme B.V."); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if len(*posted) != 1 || !strings.Contains((*posted)[0], testMessageTitle) {
		t.Errorf("posted %v, want one test notification", *posted)
	}
}

// The test action is the admin asking whether it works, so "not configured" is
// their answer rather than a warning in a log they cannot read.
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
		t.Error("the post followed a redirect off Slack's host")
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	channel := New(&stubWebhooks{url: server.URL + "/services/T000/B000/xxx"},
		acmeDirectory(), "https://wallet.example.org", email.LocaleEN)

	var delivery *DeliveryError
	if err := channel.Notify(context.Background(), event()); !errors.As(err, &delivery) {
		t.Errorf("Notify = %v, want the redirect reported as a refusal", err)
	}
}
