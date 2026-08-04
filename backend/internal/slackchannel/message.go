package slackchannel

import (
	"fmt"
	"strings"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
)

// message is an incoming-webhook payload. Only the text field is sent: a webhook
// posts to the one channel it was created for, so there is no addressing to do,
// and Block Kit would buy layout this message does not need — a bold heading, the
// fields that changed, and a link to the full record.
type message struct {
	Text string `json:"text"`
}

const (
	// titleSeparator sits between the event name and the organization it happened
	// in. A symbol rather than a word, because that line then carries no copy to
	// translate: the event name comes from the mail catalogue, already localized.
	titleSeparator = " · "
	// testMessageTitle is the one piece of copy this package holds, and it is
	// English in every deployment. The notification copy this codebase does
	// translate is the mail catalogue, keyed by audit action — a test notification
	// is not an audit action, and its only reader is the admin who pressed the
	// button a moment earlier to see whether anything arrives at all.
	testMessageTitle = "Test notification"
)

// eventMessage renders one dispatched event. The event's name comes from the mail
// catalogue, so a channel post and a notification mail call the same event the
// same thing; an action the catalogue has no name for is an error rather than a
// message that names the raw audit action.
func eventMessage(e notifications.Event, orgName, auditURL string, locale email.Locale) (message, error) {
	name, ok := email.EventLabel(e.Action, locale)
	if !ok {
		return message{}, fmt.Errorf("slackchannel: no %s name for event %q", locale, e.Action)
	}
	lines := []string{title(name, orgName)}
	if details := notifications.Summarize(e.Metadata); details != "" {
		lines = append(lines, escape(details))
	}
	// The link is posted bare so Slack renders it as one: a labelled <url|link>
	// would need a word of copy per locale for what the URL already says.
	lines = append(lines, escape(auditURL))
	return message{Text: strings.Join(lines, "\n")}, nil
}

// testMessage is the specimen a "send test notification" posts. It is the heading
// of a real notification and nothing else — enough to prove the webhook reaches
// the channel, with no event to describe.
func testMessage(orgName string) message {
	return message{Text: title(testMessageTitle, orgName)}
}

func title(name, orgName string) string {
	return "*" + escape(name) + "*" + titleSeparator + escape(orgName)
}

// escape makes a value safe to place in Slack's mrkdwn: &, < and > are Slack's own
// control characters (a raw <...> is read as a link), so an organization name or a
// metadata value carrying them would otherwise change the message's structure
// rather than appear in it. Slack asks for exactly these three, in this order.
func escape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	return strings.ReplaceAll(value, ">", "&gt;")
}
