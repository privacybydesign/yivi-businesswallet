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
  getProofingStats,
  setProofingCustomerFlows,
  setProofingFlowSelection,
  updateProofingCustomer,
  createProofingApiKey,
  getProofingApiKeys,
  getProofingWebhook,
  getProofingWebhookDeliveries,
  removeProofingCustomer,
  removeProofingWebhook,
  revokeProofingApiKey,
  rotateProofingWebhookSecret,
  saveProofingCustomerBranding,
  saveProofingWebhook,
  sendProofingWebhookTest,
  getProofingRequestEvents,
} from "./identity-proofing";
import type {
  ProofingCustomer,
  ProofingCustomerFlow,
  ProofingCustomerUpdate,
  ProofingFlow,
  ProofingFlowSelection,
  ProofingFlowSpec,
  ProofingRequest,
  ProofingRequestInput,
  ProofingSent,
  ProofingStats,
  CreatedProofingApiKey,
  ProofingApiKey,
  ProofingBrandingInput,
  ProofingWebhook,
  WebhookDelivery,
} from "./identity-proofing";
import type { AuditEvent } from "./organization";
import { toast } from "../lib/toast";
import { isProofingLive } from "../lib/identity-proofing";

// Both reads reconcile live requests at the proofing service, so a request in
// flight is re-fetched until it settles. The recipient's own page polls faster:
// it is watching one phone finish.
const REQUESTS_POLL_INTERVAL_MS = 10_000;
// The worker sends a due delivery within seconds, so a pending one is watched
// until it is sent.
const DELIVERIES_POLL_INTERVAL_MS = 5_000;

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

// Under the requests key: what refreshes the request lists refreshes the counts.
function proofingStatsQueryKey(slug: string): readonly string[] {
  return [...proofingRequestsQueryKey(slug), "stats"];
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
      invalidateFlow(queryClient, slug);
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
  // Each customer's flow list carries the flows' active version too.
  void queryClient.invalidateQueries({
    queryKey: proofingCustomersQueryKey(slug),
  });
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

export function useProofingStatsQuery(
  slug: string,
): UseQueryResult<ProofingStats, Error> {
  return useQuery({
    queryKey: proofingStatsQueryKey(slug),
    queryFn: ({ signal }) => getProofingStats(slug, signal),
    enabled: slug !== "",
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

// A new name shows in the list and in the customer's requests; a pause shows
// in the list and closes the send form.
export function useUpdateProofingCustomerMutation(
  slug: string,
  customerId: string,
): UseMutationResult<ProofingCustomer, Error, ProofingCustomerUpdate> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (update) => updateProofingCustomer(slug, customerId, update),
    meta: { suppressErrorToast: true },
    onSuccess: (customer, update) => {
      toast.success(
        update.paused === undefined
          ? t("toasts.identityProofingCustomerRenamed")
          : update.paused
            ? t("toasts.identityProofingCustomerPaused")
            : t("toasts.identityProofingCustomerResumed"),
      );
      queryClient.setQueryData(
        proofingCustomerQueryKey(slug, customerId),
        customer,
      );
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
        exact: true,
      });
      if (update.name !== undefined) {
        void queryClient.invalidateQueries({
          queryKey: proofingRequestsQueryKey(slug),
        });
      }
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

function proofingApiKeysQueryKey(
  slug: string,
  customerId: string,
): readonly string[] {
  return [...proofingCustomerQueryKey(slug, customerId), "api-keys"];
}

function proofingWebhookQueryKey(
  slug: string,
  customerId: string,
): readonly string[] {
  return [...proofingCustomerQueryKey(slug, customerId), "webhook"];
}

// A removed customer leaves every list it was in, and its sessions go with it.
export function useRemoveProofingCustomerMutation(
  slug: string,
  customerId: string,
): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => removeProofingCustomer(slug, customerId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingCustomerRemoved"));
      queryClient.removeQueries({
        queryKey: proofingCustomerQueryKey(slug, customerId),
      });
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
        exact: true,
      });
      void queryClient.invalidateQueries({
        queryKey: proofingRequestsQueryKey(slug),
      });
    },
  });
}

export function useSaveProofingBrandingMutation(
  slug: string,
  customerId: string,
): UseMutationResult<ProofingCustomer, Error, ProofingBrandingInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) =>
      saveProofingCustomerBranding(slug, customerId, input),
    meta: { suppressErrorToast: true },
    onSuccess: (customer) => {
      toast.success(t("toasts.identityProofingBrandingSaved"));
      queryClient.setQueryData(
        proofingCustomerQueryKey(slug, customerId),
        customer,
      );
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
        exact: true,
      });
    },
  });
}

