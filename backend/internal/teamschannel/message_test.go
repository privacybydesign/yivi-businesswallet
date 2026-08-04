package teamschannel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
)

// The envelope is the whole reason a card is posted instead of plain text: a Power
// Automate workflow trigger accepts nothing else, and it is also what an Office 365
// connector takes, so one payload covers both kinds of URL the settings accept.
func TestEventMessageIsAnAdaptiveCardInTheMessageEnvelope(t *testing.T) {
	got, err := eventMessage(inviteEvent(), "Acme B.V.", "https://wallet.example.org/acme/audit-log", email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}

	if got.Type != messageType {
		t.Errorf("type = %q, want %q", got.Type, messageType)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(got.Attachments))
	}
	attached := got.Attachments[0]
	if attached.ContentType != adaptiveCardType {
		t.Errorf("contentType = %q, want %q", attached.ContentType, adaptiveCardType)
	}
	if attached.Content.Type != cardType || attached.Content.Version != cardVersion {
		t.Errorf("card = %s/%s, want %s/%s",
			attached.Content.Type, attached.Content.Version, cardType, cardVersion)
	}
	for _, block := range attached.Content.Body {
		if block.Type != textBlockType {
			t.Errorf("body block type = %q, want %q", block.Type, textBlockType)
		}
		if !block.Wrap {
			t.Errorf("body block %q does not wrap; a long value would be cut off", block.Text)
		}
	}
}

// The event's name comes from the mail catalogue, so a Teams card, a Slack message
// and a notification mail call the same event the same thing.
func TestEventMessageCarriesTheEventTheOrgAndTheLink(t *testing.T) {
	got, err := eventMessage(inviteEvent(), "Acme B.V.", "https://wallet.example.org/acme/audit-log", email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}

	name, _ := email.EventLabel(audit.MembershipInvited, email.LocaleEN)
	rendered := cardText(got)
	for _, want := range []string{
		name, "Acme B.V.", "email: sam@example.org",
		"https://wallet.example.org/acme/audit-log",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("card text = %q, want it to contain %q", rendered, want)
		}
	}
}

// Each summarized field gets a block of its own. A TextBlock renders Markdown,
// where a lone "\n" is not a line break, so the newline-joined string
// notifications.Summarize returns would arrive as one run-together line if it were
// placed in a single block.
func TestEventMessagePutsEachSummaryFieldInItsOwnBlock(t *testing.T) {
	e := inviteEvent()
	e.Action = audit.MembershipRoleChanged
	e.Metadata = map[string]any{
		"before": map[string]any{"role": "member", "email": "sam@example.org"},
		"after":  map[string]any{"role": "admin", "email": "sam@example.org"},
	}

	got, err := eventMessage(e, "Acme B.V.", "https://wallet.example.org/acme/audit-log", email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}

	for _, block := range got.Attachments[0].Content.Body {
		if strings.Contains(block.Text, "\n") {
			t.Errorf("block %q carries a newline; Teams would run its lines together", block.Text)
		}
	}
	// The heading, the org, the two summarized fields and the link.
	if len(got.Attachments[0].Content.Body) != 5 {
		t.Errorf("body blocks = %d, want one per line: %+v",
			len(got.Attachments[0].Content.Body), got.Attachments[0].Content.Body)
	}
	rendered := cardText(got)
	for _, want := range []string{"email: sam@example.org", "role: member → admin"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("card text = %q, want it to contain %q", rendered, want)
		}
	}
}

// An action the mail catalogue has no name for is an error rather than a card that
// names the raw audit action at a reader who has never seen one.
func TestEventMessageRefusesAnEventWithNoName(t *testing.T) {
	e := inviteEvent()
	e.Action = "not.a.real.action"

	if _, err := eventMessage(e, "Acme B.V.", "https://wallet.example.org/acme/audit-log", email.LocaleEN); err == nil {
		t.Error("eventMessage accepted an action the mail catalogue has no name for")
	}
}

