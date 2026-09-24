import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  activateProofingFlowVersion,
  createProofingFlow,
  createProofingFlowVersion,
  createProofingRequest,
  getProofingFlowVersions,
  getProofingMembers,
  getProofingFlows,
  getProofingLink,
  getProofingRequests,
  setProofingFlowSelection,
  startProofing,
} from "./identity-proofing";
import type {
  ProofingFlow,
  ProofingFlowSelection,
  ProofingFlowSpec,
  ProofingLink,
  ProofingMember,
  ProofingRequest,
  ProofingRequestInput,
  ProofingSent,
  ProofingStart,
} from "./identity-proofing";
import { toast } from "../lib/toast";
import { isProofingLive } from "../lib/identity-proofing";

// Both reads reconcile live requests at the proofing service, so a request in
// flight is re-fetched until it settles. The recipient's own page polls faster:
// it is watching one phone finish.
const REQUESTS_POLL_INTERVAL_MS = 10_000;
const LINK_POLL_INTERVAL_MS = 3000;

export function proofingQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "identity-proofing"];
}

export function proofingFlowsQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "flows"];
}

export function proofingRequestsQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "requests"];
}

export function proofingFlowVersionsQueryKey(
  slug: string,
  flowId: string,
): readonly string[] {
  return [...proofingFlowsQueryKey(slug), flowId, "versions"];
}

export function proofingMembersQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "members"];
}

export function proofingLinkQueryKey(token: string): readonly string[] {
  return ["identity-proofing", "link", token];
}

export function useProofingFlowsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<ProofingFlow[], Error> {
  return useQuery({
    queryKey: proofingFlowsQueryKey(slug),
    queryFn: ({ signal }) => getProofingFlows(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useCreateProofingFlowMutation(
  slug: string,
): UseMutationResult<ProofingFlow, Error, ProofingFlowSpec> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (spec) => createProofingFlow(slug, spec),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingFlowCreated"));
      void queryClient.invalidateQueries({
        queryKey: proofingFlowsQueryKey(slug),
      });
    },
  });
}

export function useProofingFlowVersionsQuery(
  slug: string,
  flowId: string,
  enabled: boolean,
): UseQueryResult<ProofingFlow[], Error> {
  return useQuery({
    queryKey: proofingFlowVersionsQueryKey(slug, flowId),
    queryFn: ({ signal }) => getProofingFlowVersions(slug, flowId, signal),
    enabled: enabled && slug !== "" && flowId !== "",
  });
}

// Both change which version of a flow is active, so the flow list and the
// flow's version history are refetched.
function invalidateFlow(
  queryClient: ReturnType<typeof useQueryClient>,
  slug: string,
): void {
  void queryClient.invalidateQueries({ queryKey: proofingFlowsQueryKey(slug) });
}

export function useEditProofingFlowMutation(
  slug: string,
  flowId: string,
): UseMutationResult<ProofingFlow, Error, ProofingFlowSpec> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (spec) => createProofingFlowVersion(slug, flowId, spec),
    meta: { suppressErrorToast: true },
    onSuccess: (flow) => {
      toast.success(
        t("toasts.identityProofingFlowVersionSaved", { version: flow.version }),
      );
      invalidateFlow(queryClient, slug);
    },
  });
}

export function useActivateProofingFlowVersionMutation(
  slug: string,
  flowId: string,
): UseMutationResult<ProofingFlow, Error, number> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (version) => activateProofingFlowVersion(slug, flowId, version),
    meta: { suppressErrorToast: true },
    onSuccess: (flow) => {
      toast.success(
        t("toasts.identityProofingFlowVersionActivated", {
          version: flow.version,
        }),
      );
      invalidateFlow(queryClient, slug);
    },
  });
}

export function useProofingMembersQuery(
  slug: string,
): UseQueryResult<ProofingMember[], Error> {
  return useQuery({
    queryKey: proofingMembersQueryKey(slug),
    queryFn: ({ signal }) => getProofingMembers(slug, signal),
    enabled: slug !== "",
  });
}

// The answer is the org's flow list as an admin sees it, so it replaces the
// cached list rather than refetching it.
export function useSetProofingFlowSelectionMutation(
  slug: string,
): UseMutationResult<ProofingFlow[], Error, ProofingFlowSelection> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (selection) => setProofingFlowSelection(slug, selection),
    meta: { suppressErrorToast: true },
    onSuccess: (flows) => {
      toast.success(t("toasts.identityProofingFlowsSaved"));
      queryClient.setQueryData(proofingFlowsQueryKey(slug), flows);
    },
  });
}

export function useProofingRequestsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<ProofingRequest[], Error> {
  return useQuery({
    queryKey: proofingRequestsQueryKey(slug),
    queryFn: ({ signal }) => getProofingRequests(slug, signal),
    enabled: enabled && slug !== "",
    refetchInterval: (query) =>
      query.state.data?.some((r) => isProofingLive(r.status))
        ? REQUESTS_POLL_INTERVAL_MS
        : false,
  });
}

// sending the session mail
export function useCreateProofingRequestMutation(
  slug: string,
): UseMutationResult<ProofingSent, Error, ProofingRequestInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createProofingRequest(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: (sent) => {
      // The request stands either way, without the mail the member has no link.
      if (sent.mailSent) {
        toast.success(t("toasts.identityProofingRequestSent"));
      } else {
        toast.error(t("toasts.identityProofingMailNotSent"));
      }
      void queryClient.invalidateQueries({
        queryKey: proofingRequestsQueryKey(slug),
      });
    },
  });
}

// polling is on once the recipient has started: before that nothing changes
// at the proofing service.
export function useProofingLinkQuery(
  token: string,
  polling: boolean,
): UseQueryResult<ProofingLink, Error> {
  return useQuery({
    queryKey: proofingLinkQueryKey(token),
    queryFn: ({ signal }) => getProofingLink(token, signal),
    enabled: token !== "",
    refetchInterval: (query) =>
      polling && isProofingLive(query.state.data?.status ?? "")
        ? LINK_POLL_INTERVAL_MS
        : false,
  });
}

export function useStartProofingMutation(
  token: string,
): UseMutationResult<ProofingStart, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => startProofing(token),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: proofingLinkQueryKey(token),
      });
    },
  });
}
