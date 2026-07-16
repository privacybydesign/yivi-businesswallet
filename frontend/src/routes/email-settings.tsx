import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useEmailSettingsQuery,
  useSendTestEmailMutation,
  useUpdateEmailSettingsMutation,
} from "../api/email.queries";
import type { EmailSettings as EmailSettingsData } from "../api/email";
import { ApiError } from "../api/http";
import { Button, Card, Input } from "../ui";
import * as React from "react";

const DECIMAL_RADIX = 10;
const CONFLICT_STATUS = 409;
const DEFAULT_SMTP_PORT = 587;
const LABEL = "text-ink-soft text-[12px] font-semibold";
// Plausible address check only; the backend is the authority.
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

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

function testError(error: Error, t: TFunction): string {
  if (
    error instanceof ApiError &&
    error.status === CONFLICT_STATUS &&
    errorCode(error) === "not_configured"
  ) {
    return t("emailSettings.testNotConfigured");
  }
  return t("emailSettings.testError", { message: error.message });
}

// The SMTP form seeds its state directly from the stored settings, so it is
// remounted (via a `key` on updatedAt) whenever a save refreshes the data.
function SmtpForm({
  slug,
  initial,
}: {
  slug: string;
  initial: EmailSettingsData;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useUpdateEmailSettingsMutation(slug);

  const [host, setHost] = useState(initial.host);
  const [port, setPort] = useState(String(initial.port || DEFAULT_SMTP_PORT));
  const [username, setUsername] = useState(initial.username);
  const [password, setPassword] = useState("");
  const [fromName, setFromName] = useState(initial.fromName);
  const [fromAddress, setFromAddress] = useState(initial.fromAddress);
  const [enabled, setEnabled] = useState(initial.enabled);

  function handleSave(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (save.isPending) {
      return;
    }
    save.mutate({
      host: host.trim(),
      port: Number.parseInt(port, DECIMAL_RADIX) || DEFAULT_SMTP_PORT,
      username: username.trim(),
      // Blank keeps the stored password; a typed value replaces it.
      password: password ? password : null,
      fromName: fromName.trim(),
      fromAddress: fromAddress.trim(),
      enabled,
    });
  }

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("emailSettings.heading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("emailSettings.description")}
      </p>

      <form onSubmit={handleSave} className="mt-4 flex flex-col gap-3">
        <div className="grid grid-cols-[1fr_120px] gap-3">
          <div className="flex flex-col gap-1">
            <label htmlFor="email-host" className={LABEL}>
              {t("emailSettings.host")}
            </label>
            <Input
              id="email-host"
              value={host}
              onChange={(event) => setHost(event.target.value)}
              placeholder={t("emailSettings.hostPlaceholder")}
              autoComplete="off"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="email-port" className={LABEL}>
              {t("emailSettings.port")}
            </label>
            <Input
              id="email-port"
              type="number"
              min={1}
              value={port}
              onChange={(event) => setPort(event.target.value)}
            />
          </div>
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="email-username" className={LABEL}>
            {t("emailSettings.username")}
          </label>
          <Input
            id="email-username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            autoComplete="off"
          />
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="email-password" className={LABEL}>
            {t("emailSettings.password")}
          </label>
          <Input
            id="email-password"
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            autoComplete="new-password"
            placeholder={
              initial.hasPassword
                ? t("emailSettings.passwordUnchanged")
                : t("emailSettings.passwordPlaceholder")
            }
          />
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label htmlFor="email-from-name" className={LABEL}>
              {t("emailSettings.fromName")}
            </label>
            <Input
              id="email-from-name"
              value={fromName}
              onChange={(event) => setFromName(event.target.value)}
              placeholder={t("emailSettings.fromNamePlaceholder")}
              autoComplete="off"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="email-from-address" className={LABEL}>
              {t("emailSettings.fromAddress")}
            </label>
            <Input
              id="email-from-address"
              type="email"
              value={fromAddress}
              onChange={(event) => setFromAddress(event.target.value)}
              placeholder={t("emailSettings.fromAddressPlaceholder")}
              autoComplete="off"
            />
          </div>
        </div>

        <label className="text-ink flex cursor-pointer items-center gap-2 text-[13.5px]">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(event) => setEnabled(event.target.checked)}
          />
          {t("emailSettings.enabled")}
        </label>

        {save.isError && (
          <p role="alert" className="text-error text-[13px]">
            {t("emailSettings.saveError", { message: save.error.message })}
          </p>
        )}

        <div>
          <Button type="submit" loading={save.isPending}>
            {t("common.save")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

function TestForm({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const test = useSendTestEmailMutation(slug);
  const [testTo, setTestTo] = useState("");

  function handleTest(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    const to = testTo.trim();
    if (!EMAIL_PATTERN.test(to) || test.isPending) {
      return;
    }
    test.mutate({ to });
  }

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("emailSettings.testHeading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("emailSettings.testDescription")}
      </p>
      <form onSubmit={handleTest} className="mt-4 flex gap-2">
        <div className="flex-1">
          <Input
            type="email"
            value={testTo}
            onChange={(event) => setTestTo(event.target.value)}
            placeholder={t("emailSettings.testPlaceholder")}
            aria-label={t("emailSettings.testPlaceholder")}
            autoComplete="off"
          />
        </div>
        <Button
          type="submit"
          icon="email"
          loading={test.isPending}
          disabled={!EMAIL_PATTERN.test(testTo.trim())}
        >
          {t("emailSettings.sendTest")}
        </Button>
      </form>
      {test.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {testError(test.error, t)}
        </p>
      )}
    </Card>
  );
}

// EmailSettingsPanel is the org e-mail (SMTP) configuration, rendered as a tab
// on the Settings page. The caller (Settings) has already resolved the org and
// gated on admin, so the panel only owns the settings query and its forms.
export function EmailSettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useEmailSettingsQuery(slug, true);

  if (settings.isError) {
    return (
      <Card className="p-6">
        <p className="text-error text-[14px]">
          {t("emailSettings.loadError", { message: settings.error.message })}
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
      <SmtpForm
        key={settings.data.updatedAt ?? "unset"}
        slug={slug}
        initial={settings.data}
      />
      <TestForm slug={slug} />
    </div>
  );
}
