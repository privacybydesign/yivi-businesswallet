import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { meQueryKey } from "./auth.queries";
import {
  acceptInvitationById,
  declineMyInvitation,
  getIdentityReviews,
  getInvitePreview,
  getMyInvitations,
  resolveIdentityReview,
} from "./invitations";
import type {
  AcceptResult,
  IdentityReview,
  InvitePreview,
  MyInvitation,
  ResolveReviewResult,
} from "./invitations";

export const invitePreviewQueryKey = (token: string) =>
  ["invite", token] as const;
export const myInvitationsQueryKey = ["me", "invitations"] as const;
export const identityReviewsQueryKey = ["admin", "identity-reviews"] as const;

export function useInvitePreviewQuery(
  token: string,
): UseQueryResult<InvitePreview, Error> {
  return useQuery({
    queryKey: invitePreviewQueryKey(token),
    queryFn: ({ signal }) => getInvitePreview(token, signal),
    retry: false,
  });
}

export function useMyInvitationsQuery(
  enabled = true,
): UseQueryResult<MyInvitation[], Error> {
  return useQuery({
    queryKey: myInvitationsQueryKey,
    queryFn: ({ signal }) => getMyInvitations(signal),
    enabled,
  });
}

export function useIdentityReviewsQuery(
  enabled = true,
): UseQueryResult<IdentityReview[], Error> {
  return useQuery({
    queryKey: identityReviewsQueryKey,
    queryFn: ({ signal }) => getIdentityReviews(signal),
    enabled,
  });
}

export function useAcceptInvitationByIdMutation(): UseMutationResult<
  AcceptResult,
  Error,
  { id: string; disclosureToken: string }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, disclosureToken }) =>
      acceptInvitationById(id, disclosureToken),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: myInvitationsQueryKey });
      void queryClient.invalidateQueries({ queryKey: meQueryKey });
    },
  });
}

export function useDeclineMyInvitationMutation(): UseMutationResult<
  void,
  Error,
  string
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id) => declineMyInvitation(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: myInvitationsQueryKey });
    },
  });
}

export function useResolveIdentityReviewMutation(): UseMutationResult<
  ResolveReviewResult,
  Error,
  { id: string; approve: boolean }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, approve }) => resolveIdentityReview(id, approve),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: identityReviewsQueryKey });
    },
  });
}
