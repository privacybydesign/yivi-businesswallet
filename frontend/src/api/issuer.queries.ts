import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  getIssuerBundle,
  getIssuerSettings,
  updateIssuerSettings,
} from "./issuer";
import type {
  IssuerBundle,
  IssuerSettings,
  IssuerSettingsInput,
} from "./issuer";
import { toast } from "../lib/toast";

export function issuerSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "issuer", "settings"];
}

export function issuerBundleQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "issuer", "bundle"];
}

export function useIssuerSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<IssuerSettings, Error> {
  return useQuery({
    queryKey: issuerSettingsQueryKey(slug),
    queryFn: ({ signal }) => getIssuerSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateIssuerSettingsMutation(
  slug: string,
): UseMutationResult<IssuerSettings, Error, IssuerSettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateIssuerSettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.issuerSettingsSaved"));
      void queryClient.invalidateQueries({
        queryKey: issuerSettingsQueryKey(slug),
      });
      // The bundle's issuer name / branding derive from settings.
      void queryClient.invalidateQueries({
        queryKey: issuerBundleQueryKey(slug),
      });
    },
  });
}

// Fetched on demand — the panel enables it when the operator opens the bundle.
export function useIssuerBundleQuery(
  slug: string,
  enabled = true,
): UseQueryResult<IssuerBundle, Error> {
  return useQuery({
    queryKey: issuerBundleQueryKey(slug),
    queryFn: ({ signal }) => getIssuerBundle(slug, signal),
    enabled: enabled && slug !== "",
  });
}
