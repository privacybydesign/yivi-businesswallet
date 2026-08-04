package email

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailoauth"
)

// xoauth2Settings is an org configured for Microsoft 365: no password, an app
// registration instead.
func xoauth2Settings() stubSettings {
	return stubSettings{
		ok: true,
		cfg: sendConfig{
			Mailer: mailer.Config{
				Host:          "smtp.office365.com",
				Port:          587,
				AuthMechanism: mailer.AuthXOAuth2,
				FromAddress:   "no-reply@acme.example",
			},
			OAuth: mailoauth.Credentials{
				TenantID:     "tenant-1",
				ClientID:     "client-1",
				ClientSecret: "s3cret",
			},
		},
	}
}

// The bearer token is minted per send from the org's own app registration and
// handed to the transport; the credentials themselves never reach it.
func TestSendMintsAnAccessTokenForAnXOAuth2Org(t *testing.T) {
	sender := &recordingSender{}
	tokens := &stubTokens{token: "access-token-1"}
	svc := &Service{settings: xoauth2Settings(), sender: sender, tokens: tokens, defaultLocale: LocaleEN}

	if err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc"); err != nil {
		t.Fatalf("SendInvitation: %v", err)
	}
	if tokens.calls != 1 {
		t.Fatalf("token source called %d times, want 1", tokens.calls)
	}
	if tokens.creds.TenantID != "tenant-1" || tokens.creds.ClientID != "client-1" || tokens.creds.ClientSecret != "s3cret" {
		t.Errorf("token requested for %+v, want the org's app registration", tokens.creds)
	}
	if len(sender.configs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.configs))
	}
	got := sender.configs[0]
	if got.AccessToken != "access-token-1" {
		t.Errorf("AccessToken = %q, want the minted token", got.AccessToken)
	}
	if got.AuthMechanism != mailer.AuthXOAuth2 {
		t.Errorf("AuthMechanism = %q, want %q", got.AuthMechanism, mailer.AuthXOAuth2)
	}
}

// A password org must not pay for a token request it has no use for.
func TestSendDoesNotMintATokenForAPasswordOrg(t *testing.T) {
	sender := &recordingSender{}
	tokens := &stubTokens{token: "access-token-1"}
	settings := stubSettings{ok: true, cfg: sendConfig{Mailer: mailer.Config{
		Host: "mail.example.org", Port: 587, Username: "acme", Password: "pw",
	}}}
	svc := &Service{settings: settings, sender: sender, tokens: tokens, defaultLocale: LocaleEN}

	if err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc"); err != nil {
		t.Fatalf("SendInvitation: %v", err)
	}
	if tokens.calls != 0 {
		t.Errorf("token source called %d times for a password org, want 0", tokens.calls)
	}
	if len(sender.configs) != 1 || sender.configs[0].AccessToken != "" {
		t.Error("a password send carried an access token")
	}
}

// A refused credential must fail the send rather than reach the relay with an
// empty token, which the server would answer with an authentication error that
// says nothing about the expired secret behind it.
func TestSendFailsWhenTheTokenIsRefused(t *testing.T) {
	sender := &recordingSender{}
	refusal := errors.New("mailoauth: token: 401 Unauthorized")
	svc := &Service{
		settings:      xoauth2Settings(),
		sender:        sender,
		tokens:        &stubTokens{err: refusal},
		defaultLocale: LocaleEN,
	}

	err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc")
	if !errors.Is(err, refusal) {
		t.Fatalf("err = %v, want the token source's refusal", err)
	}
	if len(sender.sent) != 0 {
		t.Error("a message was sent without a token")
	}
}

// A deployment built without a token source has no OAuth path at all. Falling
// back to the password branch would present a credential the tenant does not
// have and read as an authentication failure; naming the missing source does not.
func TestSendFailsForAnXOAuth2OrgWithoutATokenSource(t *testing.T) {
	sender := &recordingSender{}
	svc := &Service{settings: xoauth2Settings(), sender: sender, defaultLocale: LocaleEN}

	err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc")
	if err == nil {
		t.Fatal("a send succeeded with no token source")
	}
	if !strings.Contains(err.Error(), "token source") {
		t.Errorf("err = %v, want it to name the missing token source", err)
	}
	if len(sender.sent) != 0 {
		t.Error("a message was sent without a token")
	}
}
