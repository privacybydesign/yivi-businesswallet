// The settings screen's tab layout. The five outbound-messaging panels
// (notifications and the channels they fan out to) sit under one top-level
// "Communication" tab with a sub-nav of their own, so the top-level row stays
// short enough to scan.
//
// Both levels are addressed through the single ?tab= parameter: a communication
// panel keeps the key it has always had (?tab=slack opens Slack), so links
// written before the grouping still land on the right panel. "?tab=communication"
// is accepted too and opens the tab's first section.

export const COMMUNICATION_SECTIONS = [
  { key: "notifications", labelKey: "settings.tabNotifications" },
  { key: "email", labelKey: "settings.tabEmail" },
  { key: "mailTemplates", labelKey: "settings.tabMailTemplates" },
  { key: "slack", labelKey: "settings.tabSlack" },
  { key: "msteams", labelKey: "settings.tabTeams" },
] as const;

export const SETTINGS_TABS = [
  { key: "org", labelKey: "settings.tabOrg" },
  { key: "mandates", labelKey: "settings.tabMandates" },
  { key: "branding", labelKey: "settings.tabBranding" },
  { key: "communication", labelKey: "settings.tabCommunication" },
  { key: "issuer", labelKey: "settings.tabIssuer" },
  { key: "provisioning", labelKey: "settings.tabProvisioning" },
  { key: "signing", labelKey: "settings.tabSigning" },
  { key: "postguard", labelKey: "settings.tabPostguard" },
  { key: "wallets", labelKey: "settings.tabWallets" },
] as const;

export type CommunicationSection =
  (typeof COMMUNICATION_SECTIONS)[number]["key"];

export type SettingsTab = (typeof SETTINGS_TABS)[number]["key"];

export const COMMUNICATION_TAB: SettingsTab = "communication";

export const DEFAULT_SETTINGS_TAB: SettingsTab = SETTINGS_TABS[0].key;

export const DEFAULT_COMMUNICATION_SECTION: CommunicationSection =
  COMMUNICATION_SECTIONS[0].key;

// Which panel the screen shows: the top-level tab plus, when that tab is
// Communication, the section within it. The section is carried even on other
// tabs so switching to Communication opens a defined panel.
export type SettingsLocation = {
  tab: SettingsTab;
  section: CommunicationSection;
};

export function resolveSettingsLocation(
  value: string | null,
): SettingsLocation {
  const section = COMMUNICATION_SECTIONS.find((item) => item.key === value);
  if (section) return { tab: COMMUNICATION_TAB, section: section.key };

  const tab =
    SETTINGS_TABS.find((item) => item.key === value)?.key ??
    DEFAULT_SETTINGS_TAB;
  return { tab, section: DEFAULT_COMMUNICATION_SECTION };
}

// The ?tab= value that addresses a location, or null when the parameter can be
// left off (the default tab). Communication is written as its section's key
// rather than "communication", so the URL of a panel stays the link that panel
// has always had.
export function settingsTabParam(location: SettingsLocation): string | null {
  if (location.tab === COMMUNICATION_TAB) return location.section;
  return location.tab === DEFAULT_SETTINGS_TAB ? null : location.tab;
}
