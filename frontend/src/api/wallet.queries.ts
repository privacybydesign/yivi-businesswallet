import type { UseMutationResult } from "@tanstack/react-query";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { WalletEnrollment } from "./wallet";
import { enrollWallet } from "./wallet";
import {
  myOrganizationsQueryKey,
  organizationsQueryKey,
} from "./organization.queries";

// useEnrollWalletMutation registers a wallet (organization) for a KVK number under
// a chosen slug. Errors are handled inline by the enrollment screen (e.g. the
// not-a-representative 403), so the global error toast is suppressed.
export function useEnrollWalletMutation(): UseMutationResult<
  WalletEnrollment,
  Error,
  { kvkNumber: string; slug: string }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input) => enrollWallet(input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      // A new org changed the user's memberships; refresh the org lists.
      void queryClient.invalidateQueries({ queryKey: myOrganizationsQueryKey });
      void queryClient.invalidateQueries({ queryKey: organizationsQueryKey });
    },
  });
}
