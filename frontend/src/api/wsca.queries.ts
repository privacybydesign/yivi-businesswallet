import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { activateWsca, getWscaStatus, rotateWsca } from "./wsca";
import type { WscaAccount, WscaStatus } from "./wsca";
import { toast } from "../lib/toast";

export function wscaStatusQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "wsca", "status"];
}

export function useWscaStatusQuery(
  slug: string,
  enabled = true,
): UseQueryResult<WscaStatus, Error> {
  return useQuery({
    queryKey: wscaStatusQueryKey(slug),
    queryFn: ({ signal }) => getWscaStatus(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useActivateWscaMutation(
  slug: string,
): UseMutationResult<WscaAccount, Error, { secret: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ secret }) => activateWsca(slug, secret),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.wscaActivated"));
      void queryClient.invalidateQueries({
        queryKey: wscaStatusQueryKey(slug),
      });
    },
  });
}

export function useRotateWscaMutation(
  slug: string,
): UseMutationResult<
  WscaAccount,
  Error,
  { currentSecret: string; newSecret: string }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ currentSecret, newSecret }) =>
      rotateWsca(slug, currentSecret, newSecret),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.wscaRotated"));
      void queryClient.invalidateQueries({
        queryKey: wscaStatusQueryKey(slug),
      });
    },
  });
}
