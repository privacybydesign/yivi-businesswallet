import { z } from "zod";
import { request } from "./http";

// Per-organization SMTP configuration used to deliver credential offers and
// notifications by e-mail. The password is write-only: it is never returned,
// only whether one is stored (`hasPassword`).
export const emailSettingsSchema = z.object({
  configured: z.boolean(),
  host: z.string(),
  port: z.number(),
  username: z.string(),
  fromName: z.string(),
  fromAddress: z.string(),
  enabled: z.boolean(),
  hasPassword: z.boolean(),
  updatedAt: z.string().optional(),
});

export type EmailSettings = z.infer<typeof emailSettingsSchema>;

export interface EmailSettingsInput {
  host: string;
  port: number;
  username: string;
  // null keeps the stored password, a non-empty string sets it, "" clears it.
  password: string | null;
  fromName: string;
  fromAddress: string;
  enabled: boolean;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/email`;
}

export function getEmailSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<EmailSettings> {
  return request(`${base(slug)}/settings`, {
    schema: emailSettingsSchema,
    signal,
  });
}

export function updateEmailSettings(
  slug: string,
  input: EmailSettingsInput,
  signal?: AbortSignal,
): Promise<EmailSettings> {
  return request(`${base(slug)}/settings`, {
    schema: emailSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

export function sendTestEmail(
  slug: string,
  input: { to: string },
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/test`, {
    schema: z.void(),
    method: "POST",
    body: input,
    signal,
  });
}
