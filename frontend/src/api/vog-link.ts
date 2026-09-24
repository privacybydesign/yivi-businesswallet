import { z } from "zod";
import { request } from "./http";
import {
  ownVogStateSchema,
  resolveVogOutcome,
  uploadVogResultSchema,
} from "./organization";
import type { UploadVogResult } from "./organization";

// The member-side VOG submission (#242): a bearer token from a request or
// reminder e-mail, or the dashboard banner, keyed like a credential claim or a
// re-identification link, so the member submits without signing in.

function base(token: string): string {
  return `/api/v1/vog/${encodeURIComponent(token)}`;
}

// The session-start URLs IdentityDisclosure POSTs to begin each OpenID4VP
// presentation this page can run.
export function vogIdentitySessionUrl(token: string): string {
  return `${base(token)}/identity-session`;
}

export function vogCredentialSessionUrl(token: string): string {
  return `${base(token)}/credential-session`;
}

export function vogIdentityCredentialSessionUrl(token: string): string {
  return `${base(token)}/identity-credential-session`;
}

export const vogPreviewSchema = ownVogStateSchema.extend({
  organizationName: z.string(),
  organizationSlug: z.string(),
  email: z.string(),
});

export type VogPreview = z.infer<typeof vogPreviewSchema>;

export function getVogPreview(
  token: string,
  signal?: AbortSignal,
): Promise<VogPreview> {
  return request(base(token), { schema: vogPreviewSchema, signal });
}

export function uploadVogByLink(
  token: string,
  file: File,
  signal?: AbortSignal,
): Promise<UploadVogResult> {
  return resolveVogOutcome(() => {
    const body = new FormData();
    body.append("file", file);
    return request(`${base(token)}/upload`, {
      schema: uploadVogResultSchema,
      method: "POST",
      body,
      signal,
    });
  });
}

// completeVogIdentity records the identity disclosure vogIdentitySessionUrl
// started; the backend answers 204, so there is nothing to parse.
export async function completeVogIdentity(
  token: string,
  disclosureToken: string,
  signal?: AbortSignal,
): Promise<void> {
  await request(`${base(token)}/identity-complete`, {
    schema: z.unknown(),
    method: "POST",
    body: { disclosureToken },
    signal,
  });
}

export function completeVogCredential(
  token: string,
  disclosureToken: string,
  signal?: AbortSignal,
): Promise<UploadVogResult> {
  return resolveVogOutcome(() =>
    request(`${base(token)}/credential-complete`, {
      schema: uploadVogResultSchema,
      method: "POST",
      body: { disclosureToken },
      signal,
    }),
  );
}

// completeVogIdentityCredential finishes the combined identity + pbdf.vog
// disclosure for a member who never identified: one wallet session, after
// which the identity is on file and the VOG has been matched against it.
export function completeVogIdentityCredential(
  token: string,
  disclosureToken: string,
  signal?: AbortSignal,
): Promise<UploadVogResult> {
  return resolveVogOutcome(() =>
    request(`${base(token)}/identity-credential-complete`, {
      schema: uploadVogResultSchema,
      method: "POST",
      body: { disclosureToken },
      signal,
    }),
  );
}
