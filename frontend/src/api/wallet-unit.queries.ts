import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  activateWalletUnit,
  getWalletUnit,
  revokeWalletUnit,
} from "./wallet-unit";
import type {
  WalletUnit,
  WalletUnitRevokeReason,
  WalletUnitView,
} from "./wallet-unit";
import { toast } from "../lib/toast";

export function walletUnitQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "wallet-unit"];
}

export function useWalletUnitQuery(
  slug: string,
): UseQueryResult<WalletUnitView, Error> {
  return useQuery({
    queryKey: walletUnitQueryKey(slug),
    queryFn: ({ signal }) => getWalletUnit(slug, signal),
    enabled: slug !== "",
  });
}

export function useRevokeWalletUnitMutation(
  slug: string,
): UseMutationResult<WalletUnit, Error, { reason: WalletUnitRevokeReason }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ reason }) => revokeWalletUnit(slug, reason),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.walletUnitRevoked"));
      void queryClient.invalidateQueries({
        queryKey: walletUnitQueryKey(slug),
      });
    },
  });
}

export function useActivateWalletUnitMutation(
  slug: string,
): UseMutationResult<WalletUnit, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => activateWalletUnit(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.walletUnitActivated"));
      void queryClient.invalidateQueries({
        queryKey: walletUnitQueryKey(slug),
      });
    },
  });
}
