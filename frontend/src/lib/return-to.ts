// The login screen may send a freshly signed-in user back to where they came
// from. Today exactly one flow needs that: an inbound OpenID4VP transaction,
// resumed by its opaque id. An unchecked returnTo is an open redirect the moment
// it accepts an absolute URL, so instead of sanitizing generically the accepted
// shape is pinned to that one route — a same-origin, relative path carrying only
// the opaque id (base64url) — and anything else falls back to the app root.

const OPENID4VP_RETURN_TO = /^\/openid4vp\/[A-Za-z0-9_-]+$/;

export const DEFAULT_RETURN_TO = "/";

export function safeReturnTo(raw: string | null | undefined): string {
  if (raw && OPENID4VP_RETURN_TO.test(raw)) {
    return raw;
  }
  return DEFAULT_RETURN_TO;
}

// loginPathFor builds the login URL that brings the user back to an inbound
// OpenID4VP transaction — the opaque id only, never the verifier's parameters.
export function loginPathFor(transactionId: string): string {
  return `/login?returnTo=${encodeURIComponent(`/openid4vp/${transactionId}`)}`;
}
