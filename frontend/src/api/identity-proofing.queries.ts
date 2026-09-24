import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  activateProofingFlowVersion,
  createProofingCustomer,
  createProofingFlow,
  createProofingFlowVersion,
  createProofingRequest,
  getProofingCustomer,
  getProofingCustomerFlows,
  getProofingCustomers,
  getProofingFlowVersions,
  getProofingFlows,
  getProofingRequests,
  renameProofingCustomer,
  setProofingCustomerFlows,
  setProofingFlowSelection,
} from "./identity-proofing";
import type {
  ProofingCustomer,
  ProofingCustomerFlow,
  ProofingFlow,
  ProofingFlowSelection,
  ProofingFlowSpec,
  ProofingRequest,
  ProofingRequestInput,
  ProofingSent,
} from "./identity-proofing";
import { toast } from "../lib/toast";
import { isProofingLive } from "../lib/identity-proofing";

// Both reads reconcile live requests at the proofing service, so a request in
// flight is re-fetched until it settles. The recipient's own page polls faster:
// it is watching one phone finish.
const REQUESTS_POLL_INTERVAL_MS = 10_000;

export function proofingQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "identity-proofing"];
}

export function proofingFlowsQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "flows"];
}

// Every request list, the org's and each customer's, sits under this key, so
// invalidating it refreshes them all.
export function proofingRequestsQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "requests"];
}

function proofingCustomerRequestsQueryKey(
  slug: string,
  customerId: string,
): readonly string[] {
  return [...proofingRequestsQueryKey(slug), "customer", customerId];
}

export function proofingCustomersQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "customers"];
}

export function proofingCustomerQueryKey(
  slug: string,
  customerId: string,
): readonly string[] {
  return [...proofingCustomersQueryKey(slug), customerId];
}

function proofingCustomerFlowsQueryKey(
  slug: string,
  customerId: string,
): readonly string[] {
  return [...proofingCustomerQueryKey(slug, customerId), "flows"];
}

export function proofingFlowVersionsQueryKey(
  slug: string,
  flowId: string,
): readonly string[] {
  return [...proofingFlowsQueryKey(slug), flowId, "versions"];
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

// customerId narrows the list to the requests sent for that customer.
export function useProofingRequestsQuery(
  slug: string,
  customerId?: string,
): UseQueryResult<ProofingRequest[], Error> {
  return useQuery({
    queryKey: customerId
      ? proofingCustomerRequestsQueryKey(slug, customerId)
      : proofingRequestsQueryKey(slug),
    queryFn: ({ signal }) => getProofingRequests(slug, customerId, signal),
    enabled: slug !== "" && customerId !== "",
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

export function useProofingCustomersQuery(
  slug: string,
): UseQueryResult<ProofingCustomer[], Error> {
  return useQuery({
    queryKey: proofingCustomersQueryKey(slug),
    queryFn: ({ signal }) => getProofingCustomers(slug, signal),
    enabled: slug !== "",
  });
}

export function useProofingCustomerQuery(
  slug: string,
  customerId: string,
): UseQueryResult<ProofingCustomer, Error> {
  return useQuery({
    queryKey: proofingCustomerQueryKey(slug, customerId),
    queryFn: ({ signal }) => getProofingCustomer(slug, customerId, signal),
    enabled: slug !== "" && customerId !== "",
  });
}

export function useCreateProofingCustomerMutation(
  slug: string,
): UseMutationResult<ProofingCustomer, Error, string> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (name) => createProofingCustomer(slug, name),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingCustomerCreated"));
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
      });
    },
  });
}

// A new name shows in the list and in the customer's requests.
export function useRenameProofingCustomerMutation(
  slug: string,
  customerId: string,
): UseMutationResult<ProofingCustomer, Error, string> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (name) => renameProofingCustomer(slug, customerId, name),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingCustomerRenamed"));
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: proofingRequestsQueryKey(slug),
      });
    },
  });
}

export function useProofingCustomerFlowsQuery(
  slug: string,
  customerId: string,
): UseQueryResult<ProofingCustomerFlow[], Error> {
  return useQuery({
    queryKey: proofingCustomerFlowsQueryKey(slug, customerId),
    queryFn: ({ signal }) => getProofingCustomerFlows(slug, customerId, signal),
    enabled: slug !== "" && customerId !== "",
  });
}

// The answer is the customer's flows as an admin sees them, so it replaces the
// cached list; the customer list shows the new assignment too.
export function useSetProofingCustomerFlowsMutation(
  slug: string,
  customerId: string,
): UseMutationResult<ProofingCustomerFlow[], Error, ProofingFlowSelection> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (selection) =>
      setProofingCustomerFlows(slug, customerId, selection),
    meta: { suppressErrorToast: true },
    onSuccess: (flows) => {
      toast.success(t("toasts.identityProofingCustomerFlowsSaved"));
      queryClient.setQueryData(
        proofingCustomerFlowsQueryKey(slug, customerId),
        flows,
      );
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
        exact: true,
      });
      void queryClient.invalidateQueries({
        queryKey: proofingCustomerQueryKey(slug, customerId),
        exact: true,
      });
    },
  });
}
