import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  UseMutationResult,
  UseQueryOptions,
  UseQueryResult,
} from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getOrgTheme, updateOrgTheme } from "./theme";
import type { OrgTheme, OrgThemeInput } from "./theme";
import {
  invalidateOrganizationLists,
  organizationAuditEventsQueryKey,
} from "./organization.queries";
import { brandingMailPreviewQueryKey } from "./identity-proofing.queries";
import { RARELY_CHANGING_STALE_TIME_MS } from "./stale-time";
import { toast } from "../lib/toast";

export function orgThemeQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "theme"];
}

// Shared by the hook and the route loader, so a prefetch fills the same entry.
export function orgThemeQueryOptions(slug: string): UseQueryOptions<OrgTheme> {
  return {
    queryKey: orgThemeQueryKey(slug),
    queryFn: ({ signal }) => getOrgTheme(slug, signal),
    staleTime: RARELY_CHANGING_STALE_TIME_MS,
  };
}

export function useOrgThemeQuery(
  slug: string,
  enabled = true,
): UseQueryResult<OrgTheme, Error> {
  return useQuery({
    ...orgThemeQueryOptions(slug),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateOrgThemeMutation(
  slug: string,
): UseMutationResult<OrgTheme, Error, OrgThemeInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateOrgTheme(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.themeSaved"));
      void queryClient.invalidateQueries({ queryKey: orgThemeQueryKey(slug) });
      // The org lists carry the logo's address, which changes with the theme.
      void invalidateOrganizationLists(queryClient);
      // A mail preview renders in the saved theme.
      void queryClient.invalidateQueries({
        queryKey: brandingMailPreviewQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}
