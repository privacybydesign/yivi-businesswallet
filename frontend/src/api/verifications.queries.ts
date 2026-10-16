import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { toast } from "../lib/toast";
import type {
  Verification,
  VerificationTemplate,
  VerificationTemplateInput,
} from "./verifications";
import {
  createVerificationTemplate,
  deleteVerificationTemplate,
  getVerification,
  getVerifications,
  getVerificationTemplates,
  startVerification,
} from "./verifications";

// A pending check polls the backend, which polls the hosted verifier; the same
// cadence the login QR uses.
const PENDING_POLL_INTERVAL_MS = 1500;

export function verificationTemplatesQueryKey(slug: string): readonly string[] {
  return ["organizations", slug, "verifications", "templates"];
}

export function verificationsQueryKey(slug: string): readonly string[] {
  return ["organizations", slug, "verifications"];
}

export function verificationQueryKey(
  slug: string,
  id: string,
): readonly string[] {
  return ["organizations", slug, "verifications", id];
}

export function useVerificationTemplatesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<VerificationTemplate[], Error> {
  return useQuery({
    queryKey: verificationTemplatesQueryKey(slug),
    queryFn: ({ signal }) => getVerificationTemplates(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useVerificationsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<Verification[], Error> {
  return useQuery({
    queryKey: verificationsQueryKey(slug),
    queryFn: ({ signal }) => getVerifications(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useVerificationQuery(
  slug: string,
  id: string,
): UseQueryResult<Verification, Error> {
  return useQuery({
    queryKey: verificationQueryKey(slug, id),
    queryFn: ({ signal }) => getVerification(slug, id, signal),
    enabled: slug !== "" && id !== "",
    refetchInterval: (query) =>
      query.state.data?.status === "pending" ? PENDING_POLL_INTERVAL_MS : false,
  });
}

export function useStartVerificationMutation(
  slug: string,
): UseMutationResult<Verification, Error, { templateId: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ templateId }) => startVerification(slug, templateId),
    onSuccess: (started) => {
      queryClient.setQueryData(verificationQueryKey(slug, started.id), started);
      void queryClient.invalidateQueries({
        queryKey: verificationsQueryKey(slug),
      });
    },
  });
}

export function useCreateVerificationTemplateMutation(
  slug: string,
): UseMutationResult<VerificationTemplate, Error, VerificationTemplateInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createVerificationTemplate(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.verificationTemplateCreated"));
      void queryClient.invalidateQueries({
        queryKey: verificationTemplatesQueryKey(slug),
      });
    },
  });
}

export function useDeleteVerificationTemplateMutation(
  slug: string,
): UseMutationResult<void, Error, { templateId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ templateId }) =>
      deleteVerificationTemplate(slug, templateId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.verificationTemplateDeleted"));
      void queryClient.invalidateQueries({
        queryKey: verificationTemplatesQueryKey(slug),
      });
    },
  });
}
