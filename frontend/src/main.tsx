import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import { RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { router } from "./router";
import { setUnauthorizedHandler } from "./api/http";
import { meQueryKey } from "./api/auth.queries";

const STALE_TIME_MS = 30_000;

// Retries are handled in the HTTP transport (idempotent GETs only), so disable
// TanStack Query's own retry to avoid compounding attempts.
const queryClient = new QueryClient({
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
    </QueryClientProvider>
  </StrictMode>,
);
