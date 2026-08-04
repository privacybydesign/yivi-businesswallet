import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  cancelIssuedAttestation,
  createAttestationKey,
  createAttestationSchema,
  createAttestationTemplate,
  deleteAttestationSchema,
  deleteAttestationTemplate,
  deleteHeldAttestation,
  getAttestationClaim,
  getAttestationKeys,
  getAttestationSchemaIssuerConfig,
  getAttestationSchemas,
  getAttestationTemplates,
  getHeldAttestations,
  getHeldAttestationClaims,
  getIssuedAttestation,
  getIssuedAttestations,
  getOnboardingAttestations,
  issueAttestation,
  setOnboardingAttestations,
  revokeAttestationKey,
  revokeIssuedAttestation,
  suspendAttestationKey,
  updateAttestationSchema,
  updateAttestationTemplate,
  uploadAttestationSchemaLogo,
} from "./attestations";
import type {
  AttestationClaim,
  AttestationIssuerConfig,
  AttestationKey,
  AttestationKeyInput,
  AttestationSchema,
  AttestationSchemaInput,
  AttestationSchemaUpdate,
  SchemaLogoChange,
  AttestationTemplate,
  AttestationTemplateInput,
  AttestationTemplateUpdate,
  HeldAttestation,
  HeldAttestationClaims,
  IssuedAttestation,
  IssueAttestationInput,
  IssueResult,
  OnboardingAttestation,
} from "./attestations";
import { toast } from "../lib/toast";

// The ledger reconciles status on read, so an offered attestation is re-fetched
// on this interval until the recipient claims it.
const OFFERED_POLL_INTERVAL_MS = 2000;
const OFFERED_STATUS = "offered";

export function attestationSchemasQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "schemas"];
}

export function attestationSchemaIssuerConfigQueryKey(
  slug: string,
  schemaId: string,
): readonly string[] {
  return [
    "organizations",
    "detail",
    slug,
    "attestations",
    "schemas",
    schemaId,
    "issuer-config",
  ];
}

export function attestationTemplatesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "templates"];
}

export function attestationKeysQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "keys"];
}

export function onboardingAttestationsQueryKey(
  slug: string,
): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "onboarding"];
}

export function issuedAttestationsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "issued"];
}

export function issuedAttestationQueryKey(
  slug: string,
  issuedId: string,
): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "issued", issuedId];
}

export function attestationClaimQueryKey(token: string): readonly string[] {
  return ["attestations", "claim", token];
}

export function heldAttestationsQueryKey(
  slug: string,
  lang: string,
): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "held", lang];
}

export function heldAttestationClaimsQueryKey(
  slug: string,
  heldId: string,
  lang: string,
): readonly string[] {
  return [
    "organizations",
    "detail",
    slug,
    "attestations",
    "held",
    heldId,
    lang,
  ];
}

// Public claim polling: re-fetches while the attestation is still offered so the
// page flips to a success state once the recipient claims it.
export function useAttestationClaimQuery(
  token: string,
): UseQueryResult<AttestationClaim, Error> {
  return useQuery({
    queryKey: attestationClaimQueryKey(token),
    queryFn: ({ signal }) => getAttestationClaim(token, signal),
    enabled: token !== "",
    refetchInterval: (query) =>
      query.state.data?.status === OFFERED_STATUS
        ? OFFERED_POLL_INTERVAL_MS
        : false,
  });
}

export function useAttestationSchemasQuery(
  slug: string,
  enabled = true,
): UseQueryResult<AttestationSchema[], Error> {
  return useQuery({
    queryKey: attestationSchemasQueryKey(slug),
    queryFn: ({ signal }) => getAttestationSchemas(slug, signal),
    enabled: enabled && slug !== "",
  });
}

// Fetches the Veramo issuer GitOps config generated from a schema (metadata
// fragment + VCT document). Fetched on demand — the schema editor enables it when
// the operator opens the "issuer config" section.
export function useAttestationSchemaIssuerConfigQuery(
  slug: string,
  schemaId: string,
  enabled = true,
): UseQueryResult<AttestationIssuerConfig, Error> {
  return useQuery({
    queryKey: attestationSchemaIssuerConfigQueryKey(slug, schemaId),
    queryFn: ({ signal }) =>
      getAttestationSchemaIssuerConfig(slug, schemaId, signal),
    enabled: enabled && slug !== "" && schemaId !== "",
  });
}

