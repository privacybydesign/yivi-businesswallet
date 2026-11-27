package providerlisting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/slackchannel"
)

const alertSubject = "Yivi Business Wallet: trusted list of wallet providers"

type platformMailer interface {
	SendPlatformText(recipients []string, subject, text string) error
}

// PlatformAlerter alerts us through our own Slack channel and our own mail to the
// platform admins, each only when the deployment has it.
type PlatformAlerter struct {
	slackWebhook string
	mail         platformMailer
	recipients   []string
	// origin names the deployment, so staging and production alerts differ.
	origin string
}

// NewPlatformAlerter builds the alerter. slackWebhook must have passed
// slackchannel.NormalizeWebhookURL; empty posts nothing.
func NewPlatformAlerter(slackWebhook string, mail platformMailer, recipients []string, origin string) *PlatformAlerter {
	return &PlatformAlerter{slackWebhook: slackWebhook, mail: mail, recipients: recipients, origin: origin}
}

// Alert fails only when no channel the deployment has took it; a channel that
// failed while another took it is logged, not retried, so nobody gets it twice.
// With no channel at all the log line from the watcher is the alert.
func (a *PlatformAlerter) Alert(ctx context.Context, text string) error {
	text = a.origin + ": " + text
	var channels int
	var failed []error
	if a.slackWebhook != "" {
		channels++
		if err := slackchannel.PostText(ctx, a.slackWebhook, text); err != nil {
			failed = append(failed, fmt.Errorf("slack: %w", err))
		}
	}
	if len(a.recipients) > 0 {
		err := a.mail.SendPlatformText(a.recipients, alertSubject, text)
		if !errors.Is(err, email.ErrNotConfigured) {
			channels++
			if err != nil {
				failed = append(failed, fmt.Errorf("mail: %w", err))
			}
		}
	}
	if len(failed) > 0 && len(failed) == channels {
		return errors.Join(failed...)
	}
	for _, err := range failed {
		slog.ErrorContext(ctx, "providerlisting: alert channel failed", slog.Any("error", err))
	}
	return nil
}
