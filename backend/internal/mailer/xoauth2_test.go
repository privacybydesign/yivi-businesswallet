package mailer

import (
	"fmt"
	"net/smtp"
	"strings"
	"testing"
)

// The single client response carries the mailbox and the bearer token in the
// shape Microsoft 365 and Gmail both expect. net/smtp base64-encodes it, so the
// bytes here are the raw form.
func TestXOAuth2StartBuildsTheSaslResponse(t *testing.T) {
	auth := XOAuth2Auth("no-reply@acme.example", "access-token-1")

	mechanism, response, err := auth.Start(&smtp.ServerInfo{Name: "smtp.office365.com", TLS: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mechanism != "XOAUTH2" {
		t.Errorf("mechanism = %q, want XOAUTH2", mechanism)
	}
	want := "user=no-reply@acme.example\x01auth=Bearer access-token-1\x01\x01"
	if string(response) != want {
		t.Errorf("response = %q, want %q", response, want)
	}
}

// A bearer token is a live credential for the whole mailbox: it must never leave
// the process over an unencrypted connection, which is the same refusal
// smtp.PlainAuth makes for a password.
func TestXOAuth2StartRefusesAnUnencryptedConnection(t *testing.T) {
	auth := XOAuth2Auth("no-reply@acme.example", "access-token-1")

	_, response, err := auth.Start(&smtp.ServerInfo{Name: "smtp.office365.com", TLS: false})
	if err == nil {
		t.Fatal("the token was offered over an unencrypted connection")
	}
	if response != nil {
		t.Errorf("response = %q, want nothing sent", response)
	}
	if !strings.Contains(err.Error(), "unencrypted") {
		t.Errorf("err = %v, want it to name the unencrypted connection", err)
	}
}

// A separator byte inside either field would forge an extra SASL field. Neither
// value comes from a peer, but truncating one silently would be worse than
// refusing.
func TestXOAuth2StartRefusesASeparatorByte(t *testing.T) {
	for name, auth := range map[string]smtp.Auth{
		"username": XOAuth2Auth("no-reply@acme.example\x01auth=Bearer other", "access-token-1"),
		"token":    XOAuth2Auth("no-reply@acme.example", "token\x01auth=Bearer other"),
	} {
		if _, _, err := auth.Start(&smtp.ServerInfo{TLS: true}); err == nil {
			t.Errorf("%s: a separator byte was accepted", name)
		}
	}
}

func TestXOAuth2StartRefusesAMissingCredential(t *testing.T) {
	for name, auth := range map[string]smtp.Auth{
		"no token":    XOAuth2Auth("no-reply@acme.example", ""),
		"no username": XOAuth2Auth("", "access-token-1"),
	} {
		if _, _, err := auth.Start(&smtp.ServerInfo{TLS: true}); err == nil {
			t.Errorf("%s: an incomplete credential was accepted", name)
		}
	}
}

// XOAUTH2 has no second client message. A server that rejects the token sends a
// challenge and expects an empty response, after which it issues the failure
// status the caller reports — so Next must answer, not abort.
func TestXOAuth2NextAnswersTheChallengeEmpty(t *testing.T) {
	auth := XOAuth2Auth("no-reply@acme.example", "access-token-1")

	response, err := auth.Next([]byte(`{"status":"401"}`), true)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(response) != 0 {
		t.Errorf("response = %q, want an empty response", response)
	}

	if response, err := auth.Next(nil, false); err != nil || response != nil {
		t.Errorf("Next(_, false) = %q, %v; want nil, nil", response, err)
	}
}

// The mechanism decides which credential a send presents, and an unset mechanism
// stays on the password path so a config written before XOAUTH2 existed is
// unchanged.
func TestConfigAuthPicksTheMechanism(t *testing.T) {
	tests := map[string]struct {
		cfg     Config
		secured bool
		want    string
	}{
		"unset mechanism with a username is PLAIN": {
			cfg:     Config{Host: "mail.example.org", Username: "acme", Password: "pw"},
			secured: true,
			want:    "*smtp.plainAuth",
		},
		"unset mechanism without a username is unauthenticated": {
			cfg:     Config{Host: "mail.example.org"},
			secured: false,
			want:    "<nil>",
		},
		"xoauth2 is the token mechanism": {
			cfg: Config{
				Host: "smtp.office365.com", AuthMechanism: AuthXOAuth2,
				FromAddress: "no-reply@acme.example", AccessToken: "access-token-1",
			},
			secured: true,
			want:    "*mailer.xoauth2Auth",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			auth, err := tc.cfg.auth(tc.secured)
			if err != nil {
				t.Fatalf("auth: %v", err)
			}
			if got := typeName(auth); got != tc.want {
				t.Errorf("auth = %s, want %s", got, tc.want)
			}
		})
	}
}

// Opportunistic STARTTLS is not good enough for a bearer token: a server that
// does not offer the extension must not get an AUTH command at all, and the
// reason has to name the server rather than the auth exchange.
func TestConfigAuthRequiresStartTLSForXOAuth2(t *testing.T) {
	cfg := Config{
		Host: "smtp.office365.com", AuthMechanism: AuthXOAuth2,
		FromAddress: "no-reply@acme.example", AccessToken: "access-token-1",
	}

	auth, err := cfg.auth(false)
	if err == nil {
		t.Fatal("an XOAUTH2 send was allowed on a connection with no STARTTLS")
	}
	if auth != nil {
		t.Error("an auth was returned alongside the error")
	}
	if !strings.Contains(err.Error(), "STARTTLS") || !strings.Contains(err.Error(), cfg.Host) {
		t.Errorf("err = %v, want it to name STARTTLS and the host", err)
	}
}

// XOAUTH2 authenticates as the mailbox. Username stays the override for a tenant
// whose submitting mailbox differs from the address mail is sent from.
func TestXOAuth2AuthenticatesAsTheMailbox(t *testing.T) {
	cfg := Config{
		Host: "smtp.office365.com", AuthMechanism: AuthXOAuth2,
		FromAddress: "no-reply@acme.example", AccessToken: "access-token-1",
	}
	assertIdentity(t, cfg, "no-reply@acme.example")

	cfg.Username = "smtp-relay@acme.example"
	assertIdentity(t, cfg, "smtp-relay@acme.example")
}

func assertIdentity(t *testing.T, cfg Config, want string) {
	t.Helper()
	auth, err := cfg.auth(true)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	_, response, err := auth.Start(&smtp.ServerInfo{Name: cfg.Host, TLS: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !strings.HasPrefix(string(response), "user="+want+"\x01") {
		t.Errorf("response = %q, want it to authenticate as %s", response, want)
	}
}

// A config the transport cannot act on is refused before it dials, so the error
// an admin reads names the missing piece instead of the relay's generic
// authentication failure.
func TestValidateRejectsAnUnusableConfig(t *testing.T) {
	tests := map[string]Config{
		"unknown mechanism": {Host: "mail.example.org", AuthMechanism: "cram-md5"},
		"xoauth2 without a token": {
			Host: "smtp.office365.com", AuthMechanism: AuthXOAuth2, FromAddress: "no-reply@acme.example",
		},
		"xoauth2 with nobody to authenticate as": {
			Host: "smtp.office365.com", AuthMechanism: AuthXOAuth2, AccessToken: "access-token-1",
		},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if err := cfg.validate(); err == nil {
				t.Error("an unusable config was accepted")
			}
		})
	}

	usable := Config{Host: "mail.example.org", Username: "acme", Password: "pw"}
	if err := usable.validate(); err != nil {
		t.Errorf("validate on a password config: %v", err)
	}
}

// typeName renders which smtp.Auth implementation was picked. A nil interface
// prints as <nil>, which is the "unauthenticated relay" case.
func typeName(auth smtp.Auth) string { return fmt.Sprintf("%T", auth) }
