import { useState } from "react";
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
  isInvalidColor,
  proofingErrorMessage,
} from "../lib/identity-proofing";
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
  MailPreview,
} from "./proofing-branding-fields";
import type { BrandingDraft } from "./proofing-branding-fields";
import { ColorField as ThemeColorField } from "./theme-settings";

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
}: Readonly<{
  slug: string;
  orgName: string;
  settings: ProofingSettings;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveBrandingMutation(slug);
  const [draft, setDraft] = useState<Draft>(() => draftFrom(settings));
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
          <BrandingFields
            idPrefix="branding"
            draft={draft}
            onChange={change}
            logo={{ uri: logoUri, source: logoUri ? "own" : "none" }}
            fallbackName={orgName}
            placeholders={{
              displayName: orgName,
              color: t("identityProofing.branding.orgColor"),
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
          <WhiteLabelFields draft={draft} onChange={change} />
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

// The white-label look beyond the mail's colour: a second colour, the org
// theme's palette and a font, for the Idem app and the hosted page.
function WhiteLabelFields({
  draft,
  onChange,
}: Readonly<{
  draft: Draft;
  onChange: (patch: Partial<Draft>) => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-5 px-6 pb-5">
      <ColorField
        id="branding-secondary-color"
        label={t("identityProofing.branding.secondaryColor")}
        hint={t("identityProofing.branding.secondaryColorHint")}
        hexLabel={t("identityProofing.branding.secondaryColor")}
        placeholder={t("identityProofing.branding.orgColor")}
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
              fallback={COLOR_FIELD_DEFAULTS[field]}
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
                {option.name}
              </option>
            ))}
          </Select>
        </div>
      </div>
    </div>
  );
}
