import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { DEFAULT_LANGUAGE, isSupportedLanguage } from "../i18n/language";
import {
  activateProofingFlowVersion,
  createProofingFlow,
  createProofingFlowVersion,
  createProofingRequest,
  getProofingSettings,
  getProofingFlowVersions,
  getProofingFlows,
  getProofingRequests,
  getProofingStats,
  setProofingFlowSelection,
  updateProofingSettings,
  createProofingApiKey,
  getProofingApiKeys,
  getProofingWebhook,
  getProofingWebhookDeliveries,
  removeProofingWebhook,
  revokeProofingApiKey,
  rotateProofingWebhookSecret,
  saveProofingBranding,
  saveProofingWebhook,
  sendProofingWebhookTest,
  getProofingRequestEvents,
  getProofingRequest,
  getProofingApp,
  getHostedProofing,
  decideProofingReview,
  getProofingDataMatches,
  getDataRequestFlows,
  getHostedProofingStatus,
  startHostedProofing,
  declineHostedProofing,
  getProofingFlowBranding,
  getProofingFlowHosted,
  saveProofingFlowBranding,
  saveProofingFlowHosted,
  saveProofingFlowDiplomas,
  saveProofingFlowAddress,
  getProofingPause,
  listProofingPauses,
  setPlatformProofingPause,
  getProofingRequestResult,
  getProofingYiviDisclosure,
  newProofingClaimLink,
  startProofingYivi,
  uploadProofingDiplomas,
  submitProofingAddress,
  lookUpAddress,
} from "./identity-proofing";
import type {
  ProofingSettings,
  ProofingSettingsUpdate,
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
  ProofingFlowBranding,
  ProofingFlowBrandingInput,
  ProofingFlowHosted,
  DiplomaMode,
  DiplomaVerdict,
  AddressMode,
  RegisterAddress,
  ProofingAddressInput,
  ProofingDataMatch,
} from "./identity-proofing";
import type { AuditEvent } from "./organization";
import { toast } from "../lib/toast";
import type { AddressLookup } from "../lib/identity-proofing";
import {
  deliveriesPollInterval,
  isProofingLive,
  progressPollContinues,
  reviewPollContinues,
} from "../lib/identity-proofing";

// Both reads reconcile live requests at the proofing service, so a request in
// flight is re-fetched until it settles. The recipient's own page polls faster:
// it is watching one phone finish.
const REQUESTS_POLL_INTERVAL_MS = 10_000;
// The on-screen page is watching one phone finish, so it re-reads its request
// and the Yivi disclosure much sooner.
const ON_SCREEN_POLL_INTERVAL_MS = 2_000;
// A session in review is decided by a person, or seconds later by its
// org's erasure setting, so a verify page re-reads it slowly.
const REVIEW_POLL_INTERVAL_MS = 15_000;
// A mutation whose answer carries a secret (a new API key, a webhook signing
// secret) is dropped from the cache as soon as nothing shows it, instead of
// lingering there for the default five minutes.
const SECRET_MUTATION_GC_TIME_MS = 0;
const IDENTITY_RESULT_GC_TIME_MS = 0;

export function proofingQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "identity-proofing"];
}

export function proofingFlowsQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "flows"];
}

// Every request list, the org's and each member's, sits under this key, so
// invalidating it refreshes them all.
export function proofingRequestsQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "requests"];
}

// Under the requests key: what refreshes the request lists refreshes the counts.
function proofingStatsQueryKey(slug: string): readonly string[] {
  return [...proofingRequestsQueryKey(slug), "stats"];
}

function proofingSettingsQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "settings"];
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

