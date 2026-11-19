import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  OpenID4VPOrg,
  OpenID4VPSelectResult,
  OpenID4VPTransactionStatus,
} from "./openid4vp";
import {
  getOpenID4VPOrganizations,
  getOpenID4VPStatus,
  selectOpenID4VPOrganization,
} from "./openid4vp";

export function openid4vpStatusQueryKey(id: string): readonly string[] {
  return ["openid4vp", id, "status"];
}

export function openid4vpOrgsQueryKey(id: string): readonly string[] {
  return ["openid4vp", id, "orgs"];
}

export function useOpenID4VPStatusQuery(
  id: string,
): UseQueryResult<OpenID4VPTransactionStatus, Error> {
  return useQuery({
    queryKey: openid4vpStatusQueryKey(id),
    queryFn: ({ signal }) => getOpenID4VPStatus(id, signal),
  });
}

export function useOpenID4VPOrgsQuery(
  id: string,
  enabled: boolean,
): UseQueryResult<OpenID4VPOrg[], Error> {
  return useQuery({
    queryKey: openid4vpOrgsQueryKey(id),
    queryFn: ({ signal }) => getOpenID4VPOrganizations(id, signal),
    enabled,
  });
}

export function useSelectOpenID4VPOrganizationMutation(
  id: string,
): UseMutationResult<OpenID4VPSelectResult, Error, string> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (slug: string) => selectOpenID4VPOrganization(slug, id),
    // The outcome is rendered inline from the mutation result; the status
    // query is refreshed so a reload shows the consumed state, and the generic
    // error toast is suppressed in favour of the page's own copy.
    meta: { suppressErrorToast: true },
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: openid4vpStatusQueryKey(id),
      });
    },
  });
}
