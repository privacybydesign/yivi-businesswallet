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
  UseQueryOptions,
  UseQueryResult,
} from "@tanstack/react-query";
import {
  SIGNER_STATUS,
  SIGNING_STATUS,
  createSigningRequest,
  declineExternalSign,
  declineSignRequest,
  externalSigningEventsUrl,
  getExternalSigning,
  getPendingSigningRequests,
  getSigningAvailability,
  getSigningCredential,
  getSigningRequest,
  linkExternalCredential,
  linkSigningCredential,
  listSigningRequests,
  startExternalSign,
  startSignRequest,
} from "./signing";
import type {
  ExternalSigning,
  LinkedCredential,
  NewSigningRequest,
  SigningAvailability,
  SigningRequest,
  SigningRequestPage,
  SigningStart,
} from "./signing";
import { useEventStream } from "./event-stream";
import { useOrgStreamConnected } from "./org-events";
import { RARELY_CHANGING_STALE_TIME_MS } from "./stale-time";

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
    ...signingAvailabilityQueryOptions(slug),
    enabled: enabled && slug !== "",
  });
}

// Shared by the hook and the route loader, so a prefetch fills the same entry.
export function signingAvailabilityQueryOptions(
  slug: string,
): UseQueryOptions<SigningAvailability> {
  return {
    queryKey: signingAvailabilityQueryKey(slug),
    queryFn: ({ signal }) => getSigningAvailability(slug, signal),
    staleTime: RARELY_CHANGING_STALE_TIME_MS,
  };
}

// Position of the read's name in a signing key, after the org's prefix.
const SIGNING_READ_KEY_INDEX = 4;
const REQUEST_READS: ReadonlySet<unknown> = new Set([
  "request",
  "pending",
  "requests",
]);

// Matches the org's reads that a signing request moving can change.
export function signingRequestReads(
  slug: string,
): (query: { queryKey: readonly unknown[] }) => boolean {
  return ({ queryKey }) =>
    queryKey[2] === slug &&
    queryKey[3] === "signing" &&
    REQUEST_READS.has(queryKey[SIGNING_READ_KEY_INDEX]);
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
  const streaming = useOrgStreamConnected(slug);
  return useQuery({
    queryKey: signingRequestQueryKey(slug, id ?? ""),
    queryFn: ({ signal }) => getSigningRequest(slug, id!, signal),
    enabled: slug !== "" && id !== null,
    // Poll only while the acting user's own signature is still settling. With
    // co-signing a request stays `awaiting_signatures` until every OTHER signer has
    // signed — which can be days — so polling on the request status alone would never
    // stop. Once the caller's own signer entry is signed (or the request is completed
    // or failed), there is nothing left for this card to wait on.
    // The org's event stream says when the request moves; the poll below is
    // only the fallback while that stream is down.
    refetchInterval: (query) => {
      const data = query.state.data;
      if (streaming || data?.status !== SIGNING_STATUS.awaitingSignatures) {
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

// useDeclineSignRequestMutation lets a pending signer refuse to sign a request.
// Unlike starting a ceremony, this settles the request immediately, so it
// invalidates the same queries create does (pending list + history) plus the
// request's own detail, which the "to sign" card polls once declined.
export function useDeclineSignRequestMutation(
  slug: string,
): UseMutationResult<void, Error, { id: string; reason: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }) => declineSignRequest(slug, id, reason),
    meta: { suppressErrorToast: true },
    onSuccess: (_data, { id }) => {
      void queryClient.invalidateQueries({
        queryKey: signingPendingQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: signingRequestsQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: signingRequestQueryKey(slug, id),
      });
    },
  });
}

// The external-signee flow is keyed by the invitation token, not by an org slug —
// the signee has no membership, so none of these queries live under an organisation.
export function externalSigningQueryKey(token: string): readonly string[] {
  return ["signing", "external", token];
}

// What the external signee's stream sends (events.SigningRequest in the backend).
const EXTERNAL_EVENT_KINDS = ["signing-request"] as const;

export function useExternalSigningQuery(
  token: string,
): UseQueryResult<ExternalSigning, Error> {
  const queryClient = useQueryClient();
  // The signature is completed out of band (the OAuth callback); the request's
  // stream says when, and when a co-signer signs.
  const streaming = useEventStream(
    token === "" ? null : externalSigningEventsUrl(token),
    EXTERNAL_EVENT_KINDS,
    () =>
      void queryClient.invalidateQueries({
        queryKey: externalSigningQueryKey(token),
      }),
  );
  return useQuery({
    queryKey: externalSigningQueryKey(token),
    queryFn: ({ signal }) => getExternalSigning(token, signal),
    enabled: token !== "",
    // The fallback while the stream is down: poll until this signee's own
    // signature lands. Co-signers can take days, so the request status alone
    // would never stop the poll.
    refetchInterval: (query) => {
      const data = query.state.data;
      return !streaming &&
        data?.status === SIGNING_STATUS.awaitingSignatures &&
        data.signerStatus !== SIGNER_STATUS.signed
        ? POLL_INTERVAL_MS
        : false;
    },
  });
}

export function useLinkExternalCredentialMutation(
  token: string,
): UseMutationResult<SigningStart, Error, void> {
  return useMutation({
    mutationFn: () => linkExternalCredential(token),
    meta: { suppressErrorToast: true },
  });
}

export function useStartExternalSignMutation(
  token: string,
): UseMutationResult<SigningStart, Error, void> {
  return useMutation({
    mutationFn: () => startExternalSign(token),
    meta: { suppressErrorToast: true },
  });
}

export function useDeclineExternalSignMutation(
  token: string,
): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (reason) => declineExternalSign(token, reason),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: externalSigningQueryKey(token),
      });
    },
  });
}

export function useInvalidateSigningCredential(slug: string): () => void {
  const queryClient = useQueryClient();
  return () =>
    void queryClient.invalidateQueries({
      queryKey: signingCredentialQueryKey(slug),
    });
}
