import { z } from "zod";
import { request } from "./http";
import { authorizePath } from "../lib/signing-authorization-start";

// The built-in signing service's authorization page. A signing ceremony sends
// the signer's browser here with an authorization id: the signer approves it in
// the Yivi app, and the page follows the service's redirect back into the
// ceremony. The id is the only key: there is no session and no organization.

export const AUTHORIZATION_STATUS = {
  pending: "pending",
  approved: "approved",
  denied: "denied",
} as const;

// What is approved: "link" issues or finds the signer's certificate, "sign"
// authorizes one signature.
export const AUTHORIZATION_PURPOSE = {
  link: "link",
  sign: "sign",
} as const;

// Which data the signer discloses: a chip document (passport, identity card,
// residence permit) or their registered personal data (BRP).
export const AUTHORIZATION_SOURCE = {
  mrtd: "mrtd",
  brp: "brp",
} as const;

export const AUTHORIZATION_DENIAL = {
  cancelled: "cancelled",
  identityMismatch: "identity_mismatch",
  identityIncomplete: "identity_incomplete",
} as const;

// Plain strings, not zod enums, so a new backend value never breaks parsing.
export const yiviCscAuthorizationSchema = z.object({
  status: z.string(),
  purpose: z.string(),
  // For purpose "sign": the document's file name.
  description: z.string().optional(),
  // For purpose "sign": the data the signer's certificate was issued on, which
  // they must disclose again; absent when any will do.
  source: z.string().optional(),
  // Only while pending: an openid4vp:// request for the Yivi app.
  walletLink: z.string().optional(),
  expiresAt: z.string(),
  // Why, when denied.
  reason: z.string().optional(),
  // Once approved or denied: where the browser goes next.
  redirectUrl: z.string().optional(),
});
export type YiviCscAuthorization = z.infer<typeof yiviCscAuthorizationSchema>;

// What oauth2/authorize answers a page that asks for JSON instead of following
// its redirect. No authorizationId means none was started: redirectUrl is then
// the ceremony's callback carrying the error.
export const yiviCscAuthorizationStartSchema = z.object({
  authorizationId: z.string().optional(),
  redirectUrl: z.string(),
});
export type YiviCscAuthorizationStart = z.infer<
  typeof yiviCscAuthorizationStartSchema
>;

// Starts the authorization a ceremony's authorizeUrl names, for the page to
// show itself. The URL is absolute and on the API; only its path and query are
// used, so the request goes where every other API call goes.
export function startYiviCscAuthorization(
  authorizeUrl: string,
  signal?: AbortSignal,
): Promise<YiviCscAuthorizationStart> {
  return request(authorizePath(authorizeUrl), {
    schema: yiviCscAuthorizationStartSchema,
    signal,
  });
}

function base(id: string): string {
  return `/api/v1/yivi-csc/authorizations/${encodeURIComponent(id)}`;
}

// Answers 404 (authorization_not_found) for an unknown or expired id.
export function getYiviCscAuthorization(
  id: string,
  signal?: AbortSignal,
): Promise<YiviCscAuthorization> {
  return request(base(id), {
    schema: yiviCscAuthorizationSchema,
    signal,
  });
}

// The signer refusing: the authorization comes back denied, with where to go.
export function cancelYiviCscAuthorization(
  id: string,
): Promise<YiviCscAuthorization> {
  return request(`${base(id)}/cancel`, {
    schema: yiviCscAuthorizationSchema,
    method: "POST",
  });
}
