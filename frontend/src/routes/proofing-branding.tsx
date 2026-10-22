import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useProofingSettingsQuery,
  useSaveBrandingMutation,
} from "../api/identity-proofing.queries";
import {
  PROOFING_THEME_COLOR_FIELDS,
  type ProofingThemeColorField,
  type ProofingSettings,
} from "../api/identity-proofing";
import { COLOR_FIELD_DEFAULTS, FONT_OPTIONS } from "../lib/theme";
import {
  isHexColor,
  proofingErrorMessage,
  readableTextOn,
} from "../lib/identity-proofing";
import { absoluteApiUrl } from "../api/http";
import { Button, Card, Icon, Input } from "../ui";
import { InitialMark } from "./proofing-ui";
import { ColorField as ThemeColorField } from "./theme-settings";

const LABEL = "text-ink text-[13px] font-semibold";
const HINT = "text-muted text-[12px]";
const ERROR = "text-error text-[12.5px]";
const CAPTION =
  "text-muted font-label text-[10.5px] font-medium tracking-[0.08em] uppercase";
// A few starting points; any #rrggbb works.
const PRESET_COLORS = [
  { color: "#1E5A47", nameKey: "identityProofing.branding.swatches.green" },
  { color: "#22337A", nameKey: "identityProofing.branding.swatches.blue" },
  { color: "#8E2440", nameKey: "identityProofing.branding.swatches.red" },
  { color: "#1F1E1E", nameKey: "identityProofing.branding.swatches.black" },
] as const;
// Shown when the org has no colour: the Yivi default the mail falls back to.
const FALLBACK_PREVIEW_COLOR = "#1A1A1A";
const LOGO_ACCEPT = "image/png,image/jpeg,image/gif,image/webp";
const PREVIEW_QR_ICON = 40;
const PREVIEW_MARK = "h-6 w-6 rounded text-[11px]";
const SELECT =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full max-w-[220px] border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";
// Each further colour's label, the org theme settings' own.
const THEME_COLOR_LABELS = {
  textColor: "themeSettings.textColor",
  surfaceColor: "themeSettings.surfaceColor",
  borderColor: "themeSettings.borderColor",
  linkColor: "themeSettings.linkColor",
  successColor: "themeSettings.successColor",
  warningColor: "themeSettings.warningColor",
  errorColor: "themeSettings.errorColor",
  topbarColor: "themeSettings.topbarColor",
} as const satisfies Record<ProofingThemeColorField, string>;

interface Draft extends Record<ProofingThemeColorField, string> {
  displayName: string;
  primaryColor: string;
  secondaryColor: string;
  fontFamily: string;
  supportContact: string;
  privacyUrl: string;
  hidePoweredBy: boolean;
  logo: File | null;
  removeLogo: boolean;
}

function draftFrom(settings: ProofingSettings): Draft {
  const b = settings.branding;
  return {
    displayName: b.displayName,
    primaryColor: b.primaryColor,
    secondaryColor: b.secondaryColor,
    textColor: b.textColor,
    surfaceColor: b.surfaceColor,
    borderColor: b.borderColor,
    linkColor: b.linkColor,
    successColor: b.successColor,
    warningColor: b.warningColor,
    errorColor: b.errorColor,
    topbarColor: b.topbarColor,
    fontFamily: b.fontFamily,
    supportContact: b.supportContact,
    privacyUrl: b.privacyUrl,
    hidePoweredBy: b.hidePoweredBy,
    logo: null,
    removeLogo: false,
  };
}

// Settings › Branding: the org's branding as saved, then the form for it.
export function BrandingPanel({
  slug,
  orgName,
}: {
  slug: string;
  orgName: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useProofingSettingsQuery(slug);

  if (settings.isPending) {
    return <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>;
  }
  if (settings.isError) {
    return (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(settings.error, t)}
      </p>
    );
  }
  return <BrandingTab slug={slug} orgName={orgName} settings={settings.data} />;
}

