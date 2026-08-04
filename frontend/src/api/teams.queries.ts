import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  getTeamsSettings,
  sendTeamsTestNotification,
  updateTeamsSettings,
} from "./teams";
import type { TeamsSettings, TeamsSettingsInput } from "./teams";
import { toast } from "../lib/toast";

export function teamsSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "msteams", "settings"];
}

export function useTeamsSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<TeamsSettings, Error> {
  return useQuery({
    queryKey: teamsSettingsQueryKey(slug),
    queryFn: ({ signal }) => getTeamsSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateTeamsSettingsMutation(
  slug: string,
): UseMutationResult<TeamsSettings, Error, TeamsSettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateTeamsSettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.teamsSettingsSaved"));
      void queryClient.invalidateQueries({
        queryKey: teamsSettingsQueryKey(slug),
      });
    },
  });
}

export function useSendTeamsTestMutation(
  slug: string,
): UseMutationResult<void, Error, void> {
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => sendTeamsTestNotification(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.teamsTestSent"));
    },
  });
}
