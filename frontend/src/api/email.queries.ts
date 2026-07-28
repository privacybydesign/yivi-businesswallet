import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  getEmailSettings,
  getMailTemplate,
  getMailTemplates,
  previewMailTemplate,
  resetMailTemplate,
  sendTestEmail,
  updateEmailSettings,
  updateMailTemplate,
} from "./email";
import type {
  EmailSettings,
  EmailSettingsInput,
  MailPreview,
  MailTemplate,
  MailTemplateDetail,
  MailTemplateList,
  MailTemplateRef,
  TestEmailInput,
} from "./email";
import { toast } from "../lib/toast";

export function emailSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "email", "settings"];
}

export function useEmailSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<EmailSettings, Error> {
  return useQuery({
    queryKey: emailSettingsQueryKey(slug),
    queryFn: ({ signal }) => getEmailSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateEmailSettingsMutation(
  slug: string,
): UseMutationResult<EmailSettings, Error, EmailSettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateEmailSettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.emailSettingsSaved"));
      void queryClient.invalidateQueries({
        queryKey: emailSettingsQueryKey(slug),
      });
    },
  });
}

export function useSendTestEmailMutation(
  slug: string,
): UseMutationResult<void, Error, TestEmailInput> {
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => sendTestEmail(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.emailTestSent"));
    },
  });
}

export function mailTemplatesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "email", "templates"];
}

export function mailTemplateQueryKey(
  slug: string,
  ref: MailTemplateRef,
): readonly string[] {
  return [...mailTemplatesQueryKey(slug), ref.kind, ref.locale];
}

export function useMailTemplatesQuery(
  slug: string,
): UseQueryResult<MailTemplateList, Error> {
  return useQuery({
    queryKey: mailTemplatesQueryKey(slug),
    queryFn: ({ signal }) => getMailTemplates(slug, signal),
    enabled: slug !== "",
  });
}

export function useMailTemplateQuery(
  slug: string,
  ref: MailTemplateRef | null,
): UseQueryResult<MailTemplateDetail, Error> {
  return useQuery({
    // The key is only read when ref is set, which `enabled` guarantees.
    queryKey: mailTemplateQueryKey(
      slug,
      ref ?? { kind: "smtp_test", locale: "en" },
    ),
    queryFn: ({ signal }) => getMailTemplate(slug, ref!, signal),
    enabled: slug !== "" && ref !== null,
  });
}

// Saving or reverting changes both the matrix (the customised badges) and the
// template itself, so both are invalidated.
function invalidateTemplate(
  queryClient: ReturnType<typeof useQueryClient>,
  slug: string,
  ref: MailTemplateRef,
): void {
  void queryClient.invalidateQueries({ queryKey: mailTemplatesQueryKey(slug) });
  void queryClient.invalidateQueries({
    queryKey: mailTemplateQueryKey(slug, ref),
  });
}

export function useUpdateMailTemplateMutation(
  slug: string,
  ref: MailTemplateRef,
): UseMutationResult<MailTemplateDetail, Error, MailTemplate> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (template) => updateMailTemplate(slug, ref, template),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.mailTemplateSaved"));
      invalidateTemplate(queryClient, slug, ref);
    },
  });
}

export function useResetMailTemplateMutation(
  slug: string,
  ref: MailTemplateRef,
): UseMutationResult<MailTemplateDetail, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => resetMailTemplate(slug, ref),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.mailTemplateReset"));
      invalidateTemplate(queryClient, slug, ref);
    },
  });
}

// The preview is a mutation rather than a query: it renders whatever draft the
// editor currently holds, so it is triggered explicitly and never cached.
export function usePreviewMailTemplateMutation(
  slug: string,
  ref: MailTemplateRef,
): UseMutationResult<MailPreview, Error, MailTemplate | null> {
  return useMutation({
    mutationFn: (template) => previewMailTemplate(slug, ref, template),
    meta: { suppressErrorToast: true },
  });
}
