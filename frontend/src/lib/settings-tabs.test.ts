import { describe, expect, it } from "vitest";
import { en } from "../i18n/locales/en";
import { nl } from "../i18n/locales/nl";
import {
  COMMUNICATION_SECTIONS,
  COMMUNICATION_TAB,
  DEFAULT_COMMUNICATION_SECTION,
  DEFAULT_SETTINGS_TAB,
  SETTINGS_TABS,
  resolveSettingsLocation,
  settingsTabParam,
} from "./settings-tabs";
import type { CommunicationSection, SettingsTab } from "./settings-tabs";

// Grouping the five communication panels under one top-level tab moved their
// ?tab= keys from the top-level row into a sub-nav. Those keys are the URLs the
// screen has always been deep-linked by, so this is the list the flat 12-tab row
// accepted, with the panel each one must still open.
const LEGACY_TAB_KEYS: ReadonlyArray<
  readonly [string, SettingsTab, CommunicationSection]
> = [
  ["org", "org", DEFAULT_COMMUNICATION_SECTION],
  ["branding", "branding", DEFAULT_COMMUNICATION_SECTION],
  ["email", COMMUNICATION_TAB, "email"],
  ["mailTemplates", COMMUNICATION_TAB, "mailTemplates"],
  ["slack", COMMUNICATION_TAB, "slack"],
  ["msteams", COMMUNICATION_TAB, "msteams"],
  ["notifications", COMMUNICATION_TAB, "notifications"],
  ["issuer", "issuer", DEFAULT_COMMUNICATION_SECTION],
  ["provisioning", "provisioning", DEFAULT_COMMUNICATION_SECTION],
  ["signing", "signing", DEFAULT_COMMUNICATION_SECTION],
  ["postguard", "postguard", DEFAULT_COMMUNICATION_SECTION],
  ["wallets", "wallets", DEFAULT_COMMUNICATION_SECTION],
];

// Walk a dotted i18n key ("settings.tabOrg") through a locale object.
function lookup(locale: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => {
    if (typeof node !== "object" || node === null) return undefined;
    return (node as Record<string, unknown>)[part];
  }, locale);
}

describe("settings tab layout", () => {
  it("has eight top-level tabs", () => {
    expect(SETTINGS_TABS).toHaveLength(9);
  });

  it("keeps the communication panels out of the top-level row", () => {
    const topLevel = SETTINGS_TABS.map((item) => item.key as string);
    for (const item of COMMUNICATION_SECTIONS) {
      expect(topLevel).not.toContain(item.key);
    }
  });

  it("labels every tab and section in both locales", () => {
    const labelKeys = [
      ...SETTINGS_TABS.map((item) => item.labelKey as string),
      ...COMMUNICATION_SECTIONS.map((item) => item.labelKey as string),
    ];
    for (const key of labelKeys) {
      expect(lookup(en, key), `en is missing ${key}`).toEqual(
        expect.any(String),
      );
      expect(lookup(nl, key), `nl is missing ${key}`).toEqual(
        expect.any(String),
      );
    }
  });
});

describe("resolveSettingsLocation", () => {
  it("still opens the panel every pre-grouping ?tab= key named", () => {
    for (const [value, tab, section] of LEGACY_TAB_KEYS) {
      expect(resolveSettingsLocation(value), value).toEqual({ tab, section });
    }
  });

  it("opens the first section for the communication tab's own key", () => {
    expect(resolveSettingsLocation(COMMUNICATION_TAB)).toEqual({
      tab: COMMUNICATION_TAB,
      section: DEFAULT_COMMUNICATION_SECTION,
    });
  });

  it("falls back to the first tab for a missing or unknown value", () => {
    for (const value of [null, "", "nope"]) {
      expect(resolveSettingsLocation(value), String(value)).toEqual({
        tab: DEFAULT_SETTINGS_TAB,
        section: DEFAULT_COMMUNICATION_SECTION,
      });
    }
  });
});

describe("settingsTabParam", () => {
  it("round-trips every pre-grouping ?tab= key it addresses", () => {
    for (const [value] of LEGACY_TAB_KEYS) {
      const param = settingsTabParam(resolveSettingsLocation(value));
      // The default tab drops the parameter instead of naming itself.
      expect(param, value).toBe(value === DEFAULT_SETTINGS_TAB ? null : value);
    }
  });

  it("addresses a communication section by its own key", () => {
    for (const item of COMMUNICATION_SECTIONS) {
      expect(
        settingsTabParam({ tab: COMMUNICATION_TAB, section: item.key }),
        item.key,
      ).toBe(item.key);
    }
  });

  it("ignores the section on a tab other than communication", () => {
    expect(settingsTabParam({ tab: "wallets", section: "slack" })).toBe(
      "wallets",
    );
  });
});
