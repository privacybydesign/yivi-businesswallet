import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getEmailSettings, sendTestEmail, updateEmailSettings } from "./email";
import type { EmailSettings, EmailSettingsInput } from "./email";
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
): UseMutationResult<void, Error, { to: string }> {
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => sendTestEmail(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.emailTestSent"));
    },
  });
}
