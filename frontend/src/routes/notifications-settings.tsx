import { useState } from "react";
import { useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useNotificationSettingsQuery,
  useUpdateNotificationSettingsMutation,
} from "../api/notifications.queries";
import type {
  ChannelId,
  NotificationGroup,
  NotificationSettings,
} from "../api/notifications";
import {
  NOTIFICATION_CHANNELS,
  SUPPORTED_CHANNELS,
} from "../api/notifications";
import { useSlackSettingsQuery } from "../api/slack.queries";
import { auditActionLabel } from "../lib/audit-event";
import { Button, Card, Icon } from "../ui";
import * as React from "react";

type Subscriptions = Record<string, ChannelId[]>;

const CHANNEL_LABEL_KEY = {
  email: "notifications.channels.email",
  slack: "notifications.channels.slack",
  msteams: "notifications.channels.msteams",
} as const satisfies Record<ChannelId, string>;

const GROUP_LABEL_KEY = {
  membership: "notifications.groups.membership",
  wallet: "notifications.groups.wallet",
  qerds: "notifications.groups.qerds",
  postguard: "notifications.groups.postguard",
  attestation: "notifications.groups.attestation",
} as const satisfies Record<NotificationGroup, string>;

// A stable, order-independent fingerprint of a subscription document, used to
// tell whether the admin's edits differ from what is stored. Events and channels
// are both sorted so the comparison ignores ordering the backend may re-canonicalize.
function fingerprint(subs: Subscriptions): string {
  const events = Object.keys(subs)
    .filter((event) => subs[event].length > 0)
    .sort();
  return JSON.stringify(
    events.map((event) => [event, [...subs[event]].sort()]),
  );
}

export function NotificationsSettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useNotificationSettingsQuery(slug);
  const slack = useSlackSettingsQuery(slug);

  if (settings.isError) {
    return (
      <Card className="max-w-3xl p-6">
        <p className="text-error text-[14px]">
          {t("notifications.loadError", { message: settings.error.message })}
        </p>
      </Card>
    );
  }

  if (settings.isPending) {
    return (
      <Card className="max-w-3xl p-6">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }

  // Slack can only deliver once an org has stored and enabled a webhook. The
  // column is still editable before that (a preference set now is honoured once
  // configured), but a notice makes the gap explicit — matching "via Slack (when
  // configured)".
  const slackReady = Boolean(slack.data?.enabled);

  return (
    <NotificationsForm
      // Remount after a save so local edits re-seed from the stored document.
      key={settings.data.updatedAt ?? "unset"}
      slug={slug}
      settings={settings.data}
      slackReady={slackReady}
    />
  );
}

function NotificationsForm({
  slug,
  settings,
  slackReady,
}: {
  slug: string;
  settings: NotificationSettings;
  slackReady: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const save = useUpdateNotificationSettingsMutation(slug);

  const [draft, setDraft] = useState<Subscriptions>(settings.subscriptions);
  const dirty = fingerprint(draft) !== fingerprint(settings.subscriptions);

  // Only the channels with a working backend get a column; the API may also list
  // reserved ids (msteams). Order follows the server's channel list.
  const columns = settings.channels.filter((channel) =>
    (SUPPORTED_CHANNELS as readonly ChannelId[]).includes(channel),
  );

  // Group the catalog while preserving the server's display order.
  const groups: { group: NotificationGroup; events: string[] }[] = [];
  for (const entry of settings.events) {
    const bucket = groups.find((item) => item.group === entry.group);
    if (bucket) bucket.events.push(entry.event);
    else groups.push({ group: entry.group, events: [entry.event] });
  }

  function isChecked(event: string, channel: ChannelId): boolean {
    return (draft[event] ?? []).includes(channel);
  }

  function toggle(event: string, channel: ChannelId): void {
    setDraft((prev) => {
      const current = new Set(prev[event] ?? []);
      if (current.has(channel)) current.delete(channel);
      else current.add(channel);
      const next = { ...prev };
      if (current.size === 0) {
        delete next[event];
      } else {
        // Store in the canonical channel order so the fingerprint is stable.
        next[event] = NOTIFICATION_CHANNELS.filter((c) => current.has(c));
      }
      return next;
    });
  }

  function submit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (!dirty || save.isPending) return;
    save.mutate({ subscriptions: draft });
  }

  return (
    <form onSubmit={submit} className="max-w-3xl">
      <Card className="p-7">
        <h2 className="text-[16px] font-semibold">
          {t("notifications.heading")}
        </h2>
        <p className="text-ink-soft mt-1 text-[13px]">
          {t("notifications.description")}
        </p>

        {!slackReady && columns.includes("slack") && (
          <div className="bg-warning-bg text-warning-fg rounded-yivi mt-4 flex items-start gap-2.5 p-3 text-[12.5px]">
            <Icon name="warning" size={16} className="mt-0.5 shrink-0" />
            <div>
              <span>{t("notifications.slackNotConfigured")}</span>{" "}
              <button
                type="button"
                className="font-semibold underline"
                onClick={() =>
                  setSearchParams(
                    (prev) => {
                      const params = new URLSearchParams(prev);
                      params.set("tab", "slack");
                      return params;
                    },
                    { replace: true },
                  )
                }
              >
                {t("notifications.configureSlack")}
              </button>
            </div>
          </div>
        )}

        <div className="mt-5 overflow-x-auto">
          <table className="w-full border-collapse text-[13.5px]">
            <thead>
              <tr className="border-line border-b">
                <th className="text-ink-soft py-2 pr-3 text-left text-[12px] font-semibold">
                  {t("notifications.eventColumn")}
                </th>
                {columns.map((channel) => (
                  <th
                    key={channel}
                    className="text-ink-soft w-24 px-2 py-2 text-center text-[12px] font-semibold"
                  >
                    {t(CHANNEL_LABEL_KEY[channel])}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {groups.map(({ group, events }) => (
                <React.Fragment key={group}>
                  <tr>
                    <th
                      colSpan={1 + columns.length}
                      className="text-ink pt-5 pb-1.5 text-left text-[12.5px] font-bold"
                    >
                      {t(GROUP_LABEL_KEY[group])}
                    </th>
                  </tr>
                  {events.map((event) => (
                    <tr
                      key={event}
                      className="border-line/60 hover:bg-surface-2 border-b"
                    >
                      <td className="text-ink py-2 pr-3">
                        {auditActionLabel(event, t)}
                      </td>
                      {columns.map((channel) => {
                        const label = `${auditActionLabel(event, t)} — ${t(
                          CHANNEL_LABEL_KEY[channel],
                        )}`;
                        return (
                          <td key={channel} className="px-2 py-2 text-center">
                            <input
                              type="checkbox"
                              aria-label={label}
                              checked={isChecked(event, channel)}
                              onChange={() => toggle(event, channel)}
                            />
                          </td>
                        );
                      })}
                    </tr>
                  ))}
                </React.Fragment>
              ))}
            </tbody>
          </table>
        </div>

        {save.isError && (
          <p role="alert" className="text-error mt-4 text-[13px]">
            {t("notifications.saveError", { message: save.error.message })}
          </p>
        )}

        <div className="mt-6">
          <Button type="submit" loading={save.isPending} disabled={!dirty}>
            {t("common.save")}
          </Button>
        </div>
      </Card>
    </form>
  );
}
