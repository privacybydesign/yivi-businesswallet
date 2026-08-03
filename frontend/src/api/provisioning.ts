import { z } from "zod";
import { request } from "./http";

// Per-organization directory-provisioning configuration. The client secret is
// write-only: it is never returned, only whether one is stored
// (`hasClientSecret`) — the same posture as the SMTP password in email.ts.
//
// `source` and `sources` are plain strings rather than a closed enum: the set
// of drivers is decided by the backend (backend/internal/provisioner), so the
// screen renders whatever the response reports and a new driver needs no
// frontend change to appear.
export const provisioningSettingsSchema = z.object({
  configured: z.boolean(),
  enabled: z.boolean(),
  source: z.string(),
  tenantId: z.string(),
  clientId: z.string(),
  hasClientSecret: z.boolean(),
  // Empty means the whole directory; otherwise the group whose members sync.
  groupId: z.string(),
  adminGroupIds: z.array(z.string()),
  lastRunAt: z.string().optional(),
  lastRunStatus: z.string().optional(),
  lastRunError: z.string().optional(),
  updatedAt: z.string().optional(),
  // The sources this deployment can configure, so the screen renders its source
  // options from one request.
  sources: z.array(z.string()),
});

export type ProvisioningSettings = z.infer<typeof provisioningSettingsSchema>;

export interface ProvisioningSettingsInput {
  enabled: boolean;
  source: string;
  tenantId: string;
  clientId: string;
  // null keeps the stored secret, a non-empty string sets it, "" clears it.
  clientSecret: string | null;
  groupId: string;
  adminGroupIds: string[];
}

// One source record a sync did not act on, and why (backend Skip). The reason
// is a stable code the screen maps to copy.
export const provisioningSkipSchema = z.object({
  email: z.string(),
  reason: z.string(),
});

export type ProvisioningSkip = z.infer<typeof provisioningSkipSchema>;

// What one sync did (backend Result), returned to the admin who triggered it.
export const provisioningSyncResultSchema = z.object({
  departmentsCreated: z.number(),
  membersInvited: z.number(),
  membersUpdated: z.number(),
  membersRemoved: z.number(),
  skipped: z.array(provisioningSkipSchema),
});

export type ProvisioningSyncResult = z.infer<
  typeof provisioningSyncResultSchema
>;

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/provisioning`;
}

export function getProvisioningSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<ProvisioningSettings> {
  return request(`${base(slug)}/settings`, {
    schema: provisioningSettingsSchema,
    signal,
  });
}

export function updateProvisioningSettings(
  slug: string,
  input: ProvisioningSettingsInput,
  signal?: AbortSignal,
): Promise<ProvisioningSettings> {
  return request(`${base(slug)}/settings`, {
    schema: provisioningSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

// A run-now sync is synchronous on the backend and bounded to five minutes
// there, so the request waits a little past that bound for the 504 rather than
// the client's own 30s default cutting it off first.
const SYNC_TIMEOUT_MS = 5 * 60 * 1000 + 20_000;

export function runProvisioningSync(
  slug: string,
  signal?: AbortSignal,
): Promise<ProvisioningSyncResult> {
  return request(`${base(slug)}/sync`, {
    schema: provisioningSyncResultSchema,
    method: "POST",
    timeoutMs: SYNC_TIMEOUT_MS,
    signal,
  });
}
