import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getCscSettings, testCscConnection, updateCscSettings } from "./csc";
import type { CscInfo, CscSettings, CscSettingsInput } from "./csc";
import { toast } from "../lib/toast";

export function cscSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "csc", "settings"];
}

export function useCscSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<CscSettings, Error> {
  return useQuery({
    queryKey: cscSettingsQueryKey(slug),
    queryFn: ({ signal }) => getCscSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateCscSettingsMutation(
  slug: string,
): UseMutationResult<CscSettings, Error, CscSettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateCscSettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.cscSettingsSaved"));
      void queryClient.invalidateQueries({
        queryKey: cscSettingsQueryKey(slug),
      });
    },
  });
}

export function useTestCscConnectionMutation(
  slug: string,
): UseMutationResult<CscInfo, Error, void> {
  return useMutation({
    mutationFn: () => testCscConnection(slug),
    meta: { suppressErrorToast: true },
  });
}