export function useProofingApiKeysQuery(
  slug: string,
  customerId: string,
): UseQueryResult<ProofingApiKey[], Error> {
  return useQuery({
    queryKey: proofingApiKeysQueryKey(slug, customerId),
    queryFn: ({ signal }) => getProofingApiKeys(slug, customerId, signal),
    enabled: slug !== "" && customerId !== "",
  });
}

// The secret is in the answer only; the caller shows it once.
export function useCreateProofingApiKeyMutation(
  slug: string,
  customerId: string,
): UseMutationResult<CreatedProofingApiKey, Error, string> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name) => createProofingApiKey(slug, customerId, name),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: proofingApiKeysQueryKey(slug, customerId),
      });
    },
  });
}

export function useRevokeProofingApiKeyMutation(
  slug: string,
  customerId: string,
): UseMutationResult<ProofingApiKey, Error, string> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (keyId) => revokeProofingApiKey(slug, customerId, keyId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingApiKeyRevoked"));
      void queryClient.invalidateQueries({
        queryKey: proofingApiKeysQueryKey(slug, customerId),
      });
    },
  });
}

export function useProofingWebhookQuery(
  slug: string,
  customerId: string,
): UseQueryResult<ProofingWebhook, Error> {
  return useQuery({
    queryKey: proofingWebhookQueryKey(slug, customerId),
    queryFn: ({ signal }) => getProofingWebhook(slug, customerId, signal),
    enabled: slug !== "" && customerId !== "",
  });
}

export function useProofingWebhookDeliveriesQuery(
  slug: string,
  customerId: string,
  enabled: boolean,
): UseQueryResult<WebhookDelivery[], Error> {
  return useQuery({
    queryKey: [...proofingWebhookQueryKey(slug, customerId), "deliveries"],
    queryFn: ({ signal }) =>
      getProofingWebhookDeliveries(slug, customerId, signal),
    enabled: enabled && slug !== "" && customerId !== "",
    refetchInterval: (query) =>
      query.state.data?.some((d) => d.status === "pending")
        ? DELIVERIES_POLL_INTERVAL_MS
        : false,
  });
}

// Every webhook change refreshes the endpoint, its deliveries and the
// customer's health in the lists.
function invalidateWebhook(
  queryClient: ReturnType<typeof useQueryClient>,
  slug: string,
  customerId: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: proofingWebhookQueryKey(slug, customerId),
  });
  void queryClient.invalidateQueries({
    queryKey: proofingCustomerQueryKey(slug, customerId),
    exact: true,
  });
  void queryClient.invalidateQueries({
    queryKey: proofingCustomersQueryKey(slug),
    exact: true,
  });
}

// The answer carries a new endpoint's secret, once.
export function useSaveProofingWebhookMutation(
  slug: string,
  customerId: string,
): UseMutationResult<
  ProofingWebhook,
  Error,
  { url: string; events: string[] }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => saveProofingWebhook(slug, customerId, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingWebhookSaved"));
      invalidateWebhook(queryClient, slug, customerId);
    },
  });
}

export function useRemoveProofingWebhookMutation(
  slug: string,
  customerId: string,
): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => removeProofingWebhook(slug, customerId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingWebhookRemoved"));
      invalidateWebhook(queryClient, slug, customerId);
    },
  });
}

export function useRotateProofingWebhookSecretMutation(
  slug: string,
  customerId: string,
): UseMutationResult<ProofingWebhook, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => rotateProofingWebhookSecret(slug, customerId),
    meta: { suppressErrorToast: true },
    onSuccess: () => invalidateWebhook(queryClient, slug, customerId),
  });
}

export function useSendProofingWebhookTestMutation(
  slug: string,
  customerId: string,
): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => sendProofingWebhookTest(slug, customerId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingWebhookTestQueued"));
      invalidateWebhook(queryClient, slug, customerId);
    },
  });
}

// A session's timeline, fetched when its row is opened; a live session keeps
// adding events, so it is re-read while it runs.
export function useProofingRequestEventsQuery(
  slug: string,
  requestId: string,
  live: boolean,
): UseQueryResult<AuditEvent[], Error> {
  return useQuery({
    queryKey: [...proofingRequestsQueryKey(slug), requestId, "events"],
    queryFn: ({ signal }) => getProofingRequestEvents(slug, requestId, signal),
    enabled: slug !== "" && requestId !== "",
    refetchInterval: live ? REQUESTS_POLL_INTERVAL_MS : false,
  });
}
