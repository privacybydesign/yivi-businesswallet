import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type {
  InfiniteData,
  UseInfiniteQueryResult,
  UseMutationResult,
  UseQueryResult,
} from "@tanstack/react-query";
import {
  SIGNER_STATUS,
  SIGNING_STATUS,
  createSigningRequest,
  getPendingSigningRequests,
  getSigningAvailability,
  getSigningCredential,
  getSigningRequest,
  linkSigningCredential,
  listSigningRequests,
  startSignRequest,
} from "./signing";
import type {
  LinkedCredential,
  NewSigningRequest,
  SigningAvailability,
  SigningRequest,
  SigningRequestPage,
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

export function signingPendingQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "signing", "pending"];
}

export function signingRequestsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "signing", "requests"];
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
  myUserId?: string,
): UseQueryResult<SigningRequest, Error> {
  return useQuery({
    queryKey: signingRequestQueryKey(slug, id ?? ""),
    queryFn: ({ signal }) => getSigningRequest(slug, id!, signal),
    enabled: slug !== "" && id !== null,
    // Poll only while the acting user's own signature is still settling. With
    // co-signing a request stays `awaiting_signatures` until every OTHER signer has
    // signed — which can be days — so polling on the request status alone would never
    // stop. Once the caller's own signer entry is signed (or the request is completed
    // or failed), there is nothing left for this card to wait on.
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data || data.status !== SIGNING_STATUS.awaitingSignatures) {
        return false;
      }
      const mine = myUserId
        ? (data.signers ?? []).find((s) => s.userId === myUserId)
        : undefined;
      return mine && mine.status !== SIGNER_STATUS.signed
        ? POLL_INTERVAL_MS
        : false;
    },
  });
}

// usePendingSigningRequestsQuery lists documents awaiting the caller's signature.
export function usePendingSigningRequestsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<SigningRequest[], Error> {
  return useQuery({
    queryKey: signingPendingQueryKey(slug),
    queryFn: ({ signal }) => getPendingSigningRequests(slug, signal),
    enabled: enabled && slug !== "",
  });
}

// useSigningRequestsQuery is the admin-only signed-documents history, paginated.
export function useSigningRequestsQuery(
  slug: string,
  enabled: boolean,
): UseInfiniteQueryResult<InfiniteData<SigningRequestPage>, Error> {
  return useInfiniteQuery({
    queryKey: signingRequestsQueryKey(slug),
    queryFn: ({ pageParam, signal }) =>
      listSigningRequests(slug, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor || undefined,
    enabled: enabled && slug !== "",
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
): UseMutationResult<{ id: string }, Error, NewSigningRequest> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input) => createSigningRequest(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: signingRequestsQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: signingPendingQueryKey(slug),
      });
    },
  });
}

// useStartSignRequestMutation starts the acting user's signing ceremony.
export function useStartSignRequestMutation(
  slug: string,
): UseMutationResult<SigningStart, Error, string> {
  return useMutation({
    mutationFn: (id) => startSignRequest(slug, id),
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
