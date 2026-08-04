import { z } from "zod";
import { absoluteApiUrl, request } from "./http";

// avatarUri is the API path serving the user's own portrait photo, "" when they
// have not uploaded one (their initials are shown instead).
export const meSchema = z.object({
  id: z.string(),
  email: z.string(),
  preferredName: z.string().nullable(),
  givenNames: z.string(),
  lastName: z.string(),
  avatarUri: z.string(),
  isPlatformAdmin: z.boolean(),
});

export type Me = z.infer<typeof meSchema>;

// The backend returns the avatar as a path on the API; make it absolute so an
// <img> loads it from the API origin even when the SPA is served elsewhere.
function withAbsoluteAvatar(me: Me): Me {
  return me.avatarUri ? { ...me, avatarUri: absoluteApiUrl(me.avatarUri) } : me;
}

export const pendingInvitationSchema = z.object({
  id: z.string(),
  organizationName: z.string(),
  organizationSlug: z.string(),
});
export type PendingInvitation = z.infer<typeof pendingInvitationSchema>;

const pendingInvitationsClaimSchema = z.object({
  pendingInvitations: z.array(pendingInvitationSchema),
});

// A claim either authenticates an existing user (meSchema) or, for a brand-new
// invitee with no account, returns their pending invitations to route to accept.
export const claimResultSchema = z.union([
  meSchema,
  pendingInvitationsClaimSchema,
]);
export type ClaimResult = z.infer<typeof claimResultSchema>;

export const authSessionSchema = z.object({
  id: z.string(),
  walletLink: z.string(),
});
export type AuthSession = z.infer<typeof authSessionSchema>;

// startDisclosureSession begins an OpenID4VP presentation at the given endpoint
// (login, or an invitation-accept session) and returns the transaction id plus
// the wallet deeplink to render as a QR / universal link.
export function startDisclosureSession(
  url: string,
  signal?: AbortSignal,
): Promise<AuthSession> {
  return request(url, {
    schema: authSessionSchema,
    method: "POST",
    signal,
  });
}

const sessionStatusSchema = z.object({ status: z.string() });

// getSessionStatus polls the verifier for a presentation's completion. Every
// session (login or invitation) is polled through the central auth status
// endpoint by its transaction id.
export function getSessionStatus(
  id: string,
  signal?: AbortSignal,
): Promise<string> {
  return request(`/api/v1/auth/session/${encodeURIComponent(id)}/status`, {
    schema: sessionStatusSchema,
    signal,
  }).then((r) => r.status);
}

export async function claimAuthSession(
  token: string,
  signal?: AbortSignal,
): Promise<ClaimResult> {
  const result = await request(
    `/api/v1/auth/session/${encodeURIComponent(token)}/claim`,
    {
      schema: claimResultSchema,
      method: "POST",
      signal,
    },
  );
  // The caller seeds the `me` cache with this, so the avatar path has to be made
  // absolute here too.
  return "pendingInvitations" in result ? result : withAbsoluteAvatar(result);
}

export async function getMe(signal?: AbortSignal): Promise<Me> {
  return withAbsoluteAvatar(
    await request("/api/v1/me", {
      schema: meSchema,
      signal,
    }),
  );
}

// The multipart field the backend reads the photo from.
const AVATAR_FORM_FIELD = "avatar";

// updateMyAvatar uploads a new portrait photo. The backend re-encodes it to a
// fixed square JPEG, so the returned avatarUri carries a fresh version and the
// browser refetches instead of reusing the previous photo.
export async function updateMyAvatar(
  file: File,
  signal?: AbortSignal,
): Promise<Me> {
  const form = new FormData();
  form.append(AVATAR_FORM_FIELD, file);
  return withAbsoluteAvatar(
    await request("/api/v1/me/avatar", {
      schema: meSchema,
      method: "PUT",
      body: form,
      signal,
    }),
  );
}

export async function removeMyAvatar(signal?: AbortSignal): Promise<Me> {
  return withAbsoluteAvatar(
    await request("/api/v1/me/avatar", {
      schema: meSchema,
      method: "DELETE",
      signal,
    }),
  );
}

export function logout(signal?: AbortSignal): Promise<void> {
  return request("/api/v1/auth/logout", {
    schema: z.void(),
    method: "POST",
    signal,
  });
}
