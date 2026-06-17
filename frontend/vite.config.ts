import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    proxy: {
      "/healthz": { target: "http://backend:8080", changeOrigin: true },
      "/api": { target: "http://backend:8080", changeOrigin: true },
    },
  },
});