// The look of the proofing mail external people get, and of the hosted page:
// the name it signs with, its logo and colour, and its support contact and
// privacy statement, with a live preview beside the form.
function BrandingTab({
  slug,
  orgName,
  settings,
}: {
  slug: string;
  orgName: string;
  settings: ProofingSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveBrandingMutation(slug);
  const [draft, setDraft] = useState<Draft>(() => draftFrom(settings));
  const fileInput = useRef<HTMLInputElement>(null);
  // A picked file previews from a local object URL, freed when replaced.
  const logoPreview = useMemo(
    () => (draft.logo ? URL.createObjectURL(draft.logo) : null),
    [draft.logo],
  );
  useEffect(
    () => () => {
      if (logoPreview) URL.revokeObjectURL(logoPreview);
    },
    [logoPreview],
  );

  const saved = draftFrom(settings);
  const dirty =
    draft.logo !== null ||
    draft.removeLogo ||
    draft.displayName !== saved.displayName ||
    draft.primaryColor !== saved.primaryColor ||
    draft.secondaryColor !== saved.secondaryColor ||
    PROOFING_THEME_COLOR_FIELDS.some(
      (field) => draft[field] !== saved[field],
    ) ||
    draft.fontFamily !== saved.fontFamily ||
    draft.supportContact !== saved.supportContact ||
    draft.privacyUrl !== saved.privacyUrl ||
    draft.hidePoweredBy !== saved.hidePoweredBy;
  const colorInvalid = [
    draft.primaryColor,
    draft.secondaryColor,
    ...PROOFING_THEME_COLOR_FIELDS.map((field) => draft[field]),
  ].some((color) => !isHexOrEmpty(color));
  const logoUri =
    logoPreview ??
    (draft.removeLogo || !settings.branding.logoUri
      ? ""
      : absoluteApiUrl(settings.branding.logoUri));
  const set = <K extends keyof Draft>(key: K, value: Draft[K]): void =>
    setDraft((d) => ({ ...d, [key]: value }));

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    if (colorInvalid) {
      return;
    }
    save.mutate(
      {
        displayName: draft.displayName,
        primaryColor: draft.primaryColor,
        secondaryColor: draft.secondaryColor,
        textColor: draft.textColor,
        surfaceColor: draft.surfaceColor,
        borderColor: draft.borderColor,
        linkColor: draft.linkColor,
        successColor: draft.successColor,
        warningColor: draft.warningColor,
        errorColor: draft.errorColor,
        topbarColor: draft.topbarColor,
        fontFamily: draft.fontFamily,
        supportContact: draft.supportContact,
        privacyUrl: draft.privacyUrl,
        hidePoweredBy: draft.hidePoweredBy,
        logo: draft.logo ?? undefined,
        removeLogo: draft.removeLogo,
      },
      {
        onSuccess: (next) => setDraft(draftFrom(next)),
      },
    );
  }

  return (
    <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
      <Card>
        <form onSubmit={submit} noValidate>
          <div className="border-line border-b px-6 py-4">
            <h2 className="font-display text-[17px] font-bold">
              {t("identityProofing.branding.title")}
            </h2>
            <p className={`${HINT} mt-1`}>
              {t("identityProofing.branding.hint")}
            </p>
          </div>
          <div className="flex flex-col gap-5 px-6 py-5">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="branding-display-name" className={LABEL}>
                {t("identityProofing.branding.displayName")}
              </label>
              <Input
                id="branding-display-name"
                value={draft.displayName}
                placeholder={orgName}
                onChange={(e) => set("displayName", e.target.value)}
              />
              <p className={HINT}>
                {t("identityProofing.branding.displayNameHint")}
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className={LABEL}>
                {t("identityProofing.branding.logo")}
              </span>
              <div className="border-line-strong flex items-center gap-3 rounded-lg border border-dashed p-3">
                {logoUri ? (
                  <img
                    src={logoUri}
                    alt=""
                    className="bg-surface-3 h-11 w-11 shrink-0 rounded-md object-contain"
                  />
                ) : (
                  <InitialMark
                    name={draft.displayName.trim() || orgName}
                    color={
                      isHexColor(draft.primaryColor)
                        ? draft.primaryColor
                        : FALLBACK_PREVIEW_COLOR
                    }
                    className="h-11 w-11 rounded-md text-[17px]"
                  />
                )}
                <div className="min-w-0 flex-1">
                  <div className="text-ink text-[13px] font-semibold wrap-anywhere">
                    {draft.logo?.name ??
                      (logoUri
                        ? t("identityProofing.branding.logoSet")
                        : t("identityProofing.branding.noLogo"))}
                  </div>
                  <div className={HINT}>
                    {t("identityProofing.branding.logoHint")}
                  </div>
                </div>
                <input
                  ref={fileInput}
                  type="file"
                  accept={LOGO_ACCEPT}
                  className="sr-only"
                  aria-label={t("identityProofing.branding.logo")}
                  onChange={(e) => {
                    const file = e.target.files?.[0] ?? null;
                    setDraft((d) => ({ ...d, logo: file, removeLogo: false }));
                    e.target.value = "";
                  }}
                />
                <Button
                  type="button"
                  size="sm"
                  variant="secondary"
                  onClick={() => fileInput.current?.click()}
                >
                  {logoUri
                    ? t("identityProofing.branding.replace")
                    : t("identityProofing.branding.upload")}
                </Button>
                {logoUri && (
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() =>
                      setDraft((d) => ({ ...d, logo: null, removeLogo: true }))
                    }
                  >
                    {t("identityProofing.branding.removeLogo")}
                  </Button>
                )}
              </div>
            </div>

            <ColorField
              id="branding-primary-color"
              label={t("identityProofing.branding.primaryColor")}
              hint={t("identityProofing.branding.primaryColorHint")}
              value={draft.primaryColor}
              onChange={(value) => set("primaryColor", value)}
            />

            <ColorField
              id="branding-secondary-color"
              label={t("identityProofing.branding.secondaryColor")}
              hint={t("identityProofing.branding.secondaryColorHint")}
              value={draft.secondaryColor}
              onChange={(value) => set("secondaryColor", value)}
            />

            <div className="flex flex-col gap-1.5">
              <span className={LABEL}>
                {t("identityProofing.branding.themeTitle")}
              </span>
              <p className={HINT}>{t("identityProofing.branding.themeHint")}</p>
              <div className="mt-2 grid grid-cols-1 gap-x-5 gap-y-1.5 sm:grid-cols-[180px_minmax(0,1fr)] sm:items-center sm:gap-y-3.5">
                {PROOFING_THEME_COLOR_FIELDS.map((field) => (
                  <ThemeColorField
                    key={field}
                    label={t(THEME_COLOR_LABELS[field])}
                    value={draft[field]}
                    fallback={COLOR_FIELD_DEFAULTS[field]}
                    onChange={(value) => set(field, value)}
                    onClear={() => set(field, "")}
                    clearLabel={t("themeSettings.reset")}
                  />
                ))}
                <span className={CAPTION}>{t("themeSettings.font")}</span>
                <select
                  className={SELECT}
                  value={draft.fontFamily}
                  onChange={(e) => set("fontFamily", e.target.value)}
                  aria-label={t("themeSettings.font")}
                  style={{ fontFamily: draft.fontFamily || undefined }}
                >
                  {FONT_OPTIONS.map((option) => (
                    <option
                      key={option.name}
                      value={option.value}
                      style={{ fontFamily: option.value || undefined }}
                    >
                      {option.name}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <div className="flex flex-col gap-1.5">
                <label htmlFor="branding-support" className={LABEL}>
                  {t("identityProofing.branding.supportContact")}
                </label>
                <Input
                  id="branding-support"
                  value={draft.supportContact}
                  placeholder={t(
                    "identityProofing.branding.supportPlaceholder",
                  )}
                  onChange={(e) => set("supportContact", e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <label htmlFor="branding-privacy" className={LABEL}>
                  {t("identityProofing.branding.privacyUrl")}
                </label>
                <Input
                  id="branding-privacy"
                  type="url"
                  value={draft.privacyUrl}
                  placeholder={t(
                    "identityProofing.branding.privacyPlaceholder",
                  )}
                  onChange={(e) => set("privacyUrl", e.target.value)}
                />
              </div>
              <label className="flex items-center gap-2 text-[13px]">
                <input
                  type="checkbox"
                  className="h-4 w-4"
                  checked={draft.hidePoweredBy}
                  onChange={(e) => set("hidePoweredBy", e.target.checked)}
                />
                {t("identityProofing.branding.hidePoweredBy")}
              </label>
            </div>
            {save.isError && (
              <p className={ERROR}>{proofingErrorMessage(save.error, t)}</p>
            )}
          </div>
          <div className="border-line flex justify-end gap-2 border-t px-6 py-4">
            <Button
              type="button"
              variant="secondary"
              disabled={!dirty || save.isPending}
              onClick={() => setDraft(draftFrom(settings))}
            >
              {t("identityProofing.branding.discard")}
            </Button>
            <Button
              type="submit"
              loading={save.isPending}
              disabled={!dirty || colorInvalid}
            >
              {t("identityProofing.branding.save")}
            </Button>
          </div>
        </form>
      </Card>
      <MailPreview
        name={draft.displayName.trim() || orgName}
        color={isHexColor(draft.primaryColor) ? draft.primaryColor : ""}
        logoUri={logoUri}
        supportContact={draft.supportContact.trim()}
        privacyUrl={draft.privacyUrl.trim()}
      />
    </div>
  );
}

function isHexOrEmpty(value: string): boolean {
  return value === "" || isHexColor(value);
}

// One of the branding's colours: a few named presets and the hex itself; empty
// falls back to the organisation's.
function ColorField({
  id,
  label,
  hint,
  value,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  value: string;
  onChange: (value: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const invalid = !isHexOrEmpty(value);
  return (
    <div className="flex flex-col gap-1.5">
      <span id={`${id}-label`} className={LABEL}>
        {label}
      </span>
      <fieldset
        aria-labelledby={`${id}-label`}
        className="flex min-w-0 flex-wrap items-center gap-2"
      >
        {PRESET_COLORS.map(({ color, nameKey }) => {
          const active = value.toLowerCase() === color.toLowerCase();
          return (
            <button
              key={color}
              type="button"
              aria-pressed={active}
              aria-label={t(nameKey)}
              onClick={() => onChange(color)}
              className={[
                "h-9 w-9 rounded-md transition-shadow",
                active
                  ? "ring-ink ring-offset-surface ring-2 ring-offset-2"
                  : "",
              ].join(" ")}
              style={{ backgroundColor: color }}
            />
          );
        })}
        <div className="ml-2 w-32">
          <Input
            value={value}
            placeholder={t("identityProofing.branding.orgColor")}
            aria-label={label}
            aria-invalid={invalid}
            className="font-mono text-[12.5px]"
            onChange={(e) => onChange(e.target.value.trim())}
          />
        </div>
      </fieldset>
      {invalid ? (
        <p className={ERROR}>{t("identityProofing.branding.colorInvalid")}</p>
      ) : (
        <p className={HINT}>{hint}</p>
      )}
    </div>
  );
}

// A sketch of the proofing mail with the draft branding: what the subject
// sees before they scan.
function MailPreview({
  name,
  color,
  logoUri,
  supportContact,
  privacyUrl,
}: {
  name: string;
  color: string;
  logoUri: string;
  supportContact: string;
  privacyUrl: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const fill = color || FALLBACK_PREVIEW_COLOR;
  const onFill = readableTextOn(fill);
  return (
    <div>
      <div className={`${CAPTION} mb-2`}>
        {t("identityProofing.branding.preview.caption")}
      </div>
      <div className="border-line bg-surface-2 rounded-xl border p-4">
        <div className="mb-3 flex items-center gap-2">
          {logoUri ? (
            <img
              src={logoUri}
              alt=""
              className={`bg-surface-3 object-contain ${PREVIEW_MARK}`}
            />
          ) : (
            <InitialMark name={name} color={fill} className={PREVIEW_MARK} />
          )}
          <span className="text-ink text-[13px] font-semibold">{name}</span>
        </div>
        <div className="bg-surface border-line rounded-lg border p-5">
          <h3 className="text-ink text-[17px] leading-snug font-bold">
            {t("identityProofing.branding.preview.heading", { name })}
          </h3>
          <p className="text-ink-soft mt-2 text-[12.5px]">
            {t("identityProofing.branding.preview.body", { name })}
          </p>
          <div className="border-line text-muted mx-auto my-4 flex h-24 w-24 items-center justify-center rounded-md border">
            <Icon name="scan_qrcode" size={PREVIEW_QR_ICON} />
          </div>
          <div
            className="rounded-md py-2.5 text-center text-[13px] font-semibold"
            style={{ backgroundColor: fill, color: onFill }}
          >
            {t("identityProofing.branding.preview.button")}
          </div>
          {supportContact && (
            <p className="text-ink-soft mt-3 text-[12px]">
              {t("identityProofing.branding.preview.support", {
                contact: supportContact,
              })}
            </p>
          )}
          {privacyUrl && (
            <p className="text-ink-soft mt-1 text-[12px] break-all">
              {t("identityProofing.branding.preview.privacy", {
                url: privacyUrl,
              })}
            </p>
          )}
        </div>
        <p className="text-muted mt-3 text-center text-[11px]">
          {t("identityProofing.branding.preview.footer")}
        </p>
      </div>
    </div>
  );
}
