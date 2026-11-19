import { z } from "zod";
import { request } from "./http";

// Per-organization settings for the CSC API v2 signing provider the business
// wallet drives to sign documents: the wallet's own built-in signing service
// (the default, where the signer identifies with the Yivi app), or a remote
// QTSP. A remote provider's client secret is write-only: it is never returned,
// only whether one is stored (`hasClientSecret`).
//
// `providerKind` is a plain string, not a zod enum, so a new backend kind needs no
// frontend change. `providerKinds` lists the kinds this deployment offers plus the
// base URL to pre-fill when each is chosen.

// The built-in signing service. It has nothing to configure: the backend
// stores no base URL, client id or client secret for it.
export const CSC_PROVIDER_KIND_BUILT_IN = "yivi";

export const cscProviderKindSchema = z.object({
  id: z.string(),
  defaultBaseUrl: z.string(),
});

export type CscProviderKind = z.infer<typeof cscProviderKindSchema>;

export const cscSettingsSchema = z.object({
  configured: z.boolean(),
  enabled: z.boolean(),
  providerKind: z.string(),
  baseUrl: z.string(),
  clientId: z.string(),
  hasClientSecret: z.boolean(),
  updatedAt: z.string().optional(),
  providerKinds: z.array(cscProviderKindSchema),
});

export type CscSettings = z.infer<typeof cscSettingsSchema>;

// The provider's own /info answer, shown after a successful connection test.
export const cscInfoSchema = z.object({
  name: z.string(),
  specs: z.string(),
});

export type CscInfo = z.infer<typeof cscInfoSchema>;

export interface CscSettingsInput {
  enabled: boolean;
  providerKind: string;
  baseUrl: string;
  clientId: string;
  // null keeps the stored client secret, a value replaces it, "" clears it.
  // Left out for the built-in service, which has none.
  clientSecret?: string | null;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/csc`;
}

export function getCscSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<CscSettings> {
  return request(`${base(slug)}/settings`, {
    schema: cscSettingsSchema,
    signal,
  });
}

export function updateCscSettings(
  slug: string,
  input: CscSettingsInput,
  signal?: AbortSignal,
): Promise<CscSettings> {
  return request(`${base(slug)}/settings`, {
    schema: cscSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

// Probes the stored endpoint's unauthenticated /csc/v2/info so an admin can see
// the connection reaches the provider they meant before relying on it.
export function testCscConnection(
  slug: string,
  signal?: AbortSignal,
): Promise<CscInfo> {
  return request(`${base(slug)}/test`, {
    schema: cscInfoSchema,
    method: "POST",
    signal,
  });
}
