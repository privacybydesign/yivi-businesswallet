import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ApiError } from "./http";
import type { Me } from "./auth";
import { getMe, logout, removeMyAvatar, updateMyAvatar } from "./auth";
import { organizationsQueryKey } from "./organization.queries";
import { toast } from "../lib/toast";

export const meQueryKey = ["me"] as const;

const UNAUTHORIZED_STATUS = 401;

export function useMeQuery(): UseQueryResult<Me | null, Error> {
  return useQuery({
    queryKey: meQueryKey,
    queryFn: async ({ signal }) => {
      try {
        return await getMe(signal);
      } catch (error) {
        if (error instanceof ApiError && error.status === UNAUTHORIZED_STATUS) {
          return null;
        }
        throw error;
      }
    },
  });
}

// useAvatarSettled writes the updated user straight into the `me` cache (both
// avatar endpoints return it, so no refetch is needed) and invalidates every
// organization query: the member lists and the audit log embed the same photo,
// and the avatar is per-user, so it appears under whichever org is being viewed.
function useAvatarSettled(): (me: Me) => void {
  const queryClient = useQueryClient();
  return (me) => {
    queryClient.setQueryData(meQueryKey, me);
    void queryClient.invalidateQueries({ queryKey: organizationsQueryKey });
  };
}

export function useUpdateMyAvatarMutation(): UseMutationResult<
  Me,
  Error,
  File
> {
  const { t } = useTranslation();
  const settled = useAvatarSettled();
  return useMutation({
    mutationFn: (file: File) => updateMyAvatar(file),
    meta: { suppressErrorToast: true },
    onSuccess: (me) => {
      toast.success(t("toasts.avatarSaved"));
      settled(me);
    },
  });
}

export function useRemoveMyAvatarMutation(): UseMutationResult<
  Me,
  Error,
  void
> {
  const { t } = useTranslation();
  const settled = useAvatarSettled();
  return useMutation({
    mutationFn: () => removeMyAvatar(),
    meta: { suppressErrorToast: true },
    onSuccess: (me) => {
      toast.success(t("toasts.avatarRemoved"));
      settled(me);
    },
  });
}

export function useLogoutMutation(): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => logout(),
    onSuccess: () => {
      queryClient.setQueryData(meQueryKey, null);
    },
  });
}
