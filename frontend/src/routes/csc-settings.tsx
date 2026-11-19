import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useCscSettingsQuery,
  useTestCscConnectionMutation,
  useUpdateCscSettingsMutation,
} from "../api/csc.queries";
import { CSC_PROVIDER_KIND_BUILT_IN } from "../api/csc";
import type { CscSettings } from "../api/csc";
import { ApiError } from "../api/http";
import {
  canEnableCsc,
  cscSettingsInput,
  initialCscBaseUrl,
  isBuiltInCscKind,
} from "../lib/csc-settings";
import { Button, Card, Input, Select } from "../ui";
import * as React from "react";
import { draftChanged } from "../lib/form-draft";
import { UnsavedChanges } from "./unsaved-changes";

const CONFLICT_STATUS = 409;
const BAD_GATEWAY_STATUS = 502;
const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";

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

function saveError(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    const code = errorCode(error);
    if (code === "no_encryption_key") {
      return t("cscSettings.noEncryptionKey");
    }
    if (code === "built_in_unavailable") {
      return t("cscSettings.builtInUnavailable");
    }
  }
  return t("cscSettings.saveError", { message: apiMessage(error) });
}

function testError(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    return t("cscSettings.testNotConfigured");
  }
  if (error instanceof ApiError && error.status === BAD_GATEWAY_STATUS) {
    return t("cscSettings.testFailed", { message: apiMessage(error) });
  }
  return t("cscSettings.testError", { message: error.message });
}

// The provider label falls back to the raw id, so a backend kind this frontend
// has no copy for still renders instead of showing an empty option. Literal keys
// (not a computed path) keep the typed `t()` happy.
function providerKindLabel(id: string, t: TFunction): string {
  switch (id) {
    case CSC_PROVIDER_KIND_BUILT_IN:
      return t("cscSettings.kinds.yivi");
    case "sample":
      return t("cscSettings.kinds.sample");
    case "custom":
      return t("cscSettings.kinds.custom");
    default:
      return id;
  }
}

