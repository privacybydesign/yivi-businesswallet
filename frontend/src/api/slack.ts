import { z } from "zod";
import { request } from "./http";

// Per-organization Slack incoming webhook the notification channel posts events
// to. The webhook URL is write-only: it is never returned, only whether one is
// stored (`hasWebhook`).
export const slackSettingsSchema = z.object({
  configured: z.boolean(),
  hasWebhook: z.boolean(),
  enabled: z.boolean(),
  updatedAt: z.string().optional(),
});

export type SlackSettings = z.infer<typeof slackSettingsSchema>;

export interface SlackSettingsInput {
  // null keeps the stored webhook URL, a URL replaces it, "" clears it.
  webhookUrl: string | null;
  enabled: boolean;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/slack`;
}

export function getSlackSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<SlackSettings> {
  return request(`${base(slug)}/settings`, {
    schema: slackSettingsSchema,
    signal,
  });
}

export function updateSlackSettings(
  slug: string,
  input: SlackSettingsInput,
  signal?: AbortSignal,
): Promise<SlackSettings> {
  return request(`${base(slug)}/settings`, {
    schema: slackSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

// Posts a specimen message to the stored webhook so an admin can see whether it
// arrives before relying on it.
export function sendSlackTestNotification(
  slug: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/test`, {
    schema: z.void(),
    method: "POST",
    signal,
  });
}
