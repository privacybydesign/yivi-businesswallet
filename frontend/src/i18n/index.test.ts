import { describe, expect, it } from "vitest";
import i18n, { i18nReady, showLanguage } from "./index";

describe("language loading", () => {
  // useCreateRequestMutation sends resolvedLanguage as the request's language;
  // i18next leaves it undefined when the strings arrive after the switch.
  it("resolves the language once its strings are in", async () => {
    await i18nReady;
    expect(i18n.resolvedLanguage).toBe(i18n.language);

    await showLanguage("nl");
    expect(i18n.resolvedLanguage).toBe("nl");
    expect(i18n.t("common.edit")).not.toBe("common.edit");
    await showLanguage("en");
  });
});
