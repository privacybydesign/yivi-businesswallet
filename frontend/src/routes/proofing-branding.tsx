import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useProofingFlowsQuery,
  useProofingSettingsQuery,
  useSaveBrandingMutation,
} from "../api/identity-proofing.queries";
import {
  PROOFING_THEME_COLOR_FIELDS,
  type ProofingBranding,
  type ProofingThemeColorField,
  type ProofingSettings,
} from "../api/identity-proofing";
import { useOrgThemeQuery } from "../api/theme.queries";
import type { OrgTheme } from "../api/theme";
import { COLOR_FIELD_DEFAULTS, FONT_OPTIONS } from "../lib/theme";
import {
  isHexColor,
  isInvalidColor,
  proofingErrorMessage,
} from "../lib/identity-proofing";
import { blockingError } from "../lib/query-state";
import { useLogoPreview } from "../lib/use-logo-preview";
import { absoluteApiUrl } from "../api/http";
import { Button, Card, Select } from "../ui";
import {
  BrandingFields,
  CAPTION,
  ColorField,
  ERROR,
  HINT,
  LABEL,
} from "./proofing-branding-fields";
import type { BrandingDraft } from "./proofing-branding-fields";
import { ColorField as ThemeColorField } from "./theme-settings";
import { BrandingPreviews } from "./branding-preview";

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

interface Draft extends BrandingDraft, Record<ProofingThemeColorField, string> {
  secondaryColor: string;
  fontFamily: string;
  hidePoweredBy: boolean;
}

// The fields that give external people another look than the wallet's; each
// left empty is the wallet's.
const LOOK_FIELDS = [
  "primaryColor",
  "secondaryColor",
  ...PROOFING_THEME_COLOR_FIELDS,
  "fontFamily",
] as const satisfies readonly (keyof Draft)[];

function hasOwnLook(draft: Draft): boolean {
  return LOOK_FIELDS.some((field) => draft[field] !== "");
}

function withoutOwnLook(draft: Draft): Draft {
  const out = { ...draft };
  for (const field of LOOK_FIELDS) {
    out[field] = "";
  }
  return out;
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
}: Readonly<{
  slug: string;
  orgName: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useProofingSettingsQuery(slug);
  const theme = useOrgThemeQuery(slug);

  const error = blockingError(settings, theme);
  if (error !== null) {
    return (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(error, t)}
      </p>
    );
  }
  if (settings.data === undefined || theme.data === undefined) {
    return <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>;
  }
  return (
    <BrandingTab
      slug={slug}
      orgName={orgName}
      settings={settings.data}
      theme={theme.data}
    />
  );
}