// An event whose metadata summarizes to nothing gets no empty block: an Adaptive
// Card renders a blank TextBlock as a gap.
func TestEventMessageLeavesOutAnEmptySummary(t *testing.T) {
	e := inviteEvent()
	e.Metadata = nil

	got, err := eventMessage(e, "Acme B.V.", "https://wallet.example.org/acme/audit-log", email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}
	for _, block := range got.Attachments[0].Content.Body {
		if strings.TrimSpace(block.Text) == "" {
			t.Error("the card carries an empty text block")
		}
	}
}

// The specimen is the heading of a real notification and nothing else — no event to
// describe and no record to link to.
func TestTestMessageIsTheHeadingOnly(t *testing.T) {
	got := testMessage("Acme B.V.")

	body := got.Attachments[0].Content.Body
	if len(body) != 2 {
		t.Fatalf("body blocks = %d, want the title and the organization only: %+v", len(body), body)
	}
	if body[0].Text != testMessageTitle || body[1].Text != "Acme B.V." {
		t.Errorf("body = %+v, want %q above the organization's name", body, testMessageTitle)
	}
}

// A value the card interpolates must not become a link. A notification from the
// reader's own wallet naming their own organization is exactly the message a
// planted link would be trusted in.
func TestEscapeNeutralisesTheMarkdownLinkConstruct(t *testing.T) {
	e := inviteEvent()
	e.Metadata = map[string]any{"after": map[string]any{
		"email": "[Reset your password](https://phish.example/steal)",
	}}

	got, err := eventMessage(e, "Acme [B.V.](https://phish.example)", "https://wallet.example.org/acme/audit-log", email.LocaleEN)
	if err != nil {
		t.Fatalf("eventMessage: %v", err)
	}

	rendered := cardText(got)
	// The bracket that opens a link label is what has to be dead; the text of the
	// value itself is still shown, escape or no escape.
	for _, live := range []string{"[Reset your password]", "[B.V.]"} {
		if strings.Contains(rendered, live) {
			t.Errorf("card text = %q, want the link label escaped (found %q)", rendered, live)
		}
	}
	if !strings.Contains(rendered, `\[Reset your password\]`) {
		t.Errorf("card text = %q, want the brackets backslash-escaped", rendered)
	}
}

// Escaping the escape is how a value would otherwise smuggle a live bracket past a
// guard that only replaced the bracket.
func TestEscapeCannotBeEscaped(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"a bare bracket":     {"[label](url)", `\[label\](url)`},
		"an escaped bracket": {`\[label](url)`, `\\\[label\](url)`},
		"a lone backslash":   {`C:\path`, `C:\\path`},
		// Emphasis is deliberately left live: it can only change how a value looks, and
		// escaping it would show a backslash in every address like this one on a client
		// that ignores the escape.
		"an underscored address": {"sam_smith@example.org", "sam_smith@example.org"},
		"nothing to escape":      {"Acme B.V.", "Acme B.V."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := escape(tc.in); got != tc.want {
				t.Errorf("escape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The card is written by encoding/json, so a value cannot break out of its field
// and become part of the card's structure however it is spelled.
func TestAValueCannotChangeTheCardsStructure(t *testing.T) {
	got := testMessage(`Acme","body":[{"type":"TextBlock","text":"injected`)

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var round message
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if len(round.Attachments[0].Content.Body) != 2 {
		t.Errorf("body blocks = %d, want the value kept inside its own field",
			len(round.Attachments[0].Content.Body))
	}
}

func inviteEvent() notifications.Event {
	return notifications.Event{
		Action:   audit.MembershipInvited,
		Metadata: map[string]any{"after": map[string]any{"email": "sam@example.org"}},
	}
}

// cardText is every line the card renders, which is what an assertion about the
// message's content is really about.
func cardText(m message) string {
	lines := make([]string, 0, len(m.Attachments[0].Content.Body))
	for _, block := range m.Attachments[0].Content.Body {
		lines = append(lines, block.Text)
	}
	return strings.Join(lines, "\n")
}
