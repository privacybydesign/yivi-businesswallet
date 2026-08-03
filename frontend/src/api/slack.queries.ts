import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  getSlackSettings,
  sendSlackTestNotification,
  updateSlackSettings,
} from "./slack";
import type { SlackSettings, SlackSettingsInput } from "./slack";
import { toast } from "../lib/toast";

export function slackSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "slack", "settings"];
}

export function useSlackSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<SlackSettings, Error> {
  return useQuery({
    queryKey: slackSettingsQueryKey(slug),
    queryFn: ({ signal }) => getSlackSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateSlackSettingsMutation(
  slug: string,
): UseMutationResult<SlackSettings, Error, SlackSettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateSlackSettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.slackSettingsSaved"));
      void queryClient.invalidateQueries({
        queryKey: slackSettingsQueryKey(slug),
      });
    },
  });
}

export function useSendSlackTestMutation(
  slug: string,
): UseMutationResult<void, Error, void> {
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => sendSlackTestNotification(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.slackTestSent"));
    },
  });
}
