import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useEmailSettingsQuery,
  useSendTestEmailMutation,
  useUpdateEmailSettingsMutation,
} from "../api/email.queries";
import type { EmailSettings as EmailSettingsData } from "../api/email";
import {
  SMTP_AUTH_MECHANISMS,
  isSmtpAuthMechanism,
  smtpAuthMechanismOptions,
} from "../api/email";
import { ApiError } from "../api/http";
import { Button, Card, Input } from "../ui";
import * as React from "react";

const DECIMAL_RADIX = 10;
const CONFLICT_STATUS = 409;
const DEFAULT_SMTP_PORT = 587;
const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";
const XOAUTH2 = "xoauth2";
// Plausible address check only; the backend is the authority.
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// A mechanism this frontend has no copy for still renders, as its raw id, the
// way the directory-sync screen renders an unknown source.
function mechanismLabel(mechanism: string, t: TFunction): string {
  if (!isSmtpAuthMechanism(mechanism)) {
    return mechanism;
  }
  return t(`emailSettings.authMechanisms.${mechanism}`);
}

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
  const [authMechanism, setAuthMechanism] = useState(
    initial.authMechanism || SMTP_AUTH_MECHANISMS[0],
  );
  const [tenantId, setTenantId] = useState(initial.tenantId);
  const [clientId, setClientId] = useState(initial.clientId);
  const [clientSecret, setClientSecret] = useState("");
  const [fromName, setFromName] = useState(initial.fromName);
  const [fromAddress, setFromAddress] = useState(initial.fromAddress);
  const [enabled, setEnabled] = useState(initial.enabled);
  const [localError, setLocalError] = useState<string | null>(null);

  const usesOAuth = authMechanism === XOAUTH2;

  function handleSave(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (save.isPending) {
      return;
    }
    const trimmedTenant = tenantId.trim();
    const trimmedClient = clientId.trim();
    // The backend refuses the same combination. Catching it here keeps the
    // reason beside the fields, and a half-filled configuration can still be
    // saved switched off.
    if (usesOAuth && enabled) {
      if (!trimmedTenant || !trimmedClient) {
        setLocalError(t("emailSettings.credentialsRequired"));
        return;
      }
      if (!initial.hasClientSecret && clientSecret === "") {
        setLocalError(t("emailSettings.clientSecretRequired"));
        return;
      }
    }
    setLocalError(null);
    save.mutate({
      host: host.trim(),
      port: Number.parseInt(port, DECIMAL_RADIX) || DEFAULT_SMTP_PORT,
      username: username.trim(),
      // Blank keeps the stored password; a typed value replaces it.
      password: password ? password : null,
      authMechanism,
      tenantId: trimmedTenant,
      clientId: trimmedClient,
      // Same rule as the password.
      clientSecret: clientSecret ? clientSecret : null,
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
          <label htmlFor="email-auth-mechanism" className={LABEL}>
            {t("emailSettings.authMechanism")}
          </label>
          <select
            id="email-auth-mechanism"
            className={CONTROL}
            value={authMechanism}
            onChange={(event) => {
              setAuthMechanism(event.target.value);
              // The message names fields the other mechanism does not have.
              setLocalError(null);
            }}
            aria-describedby={usesOAuth ? "email-mechanism-hint" : undefined}
          >
            {smtpAuthMechanismOptions(initial.authMechanism).map(
              (mechanism) => (
                <option key={mechanism} value={mechanism}>
                  {mechanismLabel(mechanism, t)}
                </option>
              ),
            )}
          </select>
          {usesOAuth && (
            <span id="email-mechanism-hint" className={HINT}>
              {t("emailSettings.xoauth2Hint")}
            </span>
          )}
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
            aria-describedby={usesOAuth ? "email-username-hint" : undefined}
          />
          {usesOAuth && (
            <span id="email-username-hint" className={HINT}>
              {t("emailSettings.usernameOAuthHint")}
            </span>
          )}
        </div>

        {usesOAuth ? (
          <>
            <div className="flex flex-col gap-1">
              <label htmlFor="email-tenant-id" className={LABEL}>
                {t("emailSettings.tenantId")}
              </label>
              <Input
                id="email-tenant-id"
                value={tenantId}
                onChange={(event) => setTenantId(event.target.value)}
                placeholder={t("emailSettings.tenantIdPlaceholder")}
                autoComplete="off"
              />
            </div>

            <div className="flex flex-col gap-1">
              <label htmlFor="email-client-id" className={LABEL}>
                {t("emailSettings.clientId")}
              </label>
              <Input
                id="email-client-id"
                value={clientId}
                onChange={(event) => setClientId(event.target.value)}
                placeholder={t("emailSettings.clientIdPlaceholder")}
                autoComplete="off"
              />
            </div>

            <div className="flex flex-col gap-1">
              <label htmlFor="email-client-secret" className={LABEL}>
                {t("emailSettings.clientSecret")}
              </label>
              <Input
                id="email-client-secret"
                type="password"
                value={clientSecret}
                onChange={(event) => setClientSecret(event.target.value)}
                autoComplete="new-password"
                placeholder={
                  initial.hasClientSecret
                    ? t("emailSettings.clientSecretUnchanged")
                    : t("emailSettings.clientSecretPlaceholder")
                }
                aria-describedby="email-client-secret-hint"
              />
              <span id="email-client-secret-hint" className={HINT}>
                {t("emailSettings.appRegistrationHint")}
              </span>
            </div>
          </>
        ) : (
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
        )}

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

        {localError && (
          <p role="alert" className="text-error text-[13px]">
            {localError}
          </p>
        )}
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