// The config form seeds its state from the stored settings and is remounted (via
// a `key` on updatedAt) whenever a save refreshes the data. The client-secret
// field always starts empty: a stored secret is never sent back to the browser.
// The built-in signing service has nothing to enter, so its endpoint and OAuth
// fields give way to a line on what it is.
function ConfigForm({
  slug,
  initial,
}: Readonly<{
  slug: string;
  initial: CscSettings;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const save = useUpdateCscSettingsMutation(slug);

  const [providerKind, setProviderKind] = useState(initial.providerKind);
  const [baseUrl, setBaseUrl] = useState(
    initialCscBaseUrl(initial.providerKind, initial.baseUrl),
  );
  const [clientId, setClientId] = useState(initial.clientId);
  const [clientSecret, setClientSecret] = useState("");
  const [enabled, setEnabled] = useState(initial.enabled);
  const builtIn = isBuiltInCscKind(providerKind);

  // Picking a kind pre-fills its default base URL, but only into an empty field
  // so a typed custom endpoint is never clobbered. The built-in service has no
  // field to fill.
  function handleKindChange(nextKind: string): void {
    setProviderKind(nextKind);
    if (isBuiltInCscKind(nextKind)) {
      return;
    }
    const preset = initial.providerKinds.find((k) => k.id === nextKind);
    if (preset && preset.defaultBaseUrl !== "" && baseUrl.trim() === "") {
      setBaseUrl(preset.defaultBaseUrl);
    }
  }

  // A remote provider with no endpoint cannot be reached, so enabling one waits
  // for a URL.
  const canEnable = canEnableCsc(providerKind, baseUrl);

  function handleSave(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (save.isPending) {
      return;
    }
    save.mutate(
      cscSettingsInput({
        enabled,
        providerKind,
        baseUrl,
        clientId,
        clientSecret,
      }),
    );
  }

  const dirty = draftChanged(
    { providerKind, baseUrl, clientId, clientSecret, enabled },
    {
      providerKind: initial.providerKind,
      baseUrl: initial.baseUrl,
      clientId: initial.clientId,
      clientSecret: "",
      enabled: initial.enabled,
    },
  );

  return (
    <Card className="max-w-2xl p-7">
      <UnsavedChanges dirty={dirty && !save.isPending} />
      <h2 className="text-[16px] font-semibold">{t("cscSettings.heading")}</h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("cscSettings.description")}
      </p>

      <form onSubmit={handleSave} className="mt-4 flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label htmlFor="csc-provider-kind" className={LABEL}>
            {t("cscSettings.providerKind")}
          </label>
          <Select
            id="csc-provider-kind"
            value={providerKind}
            onChange={(event) => handleKindChange(event.target.value)}
          >
            {initial.providerKinds.map((kind) => (
              <option key={kind.id} value={kind.id}>
                {providerKindLabel(kind.id, t)}
              </option>
            ))}
          </Select>
          <p className={HINT}>
            {builtIn
              ? t("cscSettings.builtInHint")
              : t("cscSettings.sampleHint")}
          </p>
        </div>

        {!builtIn && (
          <>
            <div className="flex flex-col gap-1">
              <label htmlFor="csc-base-url" className={LABEL}>
                {t("cscSettings.baseUrl")}
              </label>
              <Input
                id="csc-base-url"
                type="url"
                value={baseUrl}
                onChange={(event) => setBaseUrl(event.target.value)}
                placeholder={t("cscSettings.baseUrlPlaceholder")}
                autoComplete="off"
                spellCheck={false}
              />
            </div>

            <div className="flex flex-col gap-1">
              <label htmlFor="csc-client-id" className={LABEL}>
                {t("cscSettings.clientId")}
              </label>
              <Input
                id="csc-client-id"
                value={clientId}
                onChange={(event) => setClientId(event.target.value)}
                placeholder={t("cscSettings.clientIdPlaceholder")}
                autoComplete="off"
                spellCheck={false}
              />
              <p className={HINT}>{t("cscSettings.clientIdHint")}</p>
            </div>

            <div className="flex flex-col gap-1">
              <label htmlFor="csc-client-secret" className={LABEL}>
                {t("cscSettings.clientSecret")}
              </label>
              <Input
                id="csc-client-secret"
                type="password"
                value={clientSecret}
                onChange={(event) => setClientSecret(event.target.value)}
                placeholder={
                  initial.hasClientSecret
                    ? t("cscSettings.secretUnchanged")
                    : t("cscSettings.secretPlaceholder")
                }
                autoComplete="off"
                spellCheck={false}
              />
              <p className={HINT}>
                {initial.hasClientSecret
                  ? t("cscSettings.secretStored")
                  : t("cscSettings.secretHint")}
              </p>
            </div>
          </>
        )}

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
          {t("cscSettings.enabled")}
        </label>
        {!canEnable && (
          <p className={HINT}>{t("cscSettings.enabledNeedsUrl")}</p>
        )}

        {save.isError && (
          <p role="alert" className="text-error text-[13px]">
            {saveError(save.error, t)}
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

function TestCard({
  slug,
  ready,
}: Readonly<{
  slug: string;
  ready: boolean;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const test = useTestCscConnectionMutation(slug);

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("cscSettings.testHeading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("cscSettings.testDescription")}
      </p>
      <div className="mt-4">
        <Button
          loading={test.isPending}
          disabled={!ready}
          onClick={() => test.mutate()}
        >
          {t("cscSettings.testConnection")}
        </Button>
      </div>
      {!ready && (
        <p className="text-ink-soft mt-2 text-[12px]">
          {t("cscSettings.testNotConfigured")}
        </p>
      )}
      {test.isSuccess && (
        <p className="text-success mt-2 text-[13px]">
          {t("cscSettings.testOk", {
            name: test.data.name,
            specs: test.data.specs,
          })}
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

// CscSettingsPanel is the org's signing-provider settings, rendered as a tab on
// the Settings page. The caller (Settings) has already resolved the org and
// gated on admin, so the panel only owns the settings query and its forms.
export function CscSettingsPanel({
  slug,
}: Readonly<{
  slug: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useCscSettingsQuery(slug, true);

  if (settings.isError) {
    return (
      <Card className="p-6">
        <p className="text-error text-[14px]">
          {t("cscSettings.loadError", { message: settings.error.message })}
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
      <ConfigForm
        key={settings.data.updatedAt ?? "unset"}
        slug={slug}
        initial={settings.data}
      />
      <TestCard
        slug={slug}
        ready={
          isBuiltInCscKind(settings.data.providerKind) ||
          settings.data.baseUrl !== ""
        }
      />
    </div>
  );
}