// See my data and Delete my data: admin-only, so only read when an admin
// picks one.
export function useDataRequestFlowsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<ProofingFlow[], Error> {
  return useQuery({
    queryKey: [...proofingFlowsQueryKey(slug), "data-requests"],
    queryFn: ({ signal }) => getDataRequestFlows(slug, signal),
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

export function useSaveFlowHostedMutation(
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

function proofingFlowBrandingQueryKey(
  slug: string,
  flowId: string,
): readonly string[] {
  return [...proofingFlowsQueryKey(slug), flowId, "branding"];
}

export function useProofingFlowBrandingQuery(
  slug: string,
  flowId: string,
): UseQueryResult<ProofingFlowBranding, Error> {
  return useQuery({
    queryKey: proofingFlowBrandingQueryKey(slug, flowId),
    queryFn: ({ signal }) => getProofingFlowBranding(slug, flowId, signal),
    enabled: slug !== "" && flowId !== "",
  });
}

export function useSaveFlowBrandingMutation(
  slug: string,
  flowId: string,
): UseMutationResult<ProofingFlowBranding, Error, ProofingFlowBrandingInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => saveProofingFlowBranding(slug, flowId, input),
    // The form shows a failure itself; the global toast would say it twice.
    meta: { suppressErrorToast: true },
    onSuccess: (saved) => {
      toast.success(t("toasts.identityProofingBrandingSaved"));
      queryClient.setQueryData(
        proofingFlowBrandingQueryKey(slug, flowId),
        saved,
      );
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
}

// The flow lists carry each flow's diploma mode, so they are refetched.
export function useSaveFlowDiplomasMutation(
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

// The flow lists carry each flow's address mode, so they are refetched.
export function useSaveFlowAddressMutation(
  slug: string,
): UseMutationResult<
  AddressMode,
  Error,
  { flowId: string; mode: AddressMode }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ flowId, mode }) =>
      saveProofingFlowAddress(slug, flowId, mode),
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

export function useActivateFlowMutation(
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
export function useSetFlowSelectionMutation(
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

// The sessions sent to external people, newest first: on any flow, or on the
// one flowId names, which the backend narrows to before its cap. Under the
// requests key, so everything that refreshes the org's requests refreshes
// these too.
export function useExternalRequestsQuery(
  slug: string,
  flowId = "",
  enabled = true,
): UseQueryResult<ProofingRequest[], Error> {
  return useQuery({
    queryKey: [...proofingRequestsQueryKey(slug), "external", flowId],
    queryFn: ({ signal }) =>
      getProofingRequests(slug, { subjectKind: "external", flowId }, signal),
    enabled: enabled && slug !== "",
    refetchInterval: (query) =>
      query.state.data?.some((r) => isProofingLive(r.status))
        ? REQUESTS_POLL_INTERVAL_MS
        : false,
  });
}

// The requests sent to one member, newest first. Under the requests key, so
// everything that refreshes the org's requests refreshes these too.
export function useMemberRequestsQuery(
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
export function useCreateRequestMutation(
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

export function useProofingSettingsQuery(
  slug: string,
): UseQueryResult<ProofingSettings, Error> {
  return useQuery({
    queryKey: proofingSettingsQueryKey(slug),
    queryFn: ({ signal }) => getProofingSettings(slug, signal),
    enabled: slug !== "",
  });
}

// The answer is the org's settings, so it replaces the cached ones.
export function useUpdateSettingsMutation(
  slug: string,
): UseMutationResult<ProofingSettings, Error, ProofingSettingsUpdate> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (update) => updateProofingSettings(slug, update),
    meta: { suppressErrorToast: true },
    onSuccess: (settings) => {
      toast.success(t("toasts.identityProofingSettingsSaved"));
      queryClient.setQueryData(proofingSettingsQueryKey(slug), settings);
      // A new retention changes what every flow tells its subjects.
      void queryClient.invalidateQueries({
        queryKey: proofingFlowsQueryKey(slug),
      });
    },
  });
}

function proofingApiKeysQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "api-keys"];
}

function proofingWebhookQueryKey(slug: string): readonly string[] {
  return [...proofingQueryKey(slug), "webhook"];
}

export function useSaveBrandingMutation(
  slug: string,
): UseMutationResult<ProofingSettings, Error, ProofingBrandingInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => saveProofingBranding(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: (settings) => {
      toast.success(t("toasts.identityProofingBrandingSaved"));
      queryClient.setQueryData(proofingSettingsQueryKey(slug), settings);
    },
  });
}

export function useProofingApiKeysQuery(
  slug: string,
): UseQueryResult<ProofingApiKey[], Error> {
  return useQuery({
    queryKey: proofingApiKeysQueryKey(slug),
    queryFn: ({ signal }) => getProofingApiKeys(slug, signal),
    enabled: slug !== "",
  });
}

// The secret is in the answer only; the caller shows it once.
export function useCreateApiKeyMutation(
  slug: string,
): UseMutationResult<CreatedProofingApiKey, Error, { name: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name }) => createProofingApiKey(slug, name),
    gcTime: SECRET_MUTATION_GC_TIME_MS,
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: proofingApiKeysQueryKey(slug),
      });
    },
  });
}

export function useRevokeApiKeyMutation(
  slug: string,
): UseMutationResult<ProofingApiKey, Error, string> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (keyId) => revokeProofingApiKey(slug, keyId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingApiKeyRevoked"));
      void queryClient.invalidateQueries({
        queryKey: proofingApiKeysQueryKey(slug),
      });
    },
  });
}

export function useProofingWebhookQuery(
  slug: string,
): UseQueryResult<ProofingWebhook, Error> {
  return useQuery({
    queryKey: proofingWebhookQueryKey(slug),
    queryFn: ({ signal }) => getProofingWebhook(slug, signal),
    enabled: slug !== "",
  });
}

