import { NOTIFICATION_CHANNELS } from "../api/notifications";
import type { ChannelId } from "../api/notifications";

// An event's subscribed channels, keyed by audit action id. An event that is
// absent notifies nobody.
export type Subscriptions = Record<string, ChannelId[]>;

// A stable, order-independent fingerprint of a subscription document, used to
// tell whether the admin's edits differ from what is stored. Events and channels
// are both sorted so the comparison ignores ordering the backend may
// re-canonicalize.
export function fingerprint(subs: Subscriptions): string {
  const events = Object.keys(subs)
    .filter((event) => subs[event].length > 0)
    .sort();
  return JSON.stringify(
    events.map((event) => [event, [...subs[event]].sort()]),
  );
}

// Whether two subscription documents differ, ignoring ordering.
export function subscriptionsDiffer(
  a: Subscriptions,
  b: Subscriptions,
): boolean {
  return fingerprint(a) !== fingerprint(b);
}

// Add or remove a channel for one event, returning a new document. Channels are
// stored in the canonical NOTIFICATION_CHANNELS order so the fingerprint is
// stable, and rebuilding through that list preserves an org's saved subscription
// to a not-yet-offered channel (e.g. msteams) through a full-replacement save.
export function toggleChannel(
  subs: Subscriptions,
  event: string,
  channel: ChannelId,
): Subscriptions {
  const current = new Set(subs[event] ?? []);
  if (current.has(channel)) current.delete(channel);
  else current.add(channel);
  const next = { ...subs };
  if (current.size === 0) {
    delete next[event];
  } else {
    next[event] = NOTIFICATION_CHANNELS.filter((c) => current.has(c));
  }
  return next;
}
