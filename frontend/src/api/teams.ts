import { z } from "zod";
import { request } from "./http";

// Per-organization Microsoft Teams webhook the notification channel posts events
// to. The webhook URL is write-only: it is never returned, only whether one is
// stored (`hasWebhook`).
export const teamsSettingsSchema = z.object({
  configured: z.boolean(),
  hasWebhook: z.boolean(),
  enabled: z.boolean(),
  updatedAt: z.string().optional(),
});

export type TeamsSettings = z.infer<typeof teamsSettingsSchema>;

export interface TeamsSettingsInput {
  // null keeps the stored webhook URL, a URL replaces it, "" clears it.
  webhookUrl: string | null;
  enabled: boolean;
}

// The path segment is the channel id the subscription document uses, so the two
// name the channel the same way.
function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/msteams`;
}

export function getTeamsSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<TeamsSettings> {
  return request(`${base(slug)}/settings`, {
    schema: teamsSettingsSchema,
    signal,
  });
}

export function updateTeamsSettings(
  slug: string,
  input: TeamsSettingsInput,
  signal?: AbortSignal,
): Promise<TeamsSettings> {
  return request(`${base(slug)}/settings`, {
    schema: teamsSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

// Posts a specimen card to the stored webhook so an admin can see whether it
// arrives before relying on it.
export function sendTeamsTestNotification(
  slug: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/test`, {
    schema: z.void(),
    method: "POST",
    signal,
  });
}
