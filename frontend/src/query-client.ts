import { MutationCache, QueryClient } from "@tanstack/react-query";
import { setUnauthorizedHandler } from "./api/http";
import { meQueryKey } from "./api/auth.queries";
import { DEFAULT_STALE_TIME_MS } from "./api/stale-time";
import { walletNotActive } from "./lib/api-error";
import { toast } from "./lib/toast";
import i18n from "./i18n";

// Retries are handled in the HTTP transport (idempotent GETs only), so disable
// TanStack Query's own retry to avoid compounding attempts.
export const queryClient = new QueryClient({
  // Safety net: any mutation that fails surfaces a toast, so an action never
  // fails silently. Forms that show the error inline opt out via
  // `meta: { suppressErrorToast: true }`.
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => {
      if (mutation.meta?.suppressErrorToast) {
        return;
      }
      toast.error(
        i18n.t(
          walletNotActive(error) ? "toasts.walletNotActive" : "toasts.error",
        ),
      );
    },
  }),
  defaultOptions: {
    queries: {
      retry: false,
      staleTime: DEFAULT_STALE_TIME_MS,
    },
  },
});

// On any 401, mark the session logged-out. We set the cache to null rather than
// invalidating: invalidating would refetch `me`, whose own 401 would re-fire
// this handler in an endless loop.
setUnauthorizedHandler(() => {
  queryClient.setQueryData(meQueryKey, null);
});