export function useAttestationTemplatesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<AttestationTemplate[], Error> {
  return useQuery({
    queryKey: attestationTemplatesQueryKey(slug),
    queryFn: ({ signal }) => getAttestationTemplates(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useOnboardingAttestationsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<OnboardingAttestation[], Error> {
  return useQuery({
    queryKey: onboardingAttestationsQueryKey(slug),
    queryFn: ({ signal }) => getOnboardingAttestations(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useSetOnboardingAttestationsMutation(
  slug: string,
): UseMutationResult<
  OnboardingAttestation[],
  Error,
  { templateIds: string[] }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ templateIds }) =>
      setOnboardingAttestations(slug, templateIds),
    meta: { suppressErrorToast: true },
    onSuccess: (set) => {
      toast.success(t("toasts.onboardingAttestationsUpdated"));
      queryClient.setQueryData(onboardingAttestationsQueryKey(slug), set);
    },
  });
}

export function useAttestationKeysQuery(
  slug: string,
  enabled = true,
): UseQueryResult<AttestationKey[], Error> {
  return useQuery({
    queryKey: attestationKeysQueryKey(slug),
    queryFn: ({ signal }) => getAttestationKeys(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useIssuedAttestationsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<IssuedAttestation[], Error> {
  return useQuery({
    queryKey: issuedAttestationsQueryKey(slug),
    queryFn: ({ signal }) => getIssuedAttestations(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useIssuedAttestationQuery(
  slug: string,
  issuedId: string,
  enabled = true,
): UseQueryResult<IssuedAttestation, Error> {
  return useQuery({
    queryKey: issuedAttestationQueryKey(slug, issuedId),
    queryFn: ({ signal }) => getIssuedAttestation(slug, issuedId, signal),
    enabled: enabled && slug !== "" && issuedId !== "",
    refetchInterval: (query) =>
      query.state.data?.status === OFFERED_STATUS
        ? OFFERED_POLL_INTERVAL_MS
        : false,
  });
}

export function useCreateAttestationSchemaMutation(
  slug: string,
): UseMutationResult<AttestationSchema, Error, AttestationSchemaInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createAttestationSchema(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationSchemaCreated"));
      void queryClient.invalidateQueries({
        queryKey: attestationSchemasQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: attestationTemplatesQueryKey(slug),
      });
    },
  });
}

export function useUpdateAttestationSchemaMutation(
  slug: string,
): UseMutationResult<
  AttestationSchema,
  Error,
  { schemaId: string; input: AttestationSchemaUpdate }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ schemaId, input }) =>
      updateAttestationSchema(slug, schemaId, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationSchemaUpdated"));
      void queryClient.invalidateQueries({
        queryKey: attestationSchemasQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: attestationTemplatesQueryKey(slug),
      });
    },
  });
}

// Uploads or clears a schema's credential image. The parent schema mutation
// already toasts on save, so this one is silent — it only refreshes the schemas
// query and the schema's issuer config so the preview reflects the new image.
export function useUploadAttestationSchemaLogoMutation(
  slug: string,
): UseMutationResult<
  AttestationSchema,
  Error,
  { schemaId: string; change: Exclude<SchemaLogoChange, "keep"> }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ schemaId, change }) =>
      uploadAttestationSchemaLogo(slug, schemaId, change),
    meta: { suppressErrorToast: true },
    onSuccess: (_data, { schemaId }) => {
      void queryClient.invalidateQueries({
        queryKey: attestationSchemasQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: attestationSchemaIssuerConfigQueryKey(slug, schemaId),
      });
    },
  });
}

export function useDeleteAttestationSchemaMutation(
  slug: string,
): UseMutationResult<void, Error, { schemaId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ schemaId }) => deleteAttestationSchema(slug, schemaId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationSchemaDeleted"));
      void queryClient.invalidateQueries({
        queryKey: attestationSchemasQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: attestationTemplatesQueryKey(slug),
      });
    },
  });
}

export function useCreateAttestationTemplateMutation(
  slug: string,
): UseMutationResult<AttestationTemplate, Error, AttestationTemplateInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createAttestationTemplate(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationTemplateCreated"));
      void queryClient.invalidateQueries({
        queryKey: attestationTemplatesQueryKey(slug),
      });
    },
  });
}

export function useUpdateAttestationTemplateMutation(
  slug: string,
): UseMutationResult<
  AttestationTemplate,
  Error,
  { templateId: string; input: AttestationTemplateUpdate }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ templateId, input }) =>
      updateAttestationTemplate(slug, templateId, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationTemplateUpdated"));
      void queryClient.invalidateQueries({
        queryKey: attestationTemplatesQueryKey(slug),
      });
    },
  });
}

