import { z } from "zod";
import { request } from "./http";

// Per-organization Veramo issuer instance settings. The org's attestations issue
// from this instance (the {instance} path segment at the hosted issuer); the
// display name / logo become the issuer's wallet-facing branding. No secret here
// — the hosted issuer's admin token is deployment-global.
export const issuerSettingsSchema = z.object({
  configured: z.boolean(),
  instanceName: z.string(),
  displayName: z.string(),
  logoUri: z.string(),
  enabled: z.boolean(),
  updatedAt: z.string().optional(),
});

export type IssuerSettings = z.infer<typeof issuerSettingsSchema>;

export interface IssuerSettingsInput {
  instanceName: string;
  displayName: string;
  logoUri: string;
  enabled: boolean;
}

// The generated GitOps bundle for an org's issuer instance: the issuer
// registration, its did:web key, the issuer metadata (with every schema's
// localized display) and one VCT document per schema. Committed to the issuer
// ops repo (openid4vc-poc-ops). Inner documents are opaque here — rendered
// verbatim for the operator to copy.
export const issuerBundleSchema = z.object({
  instance: z.string(),
  issuer: z.unknown(),
  did: z.unknown(),
  metadata: z.unknown(),
  vcts: z.array(z.object({ name: z.string(), document: z.unknown() })),
});

export type IssuerBundle = z.infer<typeof issuerBundleSchema>;

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}`;
}

export function getIssuerSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<IssuerSettings> {
  return request(`${base(slug)}/issuer/settings`, {
    schema: issuerSettingsSchema,
    signal,
  });
}

export function updateIssuerSettings(
  slug: string,
  input: IssuerSettingsInput,
  signal?: AbortSignal,
): Promise<IssuerSettings> {
  return request(`${base(slug)}/issuer/settings`, {
    schema: issuerSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

export function getIssuerBundle(
  slug: string,
  signal?: AbortSignal,
): Promise<IssuerBundle> {
  return request(`${base(slug)}/attestations/issuer-bundle`, {
    schema: issuerBundleSchema,
    signal,
  });
}
