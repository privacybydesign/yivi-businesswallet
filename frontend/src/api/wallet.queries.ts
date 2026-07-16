import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "./http";
import type { WalletEnrollment, WalletInstance } from "./wallet";
import { enrollWallet, getOrgWallet } from "./wallet";
import {
  myOrganizationsQueryKey,
  organizationQueryKey,
  organizationsQueryKey,
} from "./organization.queries";

const NOT_FOUND_STATUS = 404;

// useOrgWalletQuery loads an org's business wallet, or null when the org has no
// wallet yet (a plain org created by a platform admin, not via registration).
export function useOrgWalletQuery(
  slug: string,
): UseQueryResult<WalletInstance | null, Error> {
  return useQuery({
    queryKey: [...organizationQueryKey(slug), "wallet"],
    enabled: slug !== "",
    queryFn: async ({ signal }) => {
      try {
        return await getOrgWallet(slug, signal);
      } catch (error) {
        if (error instanceof ApiError && error.status === NOT_FOUND_STATUS) {
          return null;
        }
        throw error;
      }
    },
  });
}

// useEnrollWalletMutation registers a wallet for a KVK number. Errors are handled
// inline by the enrollment screen (e.g. the not-a-representative 403), so the
// global error toast is suppressed.
export function useEnrollWalletMutation(): UseMutationResult<
  WalletEnrollment,
  Error,
  string
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (kvkNumber: string) => enrollWallet(kvkNumber),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      // A new org changed the user's memberships; refresh the org lists.
      void queryClient.invalidateQueries({ queryKey: myOrganizationsQueryKey });
      void queryClient.invalidateQueries({ queryKey: organizationsQueryKey });
    },
  });
}
