import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: true,
    proxy: {
      "/healthz": { target: "http://backend:8080", changeOrigin: true },
      "/api": { target: "http://backend:8080", changeOrigin: true },
    },
  },
});
