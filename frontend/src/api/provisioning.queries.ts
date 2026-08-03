import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  getProvisioningSettings,
  runProvisioningSync,
  updateProvisioningSettings,
} from "./provisioning";
import type {
  ProvisioningSettings,
  ProvisioningSettingsInput,
  ProvisioningSyncResult,
} from "./provisioning";
import {
  organizationAuditEventsQueryKey,
  organizationMembersQueryKey,
} from "./organization.queries";
import { toast } from "../lib/toast";

export function provisioningSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "provisioning", "settings"];
}

export function useProvisioningSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<ProvisioningSettings, Error> {
  return useQuery({
    queryKey: provisioningSettingsQueryKey(slug),
    queryFn: ({ signal }) => getProvisioningSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useUpdateProvisioningSettingsMutation(
  slug: string,
): UseMutationResult<ProvisioningSettings, Error, ProvisioningSettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateProvisioningSettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.provisioningSettingsSaved"));
      void queryClient.invalidateQueries({
        queryKey: provisioningSettingsQueryKey(slug),
      });
    },
  });
}

export function useRunProvisioningSyncMutation(
  slug: string,
): UseMutationResult<ProvisioningSyncResult, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => runProvisioningSync(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.provisioningSyncCompleted"));
      // A run updates the last-run status, invites/removes members, and records
      // audit events — refresh all three so the settings panel, member list and
      // audit log reflect what just happened.
      void queryClient.invalidateQueries({
        queryKey: provisioningSettingsQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}
