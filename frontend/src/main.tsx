import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import { RouterProvider } from "react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { router } from "./router";
import { queryClient } from "./query-client";
import { watchSystemColorScheme } from "./lib/color-scheme";
import { Toaster } from "./ui";
import { i18nReady } from "./i18n";
import "./index.css";

watchSystemColorScheme();

// A tab from before a deploy asks for chunks that no longer exist. One reload
// fetches the new index document; a chunk still missing right after one is
// missing for another reason, so it is left to the error page.
const CHUNK_RELOAD_KEY = "ybw.chunkReloadAt";
const CHUNK_RELOAD_PAUSE_MS = 60_000;
window.addEventListener("vite:preloadError", (event) => {
  try {
    const last = Number(sessionStorage.getItem(CHUNK_RELOAD_KEY) ?? 0);
    if (Date.now() - last < CHUNK_RELOAD_PAUSE_MS) {
      return;
    }
    sessionStorage.setItem(CHUNK_RELOAD_KEY, String(Date.now()));
  } catch {
    return; // no storage to guard the reload with
  }
  event.preventDefault();
  window.location.reload();
});

const root = document.getElementById("root");

function render(): void {
  ReactDOM.createRoot(root!).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
        <Toaster />
      </QueryClientProvider>
    </StrictMode>,
  );
}

// Render once the language's strings are in. If that chunk fails the app still
// renders, as an unreadable page beats a blank one.
void i18nReady.then(render, render);
