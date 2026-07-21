import { z } from "zod";
import { absoluteApiUrl, request } from "./http";

// Per-organization Veramo issuer instance settings. The org's attestations issue
// from this instance (the {instance} path segment at the hosted issuer); the
// display name / logo become the issuer's wallet-facing branding. No secret here
// — the hosted issuer's admin token is deployment-global. logoUri is the API path
// serving the uploaded logo for the admin preview ("" when none); the generated
// bundle embeds the logo as a self-contained data: URI for wallets.
export const issuerSettingsSchema = z.object({
  configured: z.boolean(),
  instanceName: z.string(),
  displayName: z.string(),
  logoUri: z.string(),
  enabled: z.boolean(),
  updatedAt: z.string().optional(),
});

export type IssuerSettings = z.infer<typeof issuerSettingsSchema>;

// The logo change to apply when saving: a File uploads a new logo, "remove"
// clears the current one, and "keep" leaves it untouched (so the other fields
// can be changed on their own).
export type LogoChange = File | "keep" | "remove";

export interface IssuerSettingsInput {
  instanceName: string;
  displayName: string;
  enabled: boolean;
  logo: LogoChange;
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

// The backend returns the logo as a path on the API; make it absolute so an
// <img> loads it from the API origin even when the SPA is served elsewhere.
function withAbsoluteLogo(settings: IssuerSettings): IssuerSettings {
  return settings.logoUri
    ? { ...settings, logoUri: absoluteApiUrl(settings.logoUri) }
    : settings;
}

export async function getIssuerSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<IssuerSettings> {
  const settings = await request(`${base(slug)}/issuer/settings`, {
    schema: issuerSettingsSchema,
    signal,
  });
  return withAbsoluteLogo(settings);
}

export async function updateIssuerSettings(
  slug: string,
  input: IssuerSettingsInput,
  signal?: AbortSignal,
): Promise<IssuerSettings> {
  const form = new FormData();
  form.append("instanceName", input.instanceName);
  form.append("displayName", input.displayName);
  form.append("enabled", String(input.enabled));
  if (input.logo instanceof File) {
    form.append("logo", input.logo);
  } else if (input.logo === "remove") {
    form.append("removeLogo", "true");
  }
  const settings = await request(`${base(slug)}/issuer/settings`, {
    schema: issuerSettingsSchema,
    method: "PUT",
    body: form,
    signal,
  });
  return withAbsoluteLogo(settings);
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
