package slackchannel

import "testing"

func ptr(s string) *string { return &s }

// A save can only leave the row in a state the settings screen can render back.
// Delivery on with no webhook is the one it cannot: GET would answer enabled with
// hasWebhook false, which the screen shows as switched off.
func TestNextStateClampsDeliveryWithoutAWebhook(t *testing.T) {
	const webhook = "https://hooks.slack.com/services/T000/B000/xxxxx"
	cases := []struct {
		name          string
		before        state
		in            SettingsInput
		setWebhook    bool
		hasNewWebhook bool
		want          state
	}{
		{
			name: "enabling with nothing stored is stored as off",
			in:   SettingsInput{Enabled: true},
			want: state{hasWebhook: false, enabled: false},
		},
		{
			name:          "clearing the webhook switches delivery off with it",
			before:        state{hasWebhook: true, enabled: true},
			in:            SettingsInput{WebhookURL: ptr(""), Enabled: true},
			setWebhook:    true,
			hasNewWebhook: false,
			want:          state{hasWebhook: false, enabled: false},
		},
		{
			name:          "a first webhook can be enabled in the same save",
			in:            SettingsInput{WebhookURL: ptr(webhook), Enabled: true},
			setWebhook:    true,
			hasNewWebhook: true,
			want:          state{hasWebhook: true, enabled: true},
		},
		{
			name:   "delivery is switched off without re-pasting the url",
			before: state{hasWebhook: true, enabled: true},
			in:     SettingsInput{Enabled: false},
			want:   state{hasWebhook: true, enabled: false},
		},
		{
			name:   "the stored webhook is kept when the body omits it",
			before: state{hasWebhook: true, enabled: false},
			in:     SettingsInput{Enabled: true},
			want:   state{hasWebhook: true, enabled: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextState(tc.before, tc.in, tc.setWebhook, tc.hasNewWebhook)
			if got != tc.want {
				t.Errorf("nextState() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
