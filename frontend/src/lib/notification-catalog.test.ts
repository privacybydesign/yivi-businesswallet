import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  NOTIFICATION_CHANNELS,
  NOTIFICATION_GROUPS,
} from "../api/notifications";
import { en } from "../i18n/locales/en";

// The backend is the source of truth for the notification channel ids and event
// groups (backend/internal/notifications/notifications.go). NOTIFICATION_CHANNELS
// and NOTIFICATION_GROUPS back closed zod enums (channelIdSchema /
// notificationGroupSchema) that every settings response is parsed through, so a
// channel id or group the backend adds and these lists omit fails the whole
// document and takes the Notifications tab down with it — the same failure mode
// `.ai/conventions/FRONTEND.md` documents for MAIL_TEMPLATE_KINDS. This test
// parses the Go constants and asserts each one is in the frontend list and has
// its i18n label, so a new backend channel/group can't silently break the screen
// or render a raw key.

const goPath = fileURLToPath(
  new URL(
    "../../../backend/internal/notifications/notifications.go",
    import.meta.url,
  ),
);
const source = readFileSync(goPath, "utf8");

// `ChannelEmail ChannelID = "email"`, one per known channel.
const backendChannels = [
  ...source.matchAll(/\bChannel\w+\s+ChannelID\s*=\s*"([^"]+)"/g),
].map((m) => m[1]);

// `GroupMembership Group = "membership"`, one per family.
const backendGroups = [
  ...source.matchAll(/\bGroup\w+\s+Group\s*=\s*"([^"]+)"/g),
].map((m) => m[1]);

// Read out of en.ts rather than through t(), because t()'s key type is the union
// of shipped keys and a raw string from the Go source is not assignable to it.
// nl.ts is typed against en.ts, so a missing Dutch twin already fails typecheck.
const channelLabels: Record<string, string> = en.notifications.channels;
const groupLabels: Record<string, string> = en.notifications.groups;

describe("notification catalog backend/frontend parity", () => {
  it("extracts the constants from notifications.go", () => {
    expect(backendChannels).toContain("email");
    expect(backendGroups).toContain("membership");
    expect(backendChannels).toHaveLength(NOTIFICATION_CHANNELS.length);
    expect(backendGroups).toHaveLength(NOTIFICATION_GROUPS.length);
  });

  it.each(backendChannels)("lists and names the channel %s", (channel) => {
    expect(NOTIFICATION_CHANNELS as readonly string[]).toContain(channel);
    expect(channelLabels[channel]).toBeTruthy();
  });

  it.each(backendGroups)("lists and names the group %s", (group) => {
    expect(NOTIFICATION_GROUPS as readonly string[]).toContain(group);
    expect(groupLabels[group]).toBeTruthy();
  });
});
