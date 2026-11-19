import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import {
  cancelYiviCscAuthorization,
  getYiviCscAuthorization,
} from "./yivi-csc";
import type { YiviCscAuthorization } from "./yivi-csc";
import { authorizationPollContinues } from "../lib/signing-authorization";

// The signer approves in the Yivi app, out of band, so the page polls until
// the authorization is decided: the cadence of the other pages that watch a
// wallet ceremony finish (signing.queries.ts, the on-screen proofing page).
const POLL_INTERVAL_MS = 2_000;

export function yiviCscAuthorizationQueryKey(id: string): readonly string[] {
  return ["yivi-csc", "authorization", id];
}

export function useYiviCscAuthorizationQuery(
  id: string,
): UseQueryResult<YiviCscAuthorization, Error> {
  return useQuery({
    queryKey: yiviCscAuthorizationQueryKey(id),
    queryFn: ({ signal }) => getYiviCscAuthorization(id, signal),
    enabled: id !== "",
    refetchInterval: (query) =>
      authorizationPollContinues(query.state.data, query.state.error)
        ? POLL_INTERVAL_MS
        : false,
  });
}

// Cancelling answers with the denied authorization, which replaces the cached
// one so the page follows its redirect without another read.
export function useCancelYiviCscAuthorizationMutation(
  id: string,
): UseMutationResult<YiviCscAuthorization, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => cancelYiviCscAuthorization(id),
    meta: { suppressErrorToast: true },
    onSuccess: (authorization) => {
      queryClient.setQueryData(yiviCscAuthorizationQueryKey(id), authorization);
    },
  });
}
