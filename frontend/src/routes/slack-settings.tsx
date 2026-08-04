import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useSendSlackTestMutation,
  useSlackSettingsQuery,
  useUpdateSlackSettingsMutation,
} from "../api/slack.queries";
import type { SlackSettings } from "../api/slack";
import { ApiError } from "../api/http";
import { Button, Card, Input } from "../ui";
import * as React from "react";

const CONFLICT_STATUS = 409;
const BAD_GATEWAY_STATUS = 502;
const LABEL = "text-ink-soft text-[12px] font-semibold";

function errorCode(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "code" in error.body
  ) {
    const { code } = error.body;
    return typeof code === "string" ? code : null;
  }
  return null;
}

// The backend's own message for a rejected webhook (Slack's refusal, the status it
// answered with), or the transport error when there is none.
function apiMessage(error: Error): string {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "message" in error.body &&
    typeof error.body.message === "string"
  ) {
    return error.body.message;
  }
  return error.message;
}

function saveError(error: Error, t: TFunction): string {
  if (
    error instanceof ApiError &&
    error.status === CONFLICT_STATUS &&
    errorCode(error) === "no_encryption_key"
  ) {
    return t("slackSettings.noEncryptionKey");
  }
  return t("slackSettings.saveError", { message: apiMessage(error) });
}

function testError(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    return t("slackSettings.testNotConfigured");
  }
  if (error instanceof ApiError && error.status === BAD_GATEWAY_STATUS) {
    return t("slackSettings.testRefused", { message: apiMessage(error) });
  }
  return t("slackSettings.testError", { message: error.message });
}

// The webhook form seeds its state from the stored settings, so it is remounted
// (via a `key` on updatedAt) whenever a save refreshes the data. The URL field
// always starts empty: a stored webhook is never sent back to the browser.
function WebhookForm({
  slug,
  initial,
}: {
  slug: string;
  initial: SlackSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useUpdateSlackSettingsMutation(slug);

  const [webhookUrl, setWebhookUrl] = useState("");
  const [enabled, setEnabled] = useState(initial.enabled);

  // Posting to nothing cannot work, so the switch waits for a webhook.
  const canEnable = initial.hasWebhook || webhookUrl.trim() !== "";
  // Removing is the same mutation as saving, so the spinner follows whichever of
  // the two is in flight instead of appearing on both buttons.
  const removing = save.isPending && save.variables.webhookUrl === "";

  function handleSave(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (save.isPending) {
      return;
    }
    const typed = webhookUrl.trim();
    save.mutate({
      // Blank keeps the stored webhook; a typed value replaces it.
      webhookUrl: typed === "" ? null : typed,
      enabled: canEnable && enabled,
    });
  }

  function handleRemove(): void {
    if (save.isPending) {
      return;
    }
    save.mutate({ webhookUrl: "", enabled: false });
  }

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("slackSettings.heading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("slackSettings.description")}
      </p>

      <form onSubmit={handleSave} className="mt-4 flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label htmlFor="slack-webhook-url" className={LABEL}>
            {t("slackSettings.webhookUrl")}
          </label>
          <Input
            id="slack-webhook-url"
            type="password"
            value={webhookUrl}
            onChange={(event) => setWebhookUrl(event.target.value)}
            placeholder={
              initial.hasWebhook
                ? t("slackSettings.webhookUnchanged")
                : t("slackSettings.webhookPlaceholder")
            }
            autoComplete="off"
            spellCheck={false}
          />
          {initial.hasWebhook && (
            <p className="text-ink-soft text-[12px]">
              {t("slackSettings.webhookStored")}
            </p>
          )}
        </div>

        <label
          className={[
            "text-ink flex items-center gap-2 text-[13.5px]",
            canEnable ? "cursor-pointer" : "cursor-not-allowed opacity-50",
          ].join(" ")}
        >
          <input
            type="checkbox"
            checked={canEnable && enabled}
            disabled={!canEnable}
            onChange={(event) => setEnabled(event.target.checked)}
          />
          {t("slackSettings.enabled")}
        </label>
        {!canEnable && (
          <p className="text-ink-soft text-[12px]">
            {t("slackSettings.enabledNeedsWebhook")}
          </p>
        )}

        {save.isError && (
          <p role="alert" className="text-error text-[13px]">
            {saveError(save.error, t)}
          </p>
        )}

        <div className="flex gap-2">
          <Button type="submit" loading={save.isPending && !removing}>
            {t("common.save")}
          </Button>
          {initial.hasWebhook && (
            <Button
              variant="danger"
              icon="delete"
              loading={removing}
              disabled={save.isPending}
              onClick={handleRemove}
            >
              {t("slackSettings.remove")}
            </Button>
          )}
        </div>
      </form>
    </Card>
  );
}

function TestCard({
  slug,
  ready,
}: {
  slug: string;
  ready: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const test = useSendSlackTestMutation(slug);

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("slackSettings.testHeading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("slackSettings.testDescription")}
      </p>
      <div className="mt-4">
        <Button
          loading={test.isPending}
          disabled={!ready}
          onClick={() => test.mutate()}
        >
          {t("slackSettings.sendTest")}
        </Button>
      </div>
      {!ready && (
        <p className="text-ink-soft mt-2 text-[12px]">
          {t("slackSettings.testNotConfigured")}
        </p>
      )}
      {test.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {testError(test.error, t)}
        </p>
      )}
    </Card>
  );
}

// SlackSettingsPanel is the org's Slack notification configuration, rendered as a
// tab on the Settings page. The caller (Settings) has already resolved the org and
// gated on admin, so the panel only owns the settings query and its forms.
export function SlackSettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useSlackSettingsQuery(slug, true);

  if (settings.isError) {
    return (
      <Card className="p-6">
        <p className="text-error text-[14px]">
          {t("slackSettings.loadError", { message: settings.error.message })}
        </p>
      </Card>
    );
  }
  if (settings.isPending) {
    return (
      <Card className="p-6">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <WebhookForm
        key={settings.data.updatedAt ?? "unset"}
        slug={slug}
        initial={settings.data}
      />
      <TestCard
        slug={slug}
        ready={settings.data.hasWebhook && settings.data.enabled}
      />
    </div>
  );
}
