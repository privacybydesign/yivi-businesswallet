import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { IssuerSettings, LogoChange } from "../api/issuer";
import {
  useIssuerBundleQuery,
  useIssuerSettingsQuery,
  useUpdateIssuerSettingsMutation,
} from "../api/issuer.queries";
import { Button, Card } from "../ui";
import { JsonSnippet } from "./json-snippet";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

// Matches the backend instanceNamePattern (a lowercase URL-path slug).
const INSTANCE_NAME_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}$/;

// Keep the accepted types and size cap in step with the backend detectLogoType
// allowlist and MaxLogoBytes.
const ACCEPTED_LOGO_TYPES = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/svg+xml",
];
const MAX_LOGO_BYTES = 512 * 1024;

export function IssuerSettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useIssuerSettingsQuery(slug);

  if (settings.isPending) {
    return (
      <Card className="max-w-2xl p-7">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }
  if (settings.isError) {
    return (
      <Card className="max-w-2xl p-7">
        <p role="alert" className="text-error text-[14px]">
          {t("issuerSettings.loadError", { message: settings.error.message })}
        </p>
      </Card>
    );
  }

  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <IssuerForm
        key={settings.data.updatedAt ?? "unset"}
        slug={slug}
        settings={settings.data}
      />
      <BundlePanel slug={slug} />
    </div>
  );
}

function IssuerForm({
  slug,
  settings,
}: {
  slug: string;
  settings: IssuerSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useUpdateIssuerSettingsMutation(slug);

  const [instanceName, setInstanceName] = useState(settings.instanceName);
  const [displayName, setDisplayName] = useState(settings.displayName);
  const [enabled, setEnabled] = useState(settings.enabled);
  const [logoFile, setLogoFile] = useState<File | null>(null);
  const [newLogoUrl, setNewLogoUrl] = useState<string | null>(null);
  const [removeLogo, setRemoveLogo] = useState(false);
  const [logoError, setLogoError] = useState<string | null>(null);
  const [attempted, setAttempted] = useState(false);

  // The object URL for a freshly picked file is created in the change handler
  // (not render), so this effect only revokes the previous one when it changes
  // or the form unmounts.
  useEffect(() => {
    if (!newLogoUrl) {
      return;
    }
    return () => URL.revokeObjectURL(newLogoUrl);
  }, [newLogoUrl]);

  const trimmedInstance = instanceName.trim();
  const instanceError =
    attempted && !INSTANCE_NAME_PATTERN.test(trimmedInstance);

  const hasStoredLogo = settings.logoUri !== "";
  const showStoredLogo = hasStoredLogo && !removeLogo && logoFile === null;
  const logoPreviewSrc = logoFile
    ? newLogoUrl
    : showStoredLogo
      ? settings.logoUri
      : null;

  function handleLogoSelect(file: File | null): void {
    if (!file) {
      return;
    }
    if (!ACCEPTED_LOGO_TYPES.includes(file.type)) {
      setLogoError(t("issuerSettings.logoTypeInvalid"));
      return;
    }
    if (file.size > MAX_LOGO_BYTES) {
      setLogoError(t("issuerSettings.logoTooLarge"));
      return;
    }
    setLogoError(null);
    setRemoveLogo(false);
    setLogoFile(file);
    setNewLogoUrl(URL.createObjectURL(file));
  }

  function handleLogoRemove(): void {
    setLogoError(null);
    setLogoFile(null);
    setNewLogoUrl(null);
    setRemoveLogo(true);
  }

  function handleSave(): void {
    setAttempted(true);
    if (!INSTANCE_NAME_PATTERN.test(trimmedInstance) || save.isPending) {
      return;
    }
    const logo: LogoChange = logoFile ?? (removeLogo ? "remove" : "keep");
    save.mutate({
      instanceName: trimmedInstance,
      displayName: displayName.trim(),
      enabled,
      logo,
    });
  }

  return (
    <Card className="p-7">
      <h2 className="text-[16px] font-semibold">{t("issuerSettings.title")}</h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("issuerSettings.intro")}
      </p>
      <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        <span className={EYEBROW}>{t("issuerSettings.instanceName")}</span>
        <input
          className={`${CONTROL} font-mono`}
          value={instanceName}
          onChange={(event) => setInstanceName(event.target.value)}
          aria-label={t("issuerSettings.instanceName")}
        />
        <span className={EYEBROW}>{t("issuerSettings.displayName")}</span>
        <input
          className={CONTROL}
          value={displayName}
          onChange={(event) => setDisplayName(event.target.value)}
          aria-label={t("issuerSettings.displayName")}
        />
        <span className={EYEBROW}>{t("issuerSettings.logo")}</span>
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <label className="rounded-yivi border-line-strong bg-surface text-ink hover:bg-surface-3 focus-within:border-ink focus-within:ring-ink/10 inline-flex h-9 cursor-pointer items-center border px-3 text-[13px] font-medium transition-colors focus-within:ring-3">
              <input
                type="file"
                accept={ACCEPTED_LOGO_TYPES.join(",")}
                className="sr-only"
                onChange={(event) =>
                  handleLogoSelect(event.target.files?.[0] ?? null)
                }
              />
              {t("issuerSettings.logoChoose")}
            </label>
            {(logoFile !== null || showStoredLogo) && (
              <Button variant="ghost" size="sm" onClick={handleLogoRemove}>
                {t("issuerSettings.logoRemove")}
              </Button>
            )}
            {logoPreviewSrc && (
              <img
                src={logoPreviewSrc}
                alt={t("issuerSettings.logoPreviewAlt")}
                className="max-h-9 max-w-[120px]"
              />
            )}
          </div>
          {logoFile && (
            <span className="text-ink-soft truncate text-[12px]">
              {logoFile.name}
            </span>
          )}
          <span className="text-muted text-[11px]">
            {t("issuerSettings.logoHint")}
          </span>
        </div>
        <span className={EYEBROW}>{t("issuerSettings.enabled")}</span>
        <label className="text-ink flex cursor-pointer items-center gap-2 text-[13.5px]">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(event) => setEnabled(event.target.checked)}
          />
          {t("issuerSettings.enabledHint")}
        </label>
      </div>
      {instanceError && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("issuerSettings.instanceNameInvalid")}
        </p>
      )}
      {logoError && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {logoError}
        </p>
      )}
      <div className="mt-5">
        <Button onClick={handleSave} disabled={save.isPending}>
          {save.isPending ? t("common.saving") : t("common.save")}
        </Button>
      </div>
      {save.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {t("common.saveError", { message: save.error.message })}
        </p>
      )}
    </Card>
  );
}