export function useDeleteAttestationTemplateMutation(
  slug: string,
): UseMutationResult<void, Error, { templateId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ templateId }) => deleteAttestationTemplate(slug, templateId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationTemplateDeleted"));
      void queryClient.invalidateQueries({
        queryKey: attestationTemplatesQueryKey(slug),
      });
    },
  });
}

export function useCreateAttestationKeyMutation(
  slug: string,
): UseMutationResult<AttestationKey, Error, AttestationKeyInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createAttestationKey(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationKeyCreated"));
      void queryClient.invalidateQueries({
        queryKey: attestationKeysQueryKey(slug),
      });
    },
  });
}

export function useSuspendAttestationKeyMutation(
  slug: string,
): UseMutationResult<AttestationKey, Error, { keyId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ keyId }) => suspendAttestationKey(slug, keyId),
    onSuccess: () => {
      toast.success(t("toasts.attestationKeySuspended"));
      void queryClient.invalidateQueries({
        queryKey: attestationKeysQueryKey(slug),
      });
    },
  });
}

export function useRevokeAttestationKeyMutation(
  slug: string,
): UseMutationResult<AttestationKey, Error, { keyId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ keyId }) => revokeAttestationKey(slug, keyId),
    onSuccess: () => {
      toast.success(t("toasts.attestationKeyRevoked"));
      void queryClient.invalidateQueries({
        queryKey: attestationKeysQueryKey(slug),
      });
    },
  });
}

export function useIssueAttestationMutation(
  slug: string,
): UseMutationResult<IssueResult, Error, IssueAttestationInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => issueAttestation(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationIssued"));
      void queryClient.invalidateQueries({
        queryKey: issuedAttestationsQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: attestationTemplatesQueryKey(slug),
      });
    },
  });
}

export function useRevokeIssuedAttestationMutation(
  slug: string,
): UseMutationResult<IssuedAttestation, Error, { issuedId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ issuedId }) => revokeIssuedAttestation(slug, issuedId),
    meta: { suppressErrorToast: true },
    onSuccess: (_data, { issuedId }) => {
      toast.success(t("toasts.attestationRevoked"));
      void queryClient.invalidateQueries({
        queryKey: issuedAttestationsQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: issuedAttestationQueryKey(slug, issuedId),
      });
    },
  });
}

export function useCancelIssuedAttestationMutation(
  slug: string,
): UseMutationResult<IssuedAttestation, Error, { issuedId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ issuedId }) => cancelIssuedAttestation(slug, issuedId),
    meta: { suppressErrorToast: true },
    onSuccess: (_data, { issuedId }) => {
      toast.success(t("toasts.attestationOfferCancelled"));
      void queryClient.invalidateQueries({
        queryKey: issuedAttestationsQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: issuedAttestationQueryKey(slug, issuedId),
      });
    },
  });
}

export function useHeldAttestationsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<HeldAttestation[], Error> {
  const { i18n } = useTranslation();
  const lang = i18n.language;
  return useQuery({
    queryKey: heldAttestationsQueryKey(slug, lang),
    queryFn: ({ signal }) => getHeldAttestations(slug, lang, signal),
    enabled: enabled && slug !== "",
  });
}

// Fetches a held credential's disclosed attributes on demand — the Wallet tab
// enables it when the user opens a credential's detail view.
export function useHeldAttestationClaimsQuery(
  slug: string,
  heldId: string,
  enabled = true,
): UseQueryResult<HeldAttestationClaims, Error> {
  const { i18n } = useTranslation();
  const lang = i18n.language;
  return useQuery({
    queryKey: heldAttestationClaimsQueryKey(slug, heldId, lang),
    queryFn: ({ signal }) =>
      getHeldAttestationClaims(slug, heldId, lang, signal),
    enabled: enabled && slug !== "" && heldId !== "",
  });
}

export function useDeleteHeldAttestationMutation(
  slug: string,
): UseMutationResult<void, Error, { heldId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ heldId }) => deleteHeldAttestation(slug, heldId),
    onSuccess: () => {
      toast.success(t("toasts.attestationHeldDeleted"));
      // Prefix match (no language) invalidates the held list across every cached
      // language, not just the active one.
      void queryClient.invalidateQueries({
        queryKey: ["organizations", "detail", slug, "attestations", "held"],
      });
    },
  });
}
