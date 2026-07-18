import { defineConfig } from "vitest/config";

// Unit tests are pure and DOM-free (they cover extracted logic, not rendered
// components), so they run in the node environment with no jsdom dependency.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
