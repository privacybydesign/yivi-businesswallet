import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import {
  SIGNING_STATUS,
  createSigningRequest,
  getSigningAvailability,
  getSigningCredential,
  getSigningRequest,
  linkSigningCredential,
} from "./signing";
import type {
  LinkedCredential,
  SigningAvailability,
  SigningRequest,
  SigningStart,
} from "./signing";

// While a request is awaiting the wallet ceremony the backend finishes it out of
// band (the OAuth callback), so the page polls until it settles.
const POLL_INTERVAL_MS = 2_000;

export function signingAvailabilityQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "signing", "availability"];
}

export function useSigningAvailabilityQuery(
  slug: string,
  enabled = true,
): UseQueryResult<SigningAvailability, Error> {
  return useQuery({
    queryKey: signingAvailabilityQueryKey(slug),
    queryFn: ({ signal }) => getSigningAvailability(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function signingCredentialQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "signing", "credential"];
}

export function signingRequestQueryKey(
  slug: string,
  id: string,
): readonly string[] {
  return ["organizations", "detail", slug, "signing", "request", id];
}

export function useSigningCredentialQuery(
  slug: string,
  enabled = true,
): UseQueryResult<LinkedCredential | null, Error> {
  return useQuery({
    queryKey: signingCredentialQueryKey(slug),
    queryFn: ({ signal }) => getSigningCredential(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useSigningRequestQuery(
  slug: string,
  id: string | null,
): UseQueryResult<SigningRequest, Error> {
  return useQuery({
    queryKey: signingRequestQueryKey(slug, id ?? ""),
    queryFn: ({ signal }) => getSigningRequest(slug, id!, signal),
    enabled: slug !== "" && id !== null,
    // Keep polling only while the ceremony is still in flight.
    refetchInterval: (query) =>
      query.state.data?.status === SIGNING_STATUS.awaitingAuthorization
        ? POLL_INTERVAL_MS
        : false,
  });
}

export function useLinkSigningCredentialMutation(
  slug: string,
): UseMutationResult<SigningStart, Error, void> {
  return useMutation({
    mutationFn: () => linkSigningCredential(slug),
    meta: { suppressErrorToast: true },
  });
}

export function useCreateSigningRequestMutation(
  slug: string,
): UseMutationResult<SigningStart, Error, File> {
  return useMutation({
    mutationFn: (document) => createSigningRequest(slug, document),
    meta: { suppressErrorToast: true },
  });
}

export function useInvalidateSigningCredential(slug: string): () => void {
  const queryClient = useQueryClient();
  return () =>
    void queryClient.invalidateQueries({
      queryKey: signingCredentialQueryKey(slug),
    });
}
