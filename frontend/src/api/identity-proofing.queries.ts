import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { DEFAULT_LANGUAGE, isSupportedLanguage } from "../i18n/language";
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
  getProofingRequest,
  getProofingApp,
  getHostedProofing,
  decideProofingReview,
  getProofingDataMatches,
  saveProofingFlowKind,
  getHostedProofingStatus,
  startHostedProofing,
  declineHostedProofing,
  getProofingFlowHosted,
  saveProofingFlowHosted,
  saveProofingFlowDiplomas,
  getProofingPause,
  listProofingPauses,
  setPlatformProofingPause,
  setProofingPause,
  getProofingRequestResult,
  getProofingYiviDisclosure,
  newProofingClaimLink,
  startProofingYivi,
  submitProofingFaceFrame,
  uploadProofingDiplomas,
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
  ProofingFaceVerdict,
  ProofingClaimLink,
  ProofingApp,
  ProofingYiviDisclosure,
  ProofingYiviStart,
  ProofingProgress,
  ProofingMethod,
  HostedProofing,
  HostedProgress,
  HostedStart,
  VerifyTarget,
  ProofingReviewInput,
  ProofingResult,
  ProofingPause,
  ProofingFlowHosted,
  DiplomaMode,
  DiplomaVerdict,
  FlowKind,
  ProofingDataMatch,
} from "./identity-proofing";
import type { AuditEvent } from "./organization";
import { toast } from "../lib/toast";
import {
  isProofingLive,
  progressPollContinues,
} from "../lib/identity-proofing";

// Both reads reconcile live requests at the proofing service, so a request in
// flight is re-fetched until it settles. The recipient's own page polls faster:
// it is watching one phone finish.
const REQUESTS_POLL_INTERVAL_MS = 10_000;
// The worker sends a due delivery within seconds, so a pending one is watched
// until it is sent.
const DELIVERIES_POLL_INTERVAL_MS = 5_000;
// The on-screen page is watching one phone finish, so it re-reads its request
// and the Yivi disclosure much sooner.
const ON_SCREEN_POLL_INTERVAL_MS = 2_000;
// A mutation whose answer carries a secret (a new API key, a webhook signing
// secret) is dropped from the cache as soon as nothing shows it, instead of
// lingering there for the default five minutes.
const SECRET_MUTATION_GC_TIME_MS = 0;

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

function proofingFlowHostedQueryKey(
  slug: string,
  flowId: string,
): readonly string[] {
  return [...proofingFlowsQueryKey(slug), flowId, "hosted"];
}

export function useProofingFlowHostedQuery(
  slug: string,
  flowId: string,
): UseQueryResult<ProofingFlowHosted, Error> {
  return useQuery({
    queryKey: proofingFlowHostedQueryKey(slug, flowId),
    queryFn: ({ signal }) => getProofingFlowHosted(slug, flowId, signal),
    enabled: slug !== "" && flowId !== "",
  });
}

export function useSaveProofingFlowHostedMutation(
  slug: string,
  flowId: string,
): UseMutationResult<ProofingFlowHosted, Error, ProofingFlowHosted> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (settings) => saveProofingFlowHosted(slug, flowId, settings),
    // The form shows a failure itself; the global toast would say it twice.
    meta: { suppressErrorToast: true },
    onSuccess: (saved) => {
      toast.success(t("toasts.identityProofingHostedSettingsSaved"));
      queryClient.setQueryData(proofingFlowHostedQueryKey(slug, flowId), saved);
    },
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

// The flow lists carry each flow's diploma mode, so they are refetched.
export function useSaveProofingFlowDiplomasMutation(
  slug: string,
): UseMutationResult<
  DiplomaMode,
  Error,
  { flowId: string; mode: DiplomaMode }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ flowId, mode }) =>
      saveProofingFlowDiplomas(slug, flowId, mode),
    meta: { suppressErrorToast: true },
    onSuccess: () => invalidateFlow(queryClient, slug),
  });
}

