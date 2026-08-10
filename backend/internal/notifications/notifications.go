// Package notifications is the org-scoped subscription and dispatch layer on top
// of the audit-event catalog: an org admin picks, per audit event, which channels
// should be told about it, and this package makes sure they are.
//
// The moving parts:
//
//   - the catalog (below) — the curated subset of audit actions an org may
//     subscribe to, grouped as the settings screen shows them;
//   - Store — the per-org subscriptions, saved like every other settings slice
//     (Settings/SettingsInput, org-admin-gated Handler, audited on change);
//   - Recorder — an audit.Recorder decorator that enqueues a subscribable event
//     on the notification outbox in the same transaction as the event itself;
//   - Dispatcher — claims outbox rows out of band and fans them out, a few events
//     at a time, to the channels the org subscribed to.
//
// Going through the outbox rather than notifying inline is what satisfies the two
// hard rules: a notification is only ever sent for an action that committed (a
// rolled back transaction takes its outbox row with it), and a channel that fails
// or hangs cannot block another channel or roll back the action that caused it.
//
// Channels themselves live outside this package — e-mail, Slack and MS Teams each
// implement Channel and are registered on the Dispatcher at startup. Delivery is
// at most once: a claimed event whose channel call fails is logged, not retried.
package notifications

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

// ChannelID identifies one delivery channel. The ids are fixed here rather than
// derived from whatever happens to be registered, so an org's saved subscriptions
// survive a deployment that has not enabled a channel yet: the Dispatcher skips
// an unregistered channel and keeps the preference.
type ChannelID string

const (
	ChannelEmail ChannelID = "email"
	ChannelSlack ChannelID = "slack"
	ChannelTeams ChannelID = "msteams"
)

// channelIDs is every known channel, in the order the settings screen lists them.
var channelIDs = []ChannelID{ChannelEmail, ChannelSlack, ChannelTeams}

// Channels returns the known channel ids.
func Channels() []ChannelID {
	out := make([]ChannelID, len(channelIDs))
	copy(out, channelIDs)
	return out
}

// KnownChannel reports whether id is one of the known channels.
func KnownChannel(id ChannelID) bool {
	for _, c := range channelIDs {
		if c == id {
			return true
		}
	}
	return false
}

// Group is the family a subscribable event belongs to. It carries no behaviour —
// it lets the settings screen show the catalog in blocks instead of one flat list
// of forty-odd checkboxes.
type Group string

const (
	GroupMembership  Group = "membership"
	GroupWallet      Group = "wallet"
	GroupQerds       Group = "qerds"
	GroupPostGuard   Group = "postguard"
	GroupAttestation Group = "attestation"
	GroupSigning     Group = "signing"
)

// CatalogEntry is one subscribable audit action.
type CatalogEntry struct {
	Event string `json:"event"`
	Group Group  `json:"group"`
}

// catalog is the subset of the audit catalog an org may subscribe to: the
// membership, wallet, QERDS, PostGuard and attestation families. The remaining
// audit actions (settings changes, the org lifecycle, identity review) are
// deliberately absent — they are administrative bookkeeping nobody asked to be
// paged about, and leaving them out keeps the settings screen readable.
//
// Adding an entry is a one-line change here, but it is a data-minimisation
// decision, not a display one: the Dispatcher hands a subscribed event's audit
// metadata to the channels unchanged, and a channel is an outside system (an SMTP
// relay, a Slack or Microsoft webhook). So an action whose metadata is more than
// the subscribing organization needs to see does not belong in this list.
//
// audit.MembershipAcceptRejected is the case that settles it and is left out on
// purpose: organization.Service records it when a disclosure does not match the
// invitation, with the legal name or e-mail the person disclosed from their
// passport or ID card as the "after" side. That is exactly the record the audit
// log exists for and exactly what must not be posted to a webhook, so the failed
// attempt stays in the audit log where an admin can read it under access control.
// audit.MembershipAccepted is in the list for the mirror-image reason: acceptance
// only reaches that record once the disclosure matched the invitation, so its
// metadata is the name the admin typed themselves.
var catalog = []CatalogEntry{
	{audit.MembershipInvited, GroupMembership},
	{audit.MembershipInviteResent, GroupMembership},
	{audit.MembershipInviteRevoked, GroupMembership},
	{audit.MembershipAccepted, GroupMembership},
	{audit.MembershipDeclined, GroupMembership},
	{audit.MembershipRevoked, GroupMembership},
	{audit.MembershipRoleChanged, GroupMembership},
	{audit.MembershipExpired, GroupMembership},

	{audit.WalletBootstrapped, GroupWallet},
	{audit.WalletSuspended, GroupWallet},
	{audit.WalletRevoked, GroupWallet},
	{audit.RepresentationClaimed, GroupWallet},
	{audit.RepresentationRevoked, GroupWallet},

	{audit.QerdsMessageSent, GroupQerds},
	{audit.QerdsMessageReceived, GroupQerds},
	{audit.QerdsAddressProvisioned, GroupQerds},
	{audit.QerdsAddressDefaultChanged, GroupQerds},
	{audit.QerdsContactAdded, GroupQerds},
	{audit.QerdsContactDeleted, GroupQerds},

	{audit.PostGuardFileSent, GroupPostGuard},
	{audit.PostGuardKeySet, GroupPostGuard},
	{audit.PostGuardKeyRemoved, GroupPostGuard},
	{audit.PostGuardEncryptionKeySet, GroupPostGuard},
	{audit.PostGuardEncryptionKeyRemoved, GroupPostGuard},

	{audit.AttestationIssued, GroupAttestation},
	{audit.AttestationClaimed, GroupAttestation},
	{audit.AttestationRevoked, GroupAttestation},
	{audit.AttestationOfferCancelled, GroupAttestation},
	{audit.AttestationKeyAdded, GroupAttestation},
	{audit.AttestationKeySuspended, GroupAttestation},
	{audit.AttestationKeyRevoked, GroupAttestation},

	// Signing lifecycle. Safe to publish: the metadata these actions record is the
	// org's own document filename, the signing mode, signer counts, and redaction-safe
	// status/error strings (see internal/signing/store.go) — never the disclosed legal
	// identity that keeps signing.signed / accept_rejected out of this list.
	{audit.SigningRequested, GroupSigning},
	{audit.SigningCompleted, GroupSigning},
	{audit.SigningFailed, GroupSigning},
}