// BundlePanel generates the GitOps files (issuer/did/metadata/vct) an operator
// commits to the issuer ops repo (openid4vc-poc-ops) and redeploys.
function BundlePanel({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const [show, setShow] = useState(false);
  const bundle = useIssuerBundleQuery(slug, show);

  return (
    <Card className="p-7">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-[16px] font-semibold">
          {t("issuerSettings.bundleTitle")}
        </h2>
        <Button variant="ghost" size="sm" onClick={() => setShow((v) => !v)}>
          {show
            ? t("issuerSettings.bundleHide")
            : t("issuerSettings.bundleShow")}
        </Button>
      </div>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("issuerSettings.bundleHint")}
      </p>
      {show && (
        <div className="mt-4 flex flex-col gap-3">
          {bundle.isPending && (
            <span className="text-ink-soft text-[12px]">
              {t("common.loading")}
            </span>
          )}
          {bundle.isError && (
            <span role="alert" className="text-error text-[12px]">
              {t("issuerSettings.bundleError")}
            </span>
          )}
          {bundle.data && (
            <>
              <JsonSnippet
                title={t("issuerSettings.bundleIssuer", {
                  instance: bundle.data.instance,
                })}
                value={bundle.data.issuer}
              />
              <JsonSnippet
                title={t("issuerSettings.bundleDid", {
                  instance: bundle.data.instance,
                })}
                value={bundle.data.did}
              />
              <JsonSnippet
                title={t("issuerSettings.bundleMetadata", {
                  instance: bundle.data.instance,
                })}
                value={bundle.data.metadata}
              />
              {bundle.data.vcts.map((vct) => (
                <JsonSnippet
                  key={vct.name}
                  title={t("issuerSettings.bundleVct", { name: vct.name })}
                  value={vct.document}
                />
              ))}
            </>
          )}
        </div>
      )}
    </Card>
  );
}