export function useSaveProofingFlowKindMutation(
  slug: string,
): UseMutationResult<FlowKind, Error, { flowId: string; kind: FlowKind }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ flowId, kind }) => saveProofingFlowKind(slug, flowId, kind),
    meta: { suppressErrorToast: true },
    onSuccess: () => invalidateFlow(queryClient, slug),
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
    queryFn: ({ signal }) => getProofingRequests(slug, { customerId }, signal),
    enabled: slug !== "" && customerId !== "",
    refetchInterval: (query) =>
      query.state.data?.some((r) => isProofingLive(r.status))
        ? REQUESTS_POLL_INTERVAL_MS
        : false,
  });
}

// The requests sent to one member, newest first. Under the requests key, so
// everything that refreshes the org's requests refreshes these too.
export function useMemberProofingRequestsQuery(
  slug: string,
  userId: string,
): UseQueryResult<ProofingRequest[], Error> {
  return useQuery({
    queryKey: [...proofingRequestsQueryKey(slug), "member", userId],
    queryFn: ({ signal }) =>
      getProofingRequests(slug, { subjectUserId: userId }, signal),
    enabled: slug !== "" && userId !== "",
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
  const { t, i18n } = useTranslation();
  return useMutation({
    // The request follows the language the sender has the wallet in.
    mutationFn: (input) =>
      createProofingRequest(
        slug,
        input,
        isSupportedLanguage(i18n.resolvedLanguage)
          ? i18n.resolvedLanguage
          : DEFAULT_LANGUAGE,
      ),
    meta: { suppressErrorToast: true },
    onSuccess: (sent, input) => {
      void queryClient.invalidateQueries({
        queryKey: proofingRequestsQueryKey(slug),
      });
      // Only a mailed request has a mail to report on: an on-screen session is
      // on the page that asked for it, and a hosted link is handed out by the
      // sender, so neither is mailed.
      if (
        "channel" in input &&
        input.channel !== undefined &&
        input.channel !== "email"
      ) {
        return;
      }
      // The request stands either way, without the mail the member has no link.
      if (sent.mailSent) {
        toast.success(t("toasts.identityProofingRequestSent"));
      } else {
        toast.error(t("toasts.identityProofingMailNotSent"));
      }
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
        update.paused !== undefined
          ? update.paused
            ? t("toasts.identityProofingCustomerPaused")
            : t("toasts.identityProofingCustomerResumed")
          : update.name !== undefined
            ? t("toasts.identityProofingCustomerRenamed")
            : t("toasts.identityProofingCustomerSettingsSaved"),
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
): UseMutationResult<CreatedProofingApiKey, Error, { name: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name }) => createProofingApiKey(slug, customerId, name),
    gcTime: SECRET_MUTATION_GC_TIME_MS,
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: proofingApiKeysQueryKey(slug, customerId),
      });
      // A customer's hasApiKey follows its keys.
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
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
      // A customer's hasApiKey follows its keys.
      void queryClient.invalidateQueries({
        queryKey: proofingCustomersQueryKey(slug),
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
): UseQueryResult<WebhookDelivery[], Error> {
  return useQuery({
    queryKey: [...proofingWebhookQueryKey(slug, customerId), "deliveries"],
    queryFn: ({ signal }) =>
      getProofingWebhookDeliveries(slug, customerId, signal),
    enabled: slug !== "" && customerId !== "",
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
    gcTime: SECRET_MUTATION_GC_TIME_MS,
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
    gcTime: SECRET_MUTATION_GC_TIME_MS,
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
  request: { id: string; status: string },
): UseQueryResult<AuditEvent[], Error> {
  return useQuery({
    queryKey: [...proofingRequestsQueryKey(slug), request.id, "events"],
    queryFn: ({ signal }) => getProofingRequestEvents(slug, request.id, signal),
    enabled: slug !== "" && request.id !== "",
    refetchInterval: isProofingLive(request.status)
      ? REQUESTS_POLL_INTERVAL_MS
      : false,
  });
}

// A data request's matched sessions, for its review and, once decided, what
// the reviewer approved.
export function useProofingDataMatchesQuery(
  slug: string,
  requestId: string,
): UseQueryResult<ProofingDataMatch[], Error> {
  return useQuery({
    queryKey: [...proofingRequestsQueryKey(slug), requestId, "data-matches"],
    queryFn: ({ signal }) => getProofingDataMatches(slug, requestId, signal),
    enabled: slug !== "" && requestId !== "",
  });
}

// Reads a settled request's verified identity while an admin has its row
// open. Every read is audited, so it is never refetched on its own: its key
// sits outside every prefix the proofing mutations invalidate. The timeline
// then shows the read.
function proofingRequestResultQueryKey(
  slug: string,
  requestId: string,
): readonly string[] {
  return ["identity-proofing", "result", slug, requestId];
}

export function useProofingRequestResultQuery(
  slug: string,
  requestId: string,
  enabled: boolean,
): UseQueryResult<ProofingResult, Error> {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: proofingRequestResultQueryKey(slug, requestId),
    queryFn: ({ signal }) => getProofingRequestResult(slug, requestId, signal),
    enabled: enabled && slug !== "" && requestId !== "",
    staleTime: Infinity,
  });
  const readAt = query.dataUpdatedAt;
  useEffect(() => {
    if (readAt === 0) return;
    void queryClient.invalidateQueries({
      queryKey: [...proofingRequestsQueryKey(slug), requestId, "events"],
    });
  }, [queryClient, slug, requestId, readAt]);
  return query;
}

const PROOFING_PAUSES_KEY = ["identity-proofing", "pauses"] as const;

function proofingPauseQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "pause"];
}

export function useProofingPauseQuery(
  slug: string,
): UseQueryResult<ProofingPause, Error> {
  return useQuery({
    queryKey: proofingPauseQueryKey(slug),
    queryFn: ({ signal }) => getProofingPause(slug, signal),
    enabled: slug !== "",
  });
}

// Switches the org's proofing off or on; everything proofing refetches, as
// its routes answer again (or stop).
export function useSetProofingPauseMutation(
  slug: string,
): UseMutationResult<ProofingPause, Error, boolean> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (paused) => setProofingPause(slug, paused),
    onSuccess: (pause) => {
      queryClient.setQueryData(proofingPauseQueryKey(slug), pause);
      void queryClient.invalidateQueries({ queryKey: proofingQueryKey(slug) });
    },
  });
}

