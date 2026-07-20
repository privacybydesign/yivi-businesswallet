import { z } from "zod";
import { request } from "./http";

// The organization's WSCA holder-wallet account. Its holder-binding keys live in
// the wallet-provider HSM under SECDSA (no private key in the backend). accountId
// is the stable per-org reference; certificateId rotates on each rotation. No
// secret is ever returned — it is sealed server-side.
export const wscaAccountSchema = z.object({
  organizationId: z.string(),
  accountId: z.string(),
  certificateId: z.string(),
  activatedAt: z.string(),
  rotatedAt: z.string().optional(),
});

export type WscaAccount = z.infer<typeof wscaAccountSchema>;

// configured: WSCA-backed holder binding is enabled on this deployment.
// activated: this organization has activated its wallet (account present).
export const wscaStatusSchema = z.object({
  configured: z.boolean(),
  activated: z.boolean(),
  account: wscaAccountSchema.optional(),
});

export type WscaStatus = z.infer<typeof wscaStatusSchema>;

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/wsca`;
}

export function getWscaStatus(
  slug: string,
  signal?: AbortSignal,
): Promise<WscaStatus> {
  return request(base(slug), { schema: wscaStatusSchema, signal });
}

export function activateWsca(
  slug: string,
  secret: string,
  signal?: AbortSignal,
): Promise<WscaAccount> {
  return request(`${base(slug)}/activate`, {
    schema: wscaAccountSchema,
    method: "POST",
    body: { secret },
    signal,
  });
}

export function rotateWsca(
  slug: string,
  currentSecret: string,
  newSecret: string,
  signal?: AbortSignal,
): Promise<WscaAccount> {
  return request(`${base(slug)}/rotate`, {
    schema: wscaAccountSchema,
    method: "POST",
    body: { currentSecret, newSecret },
    signal,
  });
}
