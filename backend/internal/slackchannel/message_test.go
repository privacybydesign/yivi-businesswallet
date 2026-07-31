package slackchannel

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
)

const testAuditURL = "https://wallet.example.org/acme/audit-log"

func TestEventMessageNamesTheEventAndItsDetails(t *testing.T) {
	e := notifications.Event{
		OrgID:  uuid.New(),
		Action: audit.MembershipRoleChanged,
		Metadata: map[string]any{
			"before": map[string]any{"role": "member"},
			"after":  map[string]any{"role": "admin"},
		},
	}

	got, err := eventMessage(e, "Acme B.V.", testAuditURL, email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}

	name, _ := email.EventLabel(audit.MembershipRoleChanged, email.LocaleEN)
	want := "*" + name + "* · Acme B.V.\nrole: member → admin\n" + testAuditURL
	if got.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", got.Text, want)
	}
}

// An event whose metadata says nothing worth repeating is still worth posting: the
// message is then the heading and the link to the full record.
func TestEventMessageOmitsEmptyDetails(t *testing.T) {
	got, err := eventMessage(
		notifications.Event{Action: audit.WalletSuspended},
		"Acme B.V.", testAuditURL, email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}
	if lines := strings.Split(got.Text, "\n"); len(lines) != 2 {
		t.Errorf("text = %q, want a heading and a link only", got.Text)
	}
}

// The event's name is the mail catalogue's, so the same event reads the same in
// both channels — including in Dutch.
func TestEventMessageFollowsTheDeploymentLocale(t *testing.T) {
	dutch, err := eventMessage(
		notifications.Event{Action: audit.MembershipInvited},
		"Acme B.V.", testAuditURL, email.LocaleNL)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}
	name, ok := email.EventLabel(audit.MembershipInvited, email.LocaleNL)
	if !ok {
		t.Fatal("the mail catalogue has no Dutch name for membership.invited")
	}
	if !strings.HasPrefix(dutch.Text, "*"+name+"*") {
		t.Errorf("text = %q, want it to open with the Dutch event name %q", dutch.Text, name)
	}
}

// Naming the raw audit action would be worse than a logged gap, so an action the
// catalogue does not label is an error.
func TestEventMessageRefusesAnUnnamedAction(t *testing.T) {
	_, err := eventMessage(
		notifications.Event{Action: "nonsense.made_up"},
		"Acme B.V.", testAuditURL, email.LocaleEN)
	if err == nil {
		t.Fatal("eventMessage accepted an action the catalogue has no name for")
	}
}

// &, < and > are Slack's own control characters: a raw <...> is read as a link, so
// a value carrying them would change the message's structure instead of appearing
// in it.
func TestEventMessageEscapesSlackMarkup(t *testing.T) {
	e := notifications.Event{
		Action:   audit.MembershipInvited,
		Metadata: map[string]any{"email": "<sam@example.org|click me>"},
	}

	got, err := eventMessage(e, "Tom & Jerry <B.V.>", testAuditURL, email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}
	if strings.ContainsAny(strings.TrimPrefix(got.Text, "*"), "<>") {
		t.Errorf("text = %q, want < and > escaped", got.Text)
	}
	for _, want := range []string{"Tom &amp; Jerry &lt;B.V.&gt;", "&lt;sam@example.org|click me&gt;"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text = %q, want it to contain %q", got.Text, want)
		}
	}
}

func TestTestMessageIsJustTheHeading(t *testing.T) {
	got := testMessage("Acme B.V.")
	if want := "*Test notification* · Acme B.V."; got.Text != want {
		t.Errorf("text = %q, want %q", got.Text, want)
	}
}