export function useWebhookDeliveriesQuery(
  slug: string,
): UseQueryResult<WebhookDelivery[], Error> {
  return useQuery({
    queryKey: [...proofingWebhookQueryKey(slug), "deliveries"],
    queryFn: ({ signal }) => getProofingWebhookDeliveries(slug, signal),
    enabled: slug !== "",
    refetchInterval: (query) => deliveriesPollInterval(query.state.data),
  });
}

// Every webhook change refreshes the endpoint and its deliveries.
function invalidateWebhook(
  queryClient: ReturnType<typeof useQueryClient>,
  slug: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: proofingWebhookQueryKey(slug),
  });
}

// The answer carries a new endpoint's secret, once.
export function useSaveWebhookMutation(
  slug: string,
): UseMutationResult<
  ProofingWebhook,
  Error,
  { url: string; events: string[] }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => saveProofingWebhook(slug, input),
    gcTime: SECRET_MUTATION_GC_TIME_MS,
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingWebhookSaved"));
      invalidateWebhook(queryClient, slug);
    },
  });
}

export function useRemoveWebhookMutation(
  slug: string,
): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => removeProofingWebhook(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingWebhookRemoved"));
      invalidateWebhook(queryClient, slug);
    },
  });
}

export function useNewWebhookSecretMutation(
  slug: string,
): UseMutationResult<ProofingWebhook, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => rotateProofingWebhookSecret(slug),
    gcTime: SECRET_MUTATION_GC_TIME_MS,
    meta: { suppressErrorToast: true },
    onSuccess: () => invalidateWebhook(queryClient, slug),
  });
}

export function useSendWebhookTestMutation(
  slug: string,
): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => sendProofingWebhookTest(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.identityProofingWebhookTestQueued"));
      invalidateWebhook(queryClient, slug);
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
// then shows the read. It holds the person's identity and photos, so it is
// dropped from the cache as soon as no row shows it (gcTime 0), not kept for
// the default five minutes.
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
    gcTime: IDENTITY_RESULT_GC_TIME_MS,
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

export function useProofingPausesQuery(): UseQueryResult<
  ProofingPause[],
  Error
> {
  return useQuery({
    queryKey: PROOFING_PAUSES_KEY,
    queryFn: ({ signal }) => listProofingPauses(signal),
  });
}

export function useSetPlatformPauseMutation(): UseMutationResult<
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
        : reviewPollContinues(query.state.data, query.state.error)
          ? REVIEW_POLL_INTERVAL_MS
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

export function useNewClaimLinkMutation(
  target: VerifyTarget,
): UseMutationResult<ProofingClaimLink, Error, void> {
  return useMutation({
    mutationFn: () => newProofingClaimLink(target),
    meta: { suppressErrorToast: true },
  });
}

// Polled until the subject has finished in the Yivi app or a poll fails (the
// page then shows the error); enabled only while the page shows the Yivi QR.
export function useYiviDisclosureQuery(
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

// Checks uploaded diploma extracts; each verdict says what became of a file.
export function useUploadDiplomasMutation(
  target: VerifyTarget,
): UseMutationResult<DiplomaVerdict[], Error, File[]> {
  return useMutation({
    mutationFn: (files) => uploadProofingDiplomas(target, files),
    meta: { suppressErrorToast: true },
  });
}

// The Dutch addresses at a postal code and house number; asked only for a
// Dutch postal code and a house number that starts with a number.
export function useAddressLookupQuery(
  target: VerifyTarget,
  lookup: AddressLookup | null,
): UseQueryResult<RegisterAddress[], Error> {
  return useQuery<RegisterAddress[]>({
    queryKey: [
      ...verifyQueryKey(target),
      "address-lookup",
      lookup?.postalCode,
      lookup?.houseNumber,
    ],
    queryFn: ({ signal }) =>
      lookUpAddress(
        target,
        lookup?.postalCode ?? "",
        lookup?.houseNumber ?? "",
        signal,
      ),
    enabled: lookup !== null,
    staleTime: Infinity,
  });
}

// Keeps the postal address entered on a verify page; the page's progress is
// refetched, so the step closes.
export function useSubmitAddressMutation(
  target: VerifyTarget,
): UseMutationResult<void, Error, ProofingAddressInput> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (address) => submitProofingAddress(target, address),
    meta: { suppressErrorToast: true },
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: verifyQueryKey(target) }),
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

export function useStartHostedMutation(
  token: string,
): UseMutationResult<HostedStart, Error, ProofingMethod> {
  return useMutation({
    mutationFn: (method) => startHostedProofing(token, method),
    meta: { suppressErrorToast: true },
  });
}

// Declines a hosted link; the page's progress follows.
export function useDeclineHostedMutation(
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
export function useDecideReviewMutation(
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
