package notifications

import (
	"reflect"
	"testing"
)

func TestCatalogHasNoDuplicatesAndAGroupPerEntry(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalog() {
		if seen[e.Event] {
			t.Errorf("event %q appears twice in the catalog", e.Event)
		}
		seen[e.Event] = true
		if e.Group == "" {
			t.Errorf("event %q has no group", e.Event)
		}
	}
	if len(seen) == 0 {
		t.Fatal("the catalog is empty")
	}
}

func TestSubscribable(t *testing.T) {
	if !Subscribable("membership.invited") {
		t.Error("membership.invited is in the catalog but reported as not subscribable")
	}
	// Settings changes are deliberately outside the catalog.
	if Subscribable("theme.settings_updated") {
		t.Error("theme.settings_updated is not in the catalog but reported as subscribable")
	}
	if Subscribable("nonsense.made_up") {
		t.Error("an unknown action reported as subscribable")
	}
}

func TestNormalizeCanonicalisesChannels(t *testing.T) {
	got, err := Normalize(map[string][]ChannelID{
		// Out of order and with a duplicate: both are canonicalised away.
		"membership.invited": {ChannelSlack, ChannelEmail, ChannelSlack},
		"attestation.issued": {ChannelTeams},
		// An event nobody is subscribed to is dropped rather than stored empty.
		"qerds.message_sent": {},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := map[string][]ChannelID{
		"membership.invited": {ChannelEmail, ChannelSlack},
		"attestation.issued": {ChannelTeams},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize = %v, want %v", got, want)
	}
}

func TestNormalizeAcceptsAnEmptyDocument(t *testing.T) {
	got, err := Normalize(nil)
	if err != nil {
		t.Fatalf("Normalize(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Normalize(nil) = %v, want an empty map", got)
	}
}

func TestNormalizeRejectsUnknownEventOrChannel(t *testing.T) {
	cases := map[string]map[string][]ChannelID{
		"unknown event":        {"nonsense.made_up": {ChannelEmail}},
		"unsubscribable event": {"theme.settings_updated": {ChannelEmail}},
		"unknown channel":      {"membership.invited": {"carrier-pigeon"}},
	}
	for name, subs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Normalize(subs); err == nil {
				t.Errorf("Normalize(%v) = nil, want an error", subs)
			}
		})
	}
}

func TestAuditSnapshotSortsChannelNames(t *testing.T) {
	got := auditSnapshot(map[string][]ChannelID{
		"membership.invited": {ChannelSlack, ChannelEmail},
	})
	want := map[string]any{"membership.invited": []string{"email", "slack"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("auditSnapshot = %v, want %v", got, want)
	}
}

func TestChannelsForReturnsNothingWhenUnsubscribed(t *testing.T) {
	s := Settings{Subscriptions: map[string][]ChannelID{"membership.invited": {ChannelEmail}}}
	if got := s.ChannelsFor("membership.revoked"); len(got) != 0 {
		t.Errorf("ChannelsFor(unsubscribed) = %v, want nothing", got)
	}
	if got := s.ChannelsFor("membership.invited"); !reflect.DeepEqual(got, []ChannelID{ChannelEmail}) {
		t.Errorf("ChannelsFor = %v, want [email]", got)
	}
}
