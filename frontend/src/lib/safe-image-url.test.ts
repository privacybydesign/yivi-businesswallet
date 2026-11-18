import { describe, expect, it } from "vitest";
import { safeImageUrl } from "./safe-image-url";

describe("safe image url", () => {
  it("keeps a picked file, an inline image, the API and a path", () => {
    for (const uri of [
      "blob:http://localhost:5173/1f2e",
      "data:image/png;base64,AAAA",
      "https://api.example.org/logo",
      "/api/v1/orgs/acme/theme/logo",
    ]) {
      expect(safeImageUrl(uri), uri).toBe(uri);
    }
  });

  it("drops anything else", () => {
    for (const uri of [
      "javascript:alert(1)",
      "data:text/html,<script>",
      "//evil.example/logo",
      "logo.png",
    ]) {
      expect(safeImageUrl(uri), uri).toBe("");
    }
  });
});
