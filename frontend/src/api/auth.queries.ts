import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "./http";
import type { Me } from "./auth";
import { getMe, logout } from "./auth";

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

export function useLogoutMutation(): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => logout(),
    onSuccess: () => {
      queryClient.setQueryData(meQueryKey, null);
    },
  });
}