export function useProofingPausesQuery(): UseQueryResult<
  ProofingPause[],
  Error
> {
  return useQuery({
    queryKey: PROOFING_PAUSES_KEY,
    queryFn: ({ signal }) => listProofingPauses(signal),
  });
}

export function useSetPlatformProofingPauseMutation(): UseMutationResult<
  ProofingPause,
  Error,
  { orgId: string; paused: boolean }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ orgId, paused }) => setPlatformProofingPause(orgId, paused),
    // The list, and any org's proofing a platform admin has open.
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: PROOFING_PAUSES_KEY });
      await queryClient.invalidateQueries({ queryKey: ["organizations"] });
    },
  });
}

// A verify target's cache key: an org request's sits under the org's requests,
// so whatever refreshes those refreshes it.
function verifyQueryKey(target: VerifyTarget): readonly unknown[] {
  return target.kind === "request"
    ? [...proofingRequestsQueryKey(target.slug), target.requestId]
    : ["identity-proofing", "hosted", target.token];
}

// The progress a verify page follows, polled while the subject can still act.
export function useProofingProgressQuery(
  target: VerifyTarget,
): UseQueryResult<ProofingProgress, Error> {
  return useQuery<ProofingProgress>({
    queryKey: [...verifyQueryKey(target), "progress"],
    queryFn: ({ signal }) =>
      target.kind === "request"
        ? getProofingRequest(target.slug, target.requestId, signal)
        : getHostedProofingStatus(target.token, signal),
    refetchInterval: (query) =>
      progressPollContinues(query.state.data, query.state.error)
        ? ON_SCREEN_POLL_INTERVAL_MS
        : false,
  });
}

