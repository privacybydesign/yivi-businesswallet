import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  getNotificationSettings,
  updateNotificationSettings,
} from "./notifications";
import type {
  NotificationSettings,
  NotificationSettingsInput,
} from "./notifications";
import { toast } from "../lib/toast";

export function notificationSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "notifications", "settings"];
}

export function useNotificationSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<NotificationSettings, Error> {
  return useQuery({
    queryKey: notificationSettingsQueryKey(slug),
    queryFn: ({ signal }) => getNotificationSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateNotificationSettingsMutation(
  slug: string,
): UseMutationResult<NotificationSettings, Error, NotificationSettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateNotificationSettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.notificationSettingsSaved"));
      void queryClient.invalidateQueries({
        queryKey: notificationSettingsQueryKey(slug),
      });
    },
  });
}
