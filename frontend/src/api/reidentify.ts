import { z } from "zod";
import { request } from "./http";

// The member-side re-identification flow (#240): a bearer token from a reminder
// e-mail, an admin's request, or the in-app banner, exchanged for the same
// identity disclosure invite-accept uses.

// The session-start URL the IdentityDisclosure component POSTs to begin the
// OpenID4VP presentation for this token.
export function reidentifySessionUrl(token: string): string {
  return `/api/v1/reidentify/${encodeURIComponent(token)}/session`;
}

export const reidentifyPreviewSchema = z.object({
  organizationName: z.string(),
  organizationSlug: z.string(),
  email: z.string(),
});

export type ReidentifyPreview = z.infer<typeof reidentifyPreviewSchema>;

export const reidentifyResultSchema = z.object({
  organizationName: z.string(),
  organizationSlug: z.string(),
});

export type ReidentifyResult = z.infer<typeof reidentifyResultSchema>;

export function getReidentifyPreview(
  token: string,
  signal?: AbortSignal,
): Promise<ReidentifyPreview> {
  return request(`/api/v1/reidentify/${encodeURIComponent(token)}`, {
    schema: reidentifyPreviewSchema,
    signal,
  });
}

export function completeReidentify(
  token: string,
  disclosureToken: string,
  signal?: AbortSignal,
): Promise<ReidentifyResult> {
  return request(`/api/v1/reidentify/${encodeURIComponent(token)}/complete`, {
    schema: reidentifyResultSchema,
    method: "POST",
    body: { disclosureToken },
    signal,
  });
}
