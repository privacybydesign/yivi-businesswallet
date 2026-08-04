package teamschannel

import (
	"fmt"
	"strings"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
)

// The payload posted to a Teams webhook: an Adaptive Card inside the
// message/attachments envelope.
//
// One shape has to work on both kinds of URL the settings accept, and this is the
// one that does. An Office 365 connector takes a bare {"text": …}, a MessageCard or
// this envelope; a Power Automate workflow trigger — what the Teams "Workflows" app
// hands out, and where Microsoft is moving everyone — takes only this envelope. So
// the card is not a richer rendering chosen over plain text, it is the common
// denominator, and there is no per-URL-kind branch to keep in step as a result.
//
// The card itself carries no more than the Slack message does: the event's name,
// the organization it happened in, the fields notifications.Summarize kept, and the
// link to the full record.
type message struct {
	Type        string       `json:"type"`
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	ContentType string       `json:"contentType"`
	Content     adaptiveCard `json:"content"`
}

type adaptiveCard struct {
	Type    string      `json:"type"`
	Schema  string      `json:"$schema"`
	Version string      `json:"version"`
	Body    []textBlock `json:"body"`
}

type textBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Wrap     bool   `json:"wrap"`
	Weight   string `json:"weight,omitempty"`
	Spacing  string `json:"spacing,omitempty"`
	IsSubtle bool   `json:"isSubtle,omitempty"`
}

const (
	messageType      = "message"
	adaptiveCardType = "application/vnd.microsoft.card.adaptive"
	cardType         = "AdaptiveCard"
	cardSchema       = "http://adaptivecards.io/schemas/adaptive-card.json"
	// cardVersion is the schema version the card declares. 1.4 is the highest the
	// Teams renderer supports across the desktop, web and mobile clients, and this
	// card uses nothing newer than 1.0 anyway — declaring a version Teams does not
	// know makes it refuse the card rather than degrade it.
	cardVersion   = "1.4"
	textBlockType = "TextBlock"
	weightBolder  = "Bolder"
	// spacingNone puts the organization's name directly under the event's name, so
	// the two read as one heading rather than two paragraphs.
	spacingNone = "None"
	// testMessageTitle is the one piece of copy this package holds, and it is
	// English in every deployment — the same choice, for the same reason, as the
	// Slack channel's. The notification copy this codebase does translate is the
	// mail catalogue, keyed by audit action; a test notification is not an audit
	// action, and its only reader is the admin who pressed the button a moment
	// earlier to see whether anything arrives at all.
	testMessageTitle = "Test notification"
)

// eventMessage renders one dispatched event. The event's name comes from the mail
// catalogue, so a Teams card, a Slack message and a notification mail call the same
// event the same thing; an action the catalogue has no name for is an error rather
// than a card that names the raw audit action.
func eventMessage(e notifications.Event, orgName, auditURL string, locale email.Locale) (message, error) {
	name, ok := email.EventLabel(e.Action, locale)
	if !ok {
		return message{}, fmt.Errorf("teamschannel: no %s name for event %q", locale, e.Action)
	}
	return card(name, orgName, notifications.Summarize(e.Metadata), auditURL), nil
}

// testMessage is the specimen a "send test notification" posts. It is the heading
// of a real notification and nothing else — enough to prove the webhook reaches
// the channel, with no event to describe and no record to link to.
func testMessage(orgName string) message {
	return card(testMessageTitle, orgName, "", "")
}

// card assembles the notification. details and auditURL are left out when empty,
// which is what keeps a test notification down to its heading and an event with no
// summarizable metadata down to two lines.
func card(name, orgName, details, auditURL string) message {
	body := []textBlock{
		{Type: textBlockType, Text: escape(name), Wrap: true, Weight: weightBolder},
		{Type: textBlockType, Text: escape(orgName), Wrap: true, Spacing: spacingNone, IsSubtle: true},
	}
	// One block per summarized field, rather than notifications.Summarize's own
	// newline-joined string in a single block. A TextBlock renders Markdown, where a
	// lone "\n" is not a line break — the fields would arrive run together on one
	// line, which is what the e-mail and Slack channels get for free and this one has
	// to build. Letting the card's structure carry the line structure is also why no
	// escape has to reach for a break: the same reason the values sit in JSON fields.
	for i, line := range strings.Split(details, "\n") {
		if line == "" {
			continue
		}
		block := textBlock{Type: textBlockType, Text: escape(line), Wrap: true}
		if i > 0 {
			// The fields are one list, not a paragraph each.
			block.Spacing = spacingNone
		}
		body = append(body, block)
	}
	if auditURL != "" {
		// Posted bare, so Teams auto-links it: a labelled [link](url) would need a word
		// of copy per locale for what the URL already says. It is not escaped either,
		// because it is built here from the deployment's own base URL and an org slug
		// that is [a-z0-9-] by organization.ValidateSlug — there is no metacharacter in
		// it to neutralise, and a backslash would break the URL rather than protect it.
		body = append(body, textBlock{Type: textBlockType, Text: auditURL, Wrap: true})
	}
	return message{
		Type: messageType,
		Attachments: []attachment{{
			ContentType: adaptiveCardType,
			Content: adaptiveCard{
				Type:    cardType,
				Schema:  cardSchema,
				Version: cardVersion,
				Body:    body,
			},
		}},
	}
}

// escape neutralises the Markdown link construct in a value the card interpolates
// (an organization's name, a metadata field). A TextBlock's text is rendered as a
// small Markdown subset, so "[click here](https://elsewhere.example)" in a member's
// name would arrive as a clickable link the reader has every reason to trust — a
// notification from their own wallet naming their own organization. Escaping the
// brackets is what keeps a value a value.
//
// The card's *structure* needs no escaping: unlike Slack's single text field, it is
// built as JSON and encoding/json is what writes it, so a value cannot become part
// of the card. This is only about what a value renders as.
//
// Deliberately narrow. The emphasis constructs (*, _, `) are left live: they can
// only change how a value looks, and escaping them would put a visible backslash
// into every address like sam_smith@example.org on a client that does not honour
// the escape. Brackets are rare enough in the values here that the same trade goes
// the other way. The backslash itself is escaped first, so a value cannot smuggle
// a bracket back by escaping the escape.
func escape(value string) string {
	var b []byte
	for i := range len(value) {
		switch c := value[i]; c {
		case '\\', '[', ']':
			b = append(b, '\\', c)
		default:
			b = append(b, c)
		}
	}
	return string(b)
}
