import { z } from "zod";
import { request } from "./http";

export const meSchema = z.object({
  id: z.string(),
  email: z.string(),
  preferredName: z.string().nullable(),
  givenNames: z.string(),
  lastName: z.string(),
  isPlatformAdmin: z.boolean(),
});

export type Me = z.infer<typeof meSchema>;

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

export function claimAuthSession(
  token: string,
  signal?: AbortSignal,
): Promise<ClaimResult> {
  return request(`/api/v1/auth/session/${encodeURIComponent(token)}/claim`, {
    schema: claimResultSchema,
    method: "POST",
    signal,
  });
}

export function getMe(signal?: AbortSignal): Promise<Me> {
  return request("/api/v1/me", {
    schema: meSchema,
    signal,
  });
}

export function logout(signal?: AbortSignal): Promise<void> {
  return request("/api/v1/auth/logout", {
    schema: z.void(),
    method: "POST",
    signal,
  });
}
