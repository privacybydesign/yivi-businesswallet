import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getOrgTheme, updateOrgTheme } from "./theme";
import type { OrgTheme, OrgThemeInput } from "./theme";
import { organizationAuditEventsQueryKey } from "./organization.queries";
import { toast } from "../lib/toast";

export function orgThemeQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "theme"];
}

export function useOrgThemeQuery(
  slug: string,
  enabled = true,
): UseQueryResult<OrgTheme, Error> {
  return useQuery({
    queryKey: orgThemeQueryKey(slug),
    queryFn: ({ signal }) => getOrgTheme(slug, signal),
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
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}
