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
import { SUPPORTED_CHANNELS } from "../api/notifications";
import { useSlackSettingsQuery } from "../api/slack.queries";
import {
  subscriptionsDiffer,
  toggleChannel,
} from "../lib/notification-subscriptions";
import type { Subscriptions } from "../lib/notification-subscriptions";
import { auditActionLabel } from "../lib/audit-event";
import { Button, Card, Icon, Table } from "../ui";
import * as React from "react";

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
  // configured)". Only trust a settled query: while it is loading or has failed
  // (main.tsx sets retry:false, so a failure never recovers) we don't yet know,
  // so we treat Slack as ready rather than show a false "not configured" notice.
  const slackReady = !slack.isSuccess || slack.data.enabled;

  return (
    <NotificationsForm
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

  // A nullable draft (null = "no local edits, show the stored document"), the
  // same pattern as postguard-notifications.tsx. Cleared on a successful save so
  // edits re-seed from the refetched document without a remount that could stomp
  // an in-flight edit. The checkboxes are also disabled while a save is pending,
  // which is when that stomp could otherwise happen.
  const [draft, setDraft] = useState<Subscriptions | null>(null);
  const active = draft ?? settings.subscriptions;
  const dirty = subscriptionsDiffer(active, settings.subscriptions);

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
    return (active[event] ?? []).includes(channel);
  }

  function toggle(event: string, channel: ChannelId): void {
    setDraft((prev) =>
      toggleChannel(prev ?? settings.subscriptions, event, channel),
    );
  }

  function submit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (!dirty || save.isPending) return;
    save.mutate({ subscriptions: active }, { onSuccess: () => setDraft(null) });
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

        <div className="mt-5">
          <Table>
            <Table.Head>
              <Table.HeaderCell scope="col">
                {t("notifications.eventColumn")}
              </Table.HeaderCell>
              {columns.map((channel) => (
                <Table.HeaderCell
                  key={channel}
                  scope="col"
                  className="w-24 text-center"
                >
                  {t(CHANNEL_LABEL_KEY[channel])}
                </Table.HeaderCell>
              ))}
            </Table.Head>
            <Table.Body>
              {groups.map(({ group, events }) => (
                <React.Fragment key={group}>
                  <Table.Row>
                    <th
                      scope="colgroup"
                      colSpan={1 + columns.length}
                      className="text-ink pt-5 pb-1.5 text-left text-[12.5px] font-bold"
                    >
                      {t(GROUP_LABEL_KEY[group])}
                    </th>
                  </Table.Row>
                  {events.map((event) => (
                    <Table.Row key={event} className="hover:bg-surface-2">
                      <Table.Cell scope="row" className="text-ink">
                        {auditActionLabel(event, t)}
                      </Table.Cell>
                      {columns.map((channel) => {
                        const label = `${auditActionLabel(event, t)} — ${t(
                          CHANNEL_LABEL_KEY[channel],
                        )}`;
                        return (
                          <Table.Cell key={channel} className="text-center">
                            <input
                              type="checkbox"
                              aria-label={label}
                              checked={isChecked(event, channel)}
                              disabled={save.isPending}
                              onChange={() => toggle(event, channel)}
                            />
                          </Table.Cell>
                        );
                      })}
                    </Table.Row>
                  ))}
                </React.Fragment>
              ))}
            </Table.Body>
          </Table>
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