export function useStartProofingYiviMutation(
  target: VerifyTarget,
): UseMutationResult<ProofingYiviStart, Error, void> {
  return useMutation({
    mutationFn: () => startProofingYivi(target),
    meta: { suppressErrorToast: true },
  });
}

// Where an on-screen Idem request's phone is, polled while its session runs;
// each read asks the engine live.
export function useProofingAppQuery(
  slug: string,
  requestId: string,
  enabled: boolean,
): UseQueryResult<ProofingApp, Error> {
  return useQuery({
    queryKey: [...proofingRequestsQueryKey(slug), requestId, "app"],
    queryFn: async ({ signal }) =>
      (await getProofingApp(slug, requestId, signal)).app,
    enabled,
    refetchInterval: enabled ? ON_SCREEN_POLL_INTERVAL_MS : false,
  });
}

export function useNewProofingClaimLinkMutation(
  target: VerifyTarget,
): UseMutationResult<ProofingClaimLink, Error, void> {
  return useMutation({
    mutationFn: () => newProofingClaimLink(target),
    meta: { suppressErrorToast: true },
  });
}

// Polled until the subject has finished in the Yivi app or a poll fails (the
// page then shows the error); enabled only while the page shows the Yivi QR.
export function useProofingYiviDisclosureQuery(
  target: VerifyTarget,
  enabled: boolean,
): UseQueryResult<ProofingYiviDisclosure, Error> {
  return useQuery({
    queryKey: [...verifyQueryKey(target), "yivi"],
    queryFn: ({ signal }) => getProofingYiviDisclosure(target, signal),
    enabled,
    refetchInterval: (query) =>
      query.state.data?.done || query.state.status === "error"
        ? false
        : ON_SCREEN_POLL_INTERVAL_MS,
  });
}

export function useSubmitProofingFaceFrameMutation(
  target: VerifyTarget,
): UseMutationResult<ProofingFaceVerdict, Error, string> {
  return useMutation({
    mutationFn: (image) => submitProofingFaceFrame(target, image),
    meta: { suppressErrorToast: true },
  });
}

// Checks uploaded diploma extracts; each verdict says what became of a file.
export function useUploadProofingDiplomasMutation(
  target: VerifyTarget,
): UseMutationResult<DiplomaVerdict[], Error, File[]> {
  return useMutation({
    mutationFn: (files) => uploadProofingDiplomas(target, files),
    meta: { suppressErrorToast: true },
  });
}

// The hosted page a link opens, read once; its progress is polled apart.
export function useHostedProofingQuery(
  token: string,
): UseQueryResult<HostedProofing, Error> {
  return useQuery({
    queryKey: [...verifyQueryKey({ kind: "hosted", token }), "page"],
    queryFn: ({ signal }) => getHostedProofing(token, signal),
    enabled: token !== "",
    retry: false,
  });
}

export function useStartHostedProofingMutation(
  token: string,
): UseMutationResult<HostedStart, Error, ProofingMethod> {
  return useMutation({
    mutationFn: (method) => startHostedProofing(token, method),
    meta: { suppressErrorToast: true },
  });
}

// Declines a hosted link; the page's progress follows.
export function useDeclineHostedProofingMutation(
  token: string,
): UseMutationResult<HostedProgress, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => declineHostedProofing(token),
    onSuccess: (progress) => {
      queryClient.setQueryData(
        [...verifyQueryKey({ kind: "hosted", token }), "progress"],
        progress,
      );
    },
    meta: { suppressErrorToast: true },
  });
}

// Decides a request under review; the lists (and counts) follow.
export function useDecideProofingReviewMutation(
  slug: string,
  requestId: string,
): UseMutationResult<ProofingRequest, Error, ProofingReviewInput> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input) => decideProofingReview(slug, requestId, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: proofingRequestsQueryKey(slug),
      });
      // The result is read once and kept (staleTime Infinity); the decision
      // changed it, so the one read under review is no longer the outcome.
      void queryClient.invalidateQueries({
        queryKey: proofingRequestResultQueryKey(slug, requestId),
      });
    },
  });
}
