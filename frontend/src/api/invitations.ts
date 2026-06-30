import { z } from "zod";
import { request } from "./http";

// Session-start URLs are consumed by the Yivi frontend library (not request()),
// which posts to them to begin an identity disclosure and render the QR.
export const INVITATION_SESSION_URL = "/api/v1/invitations/session";
export function inviteSessionUrl(token: string): string {
  return `/api/v1/invite/${encodeURIComponent(token)}/session`;
}

export const invitePreviewSchema = z.object({
  organizationName: z.string(),
  organizationSlug: z.string(),
  givenNames: z.string(),
  lastName: z.string(),
  email: z.string(),
  expiresAt: z.string(),
});
export type InvitePreview = z.infer<typeof invitePreviewSchema>;

export const acceptResultSchema = z.object({
  status: z.enum(["accepted", "pending_review"]),
  organizationName: z.string(),
  organizationSlug: z.string(),
});
export type AcceptResult = z.infer<typeof acceptResultSchema>;

export const myInvitationSchema = z.object({
  id: z.string(),
  organizationName: z.string(),
  organizationSlug: z.string(),
  givenNames: z.string(),
  lastName: z.string(),
  email: z.string(),
  expiresAt: z.string(),
  underReview: z.boolean(),
});
export type MyInvitation = z.infer<typeof myInvitationSchema>;
const myInvitationListSchema = z.array(myInvitationSchema);

export const identityReviewSchema = z.object({
  id: z.string(),
  userId: z.string(),
  email: z.string(),
  organizationName: z.string(),
  organizationSlug: z.string(),
  storedGivenNames: z.string(),
  storedLastName: z.string(),
  disclosedGivenNames: z.string(),
  disclosedLastName: z.string(),
  createdAt: z.string(),
});
export type IdentityReview = z.infer<typeof identityReviewSchema>;
const identityReviewListSchema = z.array(identityReviewSchema);

const resolveReviewResultSchema = z.object({
  approved: z.boolean(),
  organizationName: z.string(),
  organizationSlug: z.string(),
});
export type ResolveReviewResult = z.infer<typeof resolveReviewResultSchema>;

export function getInvitePreview(
  token: string,
  signal?: AbortSignal,
): Promise<InvitePreview> {
  return request(`/api/v1/invite/${encodeURIComponent(token)}`, {
    schema: invitePreviewSchema,
    signal,
  });
}

export function acceptInviteByToken(
  token: string,
  disclosureToken: string,
  signal?: AbortSignal,
): Promise<AcceptResult> {
  return request(`/api/v1/invite/${encodeURIComponent(token)}/accept`, {
    schema: acceptResultSchema,
    method: "POST",
    body: { disclosureToken },
    signal,
  });
}

export function declineInviteByToken(
  token: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`/api/v1/invite/${encodeURIComponent(token)}/decline`, {
    schema: z.void(),
    method: "POST",
    signal,
  });
}

export function acceptInvitationById(
  id: string,
  disclosureToken: string,
  signal?: AbortSignal,
): Promise<AcceptResult> {
  return request(`/api/v1/invitations/${encodeURIComponent(id)}/accept`, {
    schema: acceptResultSchema,
    method: "POST",
    body: { disclosureToken },
    signal,
  });
}

export function getMyInvitations(
  signal?: AbortSignal,
): Promise<MyInvitation[]> {
  return request("/api/v1/me/invitations", {
    schema: myInvitationListSchema,
    signal,
  });
}

export function declineMyInvitation(
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`/api/v1/me/invitations/${encodeURIComponent(id)}/decline`, {
    schema: z.void(),
    method: "POST",
    signal,
  });
}

export function getIdentityReviews(
  signal?: AbortSignal,
): Promise<IdentityReview[]> {
  return request("/api/v1/admin/identity-reviews", {
    schema: identityReviewListSchema,
    signal,
  });
}

export function resolveIdentityReview(
  id: string,
  approve: boolean,
  signal?: AbortSignal,
): Promise<ResolveReviewResult> {
  const action = approve ? "approve" : "reject";
  return request(
    `/api/v1/admin/identity-reviews/${encodeURIComponent(id)}/${action}`,
    {
      schema: resolveReviewResultSchema,
      method: "POST",
      signal,
    },
  );
}
