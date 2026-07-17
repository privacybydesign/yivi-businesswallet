import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
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
  getIssuedAttestation,
  getIssuedAttestations,
  issueAttestation,
  revokeAttestationKey,
  revokeIssuedAttestation,
  suspendAttestationKey,
  updateAttestationSchema,
  updateAttestationTemplate,
} from "./attestations";
import type {
  AttestationClaim,
  AttestationIssuerConfig,
  AttestationKey,
  AttestationKeyInput,
  AttestationSchema,
  AttestationSchemaInput,
  AttestationSchemaUpdate,
  AttestationTemplate,
  AttestationTemplateInput,
  AttestationTemplateUpdate,
  HeldAttestation,
  IssuedAttestation,
  IssueAttestationInput,
  IssueResult,
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

export function heldAttestationsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "attestations", "held"];
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

export function useHeldAttestationsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<HeldAttestation[], Error> {
  return useQuery({
    queryKey: heldAttestationsQueryKey(slug),
    queryFn: ({ signal }) => getHeldAttestations(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useDeleteHeldAttestationMutation(
  slug: string,
): UseMutationResult<void, Error, { heldId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ heldId }) => deleteHeldAttestation(slug, heldId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.attestationHeldDeleted"));
      void queryClient.invalidateQueries({
        queryKey: heldAttestationsQueryKey(slug),
      });
    },
  });
}
