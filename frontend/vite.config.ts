import { defineConfig } from "vite";
import type { Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { DEFAULT_LANGUAGE, LANGUAGE_STORAGE_KEY } from "./src/i18n/language";

const LOCALE_MODULE = /\/i18n\/locales\/(\w+)\.ts$/;

// Each language is its own chunk (src/i18n/index.ts), which the app would only
// ask for once the entry script runs. This adds a preload for the visitor's
// language to index.html, so it downloads next to the entry script. It picks
// the language as resolveInitialLanguage (src/i18n/language.ts) does.
function preloadLocale(): Plugin {
  return {
    name: "preload-locale",
    apply: "build",
    transformIndexHtml: {
      order: "post",
      handler(_html, ctx) {
        const files: Record<string, string> = {};
        for (const chunk of Object.values(ctx.bundle ?? {})) {
          const locale =
            chunk.type === "chunk" && chunk.facadeModuleId
              ? LOCALE_MODULE.exec(chunk.facadeModuleId)
              : null;
          if (locale) {
            files[locale[1]] = chunk.fileName;
          }
        }
        const script = `(function () {
  var files = ${JSON.stringify(files)};
  var lang = null;
  try { lang = localStorage.getItem(${JSON.stringify(LANGUAGE_STORAGE_KEY)}); } catch (e) { /* no stored choice */ }
  if (!files[lang]) {
    lang = null;
    var preferred = navigator.languages || [navigator.language];
    for (var i = 0; i < preferred.length && !lang; i++) {
      var base = String(preferred[i]).toLowerCase().split("-")[0];
      if (files[base]) lang = base;
    }
  }
  var link = document.createElement("link");
  link.rel = "modulepreload";
  link.href = "/" + files[lang || ${JSON.stringify(DEFAULT_LANGUAGE)}];
  document.head.appendChild(link);
})();`;
        return [{ tag: "script", injectTo: "head", children: script }];
      },
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), preloadLocale()],
  server: {
    host: true,
    proxy: {
      "/healthz": { target: "http://backend:8080", changeOrigin: true },
      "/api": { target: "http://backend:8080", changeOrigin: true },
    },
  },
});
