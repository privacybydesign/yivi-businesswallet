import { z } from "zod";
import { request } from "./http";

// Inbound OpenID4VP: an external verifier invoked the business wallet as the
// holder of an organization's credentials. The browser only ever carries the
// opaque transaction id the backend minted — never the verifier's client_id,
// request_uri or the fetched request object — including through the login
// redirect. See .ai/features/openid4vp-inbound.md.

export const OPENID4VP_STATUSES = [
  "pending_auth",
  "org_selected",
  "completed",
  "denied",
  "expired",
] as const;
export type OpenID4VPStatus = (typeof OPENID4VP_STATUSES)[number];

// The invocation as the verifier's redirect delivered it: the query parameters
// of GET /openid4vp, forwarded verbatim for the backend to validate.
export interface OpenID4VPInvocation {
  clientId: string;
  requestUri?: string;
  requestUriMethod?: string;
  request?: string;
}

const startSchema = z.object({ id: z.string() });

export function startOpenID4VPTransaction(
  invocation: OpenID4VPInvocation,
  signal?: AbortSignal,
): Promise<string> {
  return request("/api/v1/openid4vp/start", {
    schema: startSchema,
    method: "POST",
    body: invocation,
    signal,
  }).then((r) => r.id);
}

export const openid4vpStatusSchema = z.object({
  status: z.enum(OPENID4VP_STATUSES),
  verifier: z.string(),
});
export type OpenID4VPTransactionStatus = z.infer<typeof openid4vpStatusSchema>;

export function getOpenID4VPStatus(
  id: string,
  signal?: AbortSignal,
): Promise<OpenID4VPTransactionStatus> {
  return request(`/api/v1/openid4vp/${encodeURIComponent(id)}/status`, {
    schema: openid4vpStatusSchema,
    signal,
  });
}

export const openid4vpOrgSchema = z.object({
  slug: z.string(),
  name: z.string(),
  logoUri: z.string().optional(),
});
export type OpenID4VPOrg = z.infer<typeof openid4vpOrgSchema>;

export function getOpenID4VPOrganizations(
  id: string,
  signal?: AbortSignal,
): Promise<OpenID4VPOrg[]> {
  return request(`/api/v1/openid4vp/${encodeURIComponent(id)}/orgs`, {
    schema: z.array(openid4vpOrgSchema),
    signal,
  });
}

export const openid4vpSelectSchema = z.object({
  status: z.enum(OPENID4VP_STATUSES),
  redirectUri: z.string().optional(),
});
export type OpenID4VPSelectResult = z.infer<typeof openid4vpSelectSchema>;

// selectOpenID4VPOrganization is the one org-scoped call: it runs behind the
// tenant seam, so the slug is authorization-checked against the caller's
// membership server-side and never taken as a hint.
export function selectOpenID4VPOrganization(
  slug: string,
  id: string,
  signal?: AbortSignal,
): Promise<OpenID4VPSelectResult> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/openid4vp/${encodeURIComponent(id)}/select`,
    {
      schema: openid4vpSelectSchema,
      method: "POST",
      signal,
    },
  );
}