// subscribable indexes the catalog for the per-event lookup on the write path.
var subscribable = func() map[string]bool {
	m := make(map[string]bool, len(catalog))
	for _, e := range catalog {
		m[e.Event] = true
	}
	return m
}()

// Catalog returns the subscribable events, grouped in display order.
func Catalog() []CatalogEntry {
	out := make([]CatalogEntry, len(catalog))
	copy(out, catalog)
	return out
}

// Subscribable reports whether an audit action can be subscribed to. The Recorder
// checks it before enqueuing, so an event nobody could ever subscribe to never
// reaches the outbox.
func Subscribable(action string) bool { return subscribable[action] }

// Settings is an org's notification subscriptions. Configured is false when the
// org has never saved any, in which case Subscriptions is empty and nothing is
// notified. Subscriptions maps an audit action to the channels to notify; an
// action that is absent notifies nobody.
type Settings struct {
	Configured    bool                   `json:"configured"`
	Subscriptions map[string][]ChannelID `json:"subscriptions"`
	UpdatedAt     *time.Time             `json:"updatedAt,omitempty"`
}

// ChannelsFor returns the channels subscribed to an action, in stored order.
func (s Settings) ChannelsFor(action string) []ChannelID {
	return s.Subscriptions[action]
}

// SettingsInput is a full replacement of an org's subscriptions: what it does not
// mention is unsubscribed. Callers pass it through Normalize first.
type SettingsInput struct {
	Subscriptions map[string][]ChannelID
}

// Normalize validates a subscription document and returns a canonical copy:
// unknown events and unknown channels are rejected, duplicate channels are
// collapsed, channels are ordered as Channels() lists them, and an event with no
// channels left is dropped rather than stored as an empty list. Storing the
// canonical form keeps a saved document comparable to the next one, so the audit
// diff of a change reads as the change the admin actually made.
func Normalize(subs map[string][]ChannelID) (map[string][]ChannelID, error) {
	out := make(map[string][]ChannelID, len(subs))
	for event, channels := range subs {
		if !Subscribable(event) {
			return nil, fmt.Errorf("unknown event %q", event)
		}
		seen := make(map[ChannelID]bool, len(channels))
		for _, c := range channels {
			if !KnownChannel(c) {
				return nil, fmt.Errorf("unknown channel %q for event %q", c, event)
			}
			seen[c] = true
		}
		if len(seen) == 0 {
			continue
		}
		ordered := make([]ChannelID, 0, len(seen))
		for _, c := range channelIDs {
			if seen[c] {
				ordered = append(ordered, c)
			}
		}
		out[event] = ordered
	}
	return out, nil
}

// auditSnapshot renders subscriptions as audit metadata: a sorted event ->
// channel-name list map, so the {before, after} envelope diffs readably.
func auditSnapshot(subs map[string][]ChannelID) map[string]any {
	out := make(map[string]any, len(subs))
	for event, channels := range subs {
		names := make([]string, 0, len(channels))
		for _, c := range channels {
			names = append(names, string(c))
		}
		sort.Strings(names)
		out[event] = names
	}
	return out
}

// Event is one recorded audit event on its way to the channels: the outbox row
// the Recorder enqueued, as the Dispatcher hands it to a Channel.
//
// Metadata is the audit metadata of the action verbatim — there is no per-channel
// filtering seam, so what a channel may see is decided by what the catalog admits
// (see catalog). A Channel must not modify it; see Channel.Notify.
type Event struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ActorUserID *uuid.UUID
	Action      string
	TargetType  string
	TargetID    string
	Metadata    map[string]any
	OccurredAt  time.Time
}
