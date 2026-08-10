import { z } from "zod";
import { request } from "./http";

// The delivery channels the backend knows about, in the order the settings
// screen lists them.
export const NOTIFICATION_CHANNELS = ["email", "slack", "msteams"] as const;
export const channelIdSchema = z.enum(NOTIFICATION_CHANNELS);
export type ChannelId = z.infer<typeof channelIdSchema>;

// The channels the settings screen renders a column for: those with a working
// backend channel today. That is currently all of them, but it stays a separate
// subset of NOTIFICATION_CHANNELS rather than collapsing into it — the ids exist
// so an org's saved subscription survives a deployment that has not enabled a
// channel (see notifications.ChannelID in the backend), and the next reserved id
// needs a column withheld without its subscriptions being dropped.
export const SUPPORTED_CHANNELS = [
  "email",
  "slack",
  "msteams",
] as const satisfies readonly ChannelId[];

// The families the subscribable events are grouped into, so the screen renders
// blocks instead of one flat list. Mirrors notifications.Group in the backend.
export const NOTIFICATION_GROUPS = [
  "membership",
  "wallet",
  "qerds",
  "postguard",
  "attestation",
  "signing",
] as const;
export const notificationGroupSchema = z.enum(NOTIFICATION_GROUPS);
export type NotificationGroup = z.infer<typeof notificationGroupSchema>;

// One subscribable audit event and the family it belongs to. The event id is an
// audit action string (e.g. "attestation.issued"); its label comes from the
// shared auditActionLabel map, so this carries no copy of its own.
export const catalogEntrySchema = z.object({
  event: z.string(),
  group: notificationGroupSchema,
});
export type CatalogEntry = z.infer<typeof catalogEntrySchema>;

// The org's saved subscriptions plus the catalog they are chosen from, so the
// screen renders from one request. `subscriptions` maps an event to the channels
// to notify; an event that is absent notifies nobody. A backend that has never
// saved settings returns it null/absent, which we normalize to an empty map.
export const notificationSettingsSchema = z.object({
  configured: z.boolean(),
  subscriptions: z
    .record(z.string(), z.array(channelIdSchema))
    .nullish()
    .transform((value) => value ?? {}),
  updatedAt: z.string().optional(),
  events: z.array(catalogEntrySchema),
  channels: z.array(channelIdSchema),
});

export type NotificationSettings = z.infer<typeof notificationSettingsSchema>;

// A full replacement of the subscription document: an event left out is
// unsubscribed. The backend re-normalizes (canonical channel order, empties
// dropped), so the exact shape here only needs to be valid, not canonical.
export interface NotificationSettingsInput {
  subscriptions: Record<string, ChannelId[]>;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/notifications`;
}

export function getNotificationSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<NotificationSettings> {
  return request(`${base(slug)}/settings`, {
    schema: notificationSettingsSchema,
    signal,
  });
}

export function updateNotificationSettings(
  slug: string,
  input: NotificationSettingsInput,
  signal?: AbortSignal,
): Promise<NotificationSettings> {
  return request(`${base(slug)}/settings`, {
    schema: notificationSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}
