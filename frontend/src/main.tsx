import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import { RouterProvider } from "react-router";
import {
  MutationCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { router } from "./router";
import { setUnauthorizedHandler } from "./api/http";
import { meQueryKey } from "./api/auth.queries";
import { toast } from "./lib/toast";
import { Toaster } from "./ui";
import i18n from "./i18n";
import "./index.css";

const STALE_TIME_MS = 30_000;

// Retries are handled in the HTTP transport (idempotent GETs only), so disable
// TanStack Query's own retry to avoid compounding attempts.
const queryClient = new QueryClient({
  // Safety net: any mutation that fails surfaces a toast, so an action never
  // fails silently. Forms that show the error inline opt out via
  // `meta: { suppressErrorToast: true }`.
  mutationCache: new MutationCache({
    onError: (_error, _variables, _context, mutation) => {
      if (mutation.meta?.suppressErrorToast) {
        return;
      }
      toast.error(i18n.t("toasts.error"));
    },
  }),
  defaultOptions: {
    queries: {
      retry: false,
      staleTime: STALE_TIME_MS,
    },
  },
});

// On any 401, mark the session logged-out. We set the cache to null rather than
// invalidating: invalidating would refetch `me`, whose own 401 would re-fire
// this handler in an endless loop.
setUnauthorizedHandler(() => {
  queryClient.setQueryData(meQueryKey, null);
});

const root = document.getElementById("root");

ReactDOM.createRoot(root!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster />
    </QueryClientProvider>
  </StrictMode>,
);