// The look of the proofing mail external people get, and of the hosted page:
// the name it signs with, its logo and colour, and its support contact and
// privacy statement, with a live preview beside the form. Its colours and font
// are the wallet's unless it sets its own.
function BrandingTab({
  slug,
  orgName,
  settings,
  theme,
}: Readonly<{
  slug: string;
  orgName: string;
  settings: ProofingSettings;
  theme: OrgTheme;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveBrandingMutation(slug);
  const [draft, setDraft] = useState<Draft>(() => draftFrom(settings));
  const [ownLook, setOwnLook] = useState(() => hasOwnLook(draftFrom(settings)));
  const logoPreview = useLogoPreview(draft.logo);

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
  ].some(isInvalidColor);
  const logoUri =
    logoPreview ??
    (draft.removeLogo || !settings.branding.logoUri
      ? ""
      : absoluteApiUrl(settings.branding.logoUri));
  const change = (patch: Partial<Draft>): void =>
    setDraft((d) => ({ ...d, ...patch }));
  const flows = useProofingFlowsQuery(slug);
  const shown: ProofingBranding = {
    ...settings.branding,
    displayName: draft.displayName.trim(),
    primaryColor: hexOrEmpty(draft.primaryColor),
    secondaryColor: hexOrEmpty(draft.secondaryColor),
    ...Object.fromEntries(
      PROOFING_THEME_COLOR_FIELDS.map((field) => [
        field,
        hexOrEmpty(draft[field]),
      ]),
    ),
    fontFamily: draft.fontFamily,
    supportContact: draft.supportContact.trim(),
    privacyUrl: draft.privacyUrl.trim(),
    hidePoweredBy: draft.hidePoweredBy,
    logoUri: logoUri || theme.logoUri,
  };
  const name = shown.displayName || orgName;

  function setWalletLook(on: boolean): void {
    setOwnLook(!on);
    if (on) {
      setDraft(withoutOwnLook);
    }
  }

  function reset(next: Draft): void {
    setDraft(next);
    setOwnLook(hasOwnLook(next));
  }

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
        onSuccess: (next) => reset(draftFrom(next)),
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
          <BrandingFields
            idPrefix="branding"
            draft={draft}
            onChange={change}
            logo={{
              uri: logoUri || theme.logoUri,
              source: logoSource(logoUri, theme.logoUri),
            }}
            fallbackName={orgName}
            fallbackColor={
              isHexColor(theme.primaryColor) ? theme.primaryColor : undefined
            }
            hideColor={!ownLook}
            placeholders={{
              displayName: orgName,
              color:
                theme.primaryColor || t("identityProofing.branding.orgColor"),
              support: t("identityProofing.branding.supportPlaceholder"),
              privacy: t("identityProofing.branding.privacyPlaceholder"),
            }}
          >
            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                className="h-4 w-4"
                checked={draft.hidePoweredBy}
                onChange={(e) => change({ hidePoweredBy: e.target.checked })}
              />
              {t("identityProofing.branding.hidePoweredBy")}
            </label>
          </BrandingFields>
          <div className="flex flex-col gap-1 px-6 pb-5">
            <label className="flex items-center gap-2 text-[13px] font-semibold">
              <input
                type="checkbox"
                className="h-4 w-4"
                checked={!ownLook}
                onChange={(e) => setWalletLook(e.target.checked)}
              />
              {t("identityProofing.branding.useWalletLook")}
            </label>
            <p className={`${HINT} pl-6`}>
              {t("identityProofing.branding.useWalletLookHint")}
            </p>
          </div>
          {ownLook && (
            <WhiteLabelFields draft={draft} onChange={change} theme={theme} />
          )}
          {save.isError && (
            <p className={`${ERROR} px-6 pb-5`}>
              {proofingErrorMessage(save.error, t)}
            </p>
          )}
          <div className="border-line flex justify-end gap-2 border-t px-6 py-4">
            <Button
              type="button"
              variant="secondary"
              disabled={!dirty || save.isPending}
              onClick={() => reset(draftFrom(settings))}
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
      <BrandingPreviews
        slug={slug}
        orgName={orgName}
        name={name}
        branding={shown}
        theme={theme}
        flow={flows.data?.find((flow) => flow.default)}
        dataRetentionDays={settings.dataRetentionDays}
        sessionTtlSeconds={settings.sessionTtlSeconds}
        mail={{
          displayName: draft.displayName,
          primaryColor: shown.primaryColor,
          supportContact: shown.supportContact,
          privacyUrl: shown.privacyUrl,
          logo: draft.logo ?? undefined,
          removeLogo: draft.removeLogo,
        }}
      />
    </div>
  );
}

// The white-label look beyond the mail's colour: a second colour, the org
// theme's palette and a font, for the Idem app and the hosted page. Each
// shows the wallet's value while empty.
function WhiteLabelFields({
  draft,
  onChange,
  theme,
}: Readonly<{
  draft: Draft;
  onChange: (patch: Partial<Draft>) => void;
  theme: OrgTheme;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const walletFont = FONT_OPTIONS.find(
    (option) => option.value !== "" && option.value === theme.fontFamily,
  );
  return (
    <div className="flex flex-col gap-5 px-6 pb-5">
      <ColorField
        id="branding-secondary-color"
        label={t("identityProofing.branding.secondaryColor")}
        hint={t("identityProofing.branding.secondaryColorHint")}
        hexLabel={t("identityProofing.branding.secondaryColor")}
        placeholder={
          theme.accentColor || t("identityProofing.branding.orgColor")
        }
        value={draft.secondaryColor}
        onChange={(value) => onChange({ secondaryColor: value })}
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
              fallback={theme[field] || COLOR_FIELD_DEFAULTS[field]}
              onChange={(value) => onChange({ [field]: value })}
              onClear={() => onChange({ [field]: "" })}
              clearLabel={t("themeSettings.reset")}
            />
          ))}
          <span className={CAPTION}>{t("themeSettings.font")}</span>
          <Select
            className="w-full max-w-[220px]"
            value={draft.fontFamily}
            onChange={(e) => onChange({ fontFamily: e.target.value })}
            aria-label={t("themeSettings.font")}
            style={{ fontFamily: draft.fontFamily || undefined }}
          >
            {FONT_OPTIONS.map((option) => (
              <option
                key={option.name}
                value={option.value}
                style={{ fontFamily: option.value || undefined }}
              >
                {option.value === "" && walletFont
                  ? t("identityProofing.branding.walletFont", {
                      name: walletFont.name,
                    })
                  : option.name}
              </option>
            ))}
          </Select>
        </div>
      </div>
    </div>
  );
}

// Where the logo row's image comes from: the branding's own, else the
// wallet's it falls back to, else none.
function logoSource(
  ownUri: string,
  walletUri: string,
): "own" | "inherited" | "none" {
  if (ownUri) {
    return "own";
  }
  return walletUri ? "inherited" : "none";
}

function hexOrEmpty(value: string): string {
  return isHexColor(value) ? value : "";
}
