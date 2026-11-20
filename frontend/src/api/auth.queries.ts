import type {
  QueryClient,
  UseMutationResult,
  UseQueryResult,
} from "@tanstack/react-query";
import {
  hashKey,
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ApiError } from "./http";
import type { Me } from "./auth";
import { getMe, logout, removeMyAvatar, updateMyAvatar } from "./auth";
import { orgResourceQueries } from "./organization.queries";
import { toast } from "../lib/toast";

export const meQueryKey = ["me"] as const;

const UNAUTHORIZED_STATUS = 401;

// Shared by the hook and the route loader, so a prefetch fills the same entry.
export const meQueryOptions = queryOptions({
  queryKey: meQueryKey,
  queryFn: async ({ signal }): Promise<Me | null> => {
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

export function useMeQuery(): UseQueryResult<Me | null, Error> {
  return useQuery(meQueryOptions);
}

// useAvatarSettled writes the updated user straight into the `me` cache (both
// avatar endpoints return it, so no refetch is needed) and invalidates the
// member lists and audit logs of every org: they embed the same photo, and the
// avatar is per-user, so it appears under whichever org is being viewed.
function useAvatarSettled(): (me: Me) => void {
  const queryClient = useQueryClient();
  return (me) => {
    queryClient.setQueryData(meQueryKey, me);
    void queryClient.invalidateQueries({
      predicate: orgResourceQueries("members", "member", "audit-events"),
    });
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

// signOutCache leaves nothing the signed-out user read in memory for whoever
// uses the browser next, and makes "me" read signed out.
export function signOutCache(queryClient: QueryClient): void {
  // "me" is set, not removed: removing it leaves the screens that read it on
  // the old user, so the route guard never sends them to the login page.
  queryClient.setQueryData(meQueryKey, null);
  const meHash = hashKey(meQueryKey);
  queryClient.removeQueries({
    predicate: (query) => query.queryHash !== meHash,
  });
}

export function useLogoutMutation(): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => logout(),
    onSuccess: () => signOutCache(queryClient),
  });
}
