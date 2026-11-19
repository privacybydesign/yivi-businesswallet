import { useRef } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { isHexColor, isInvalidColor } from "../lib/identity-proofing";
import { Button, Input } from "../ui";
import { InitialMark } from "./proofing-ui";

export const LABEL = "text-ink text-[13px] font-semibold";
export const HINT = "text-muted text-[12px]";
export const ERROR = "text-error text-[12.5px]";
export const CAPTION =
  "text-muted font-label text-[10.5px] font-medium tracking-[0.08em] uppercase";
// A few starting points; any #rrggbb works.
const PRESET_COLORS = [
  { color: "#1E5A47", nameKey: "identityProofing.branding.swatches.green" },
  { color: "#22337A", nameKey: "identityProofing.branding.swatches.blue" },
  { color: "#8E2440", nameKey: "identityProofing.branding.swatches.red" },
  { color: "#1F1E1E", nameKey: "identityProofing.branding.swatches.black" },
] as const;
// Shown when there is no colour: the Yivi default the mail falls back to.
export const FALLBACK_PREVIEW_COLOR = "#1A1A1A";
const LOGO_ACCEPT = "image/png,image/jpeg,image/gif,image/webp";

// The fields a branding form edits; a picked logo file or its removal is kept
// beside the text until it is saved.
export interface BrandingDraft {
  displayName: string;
  primaryColor: string;
  supportContact: string;
  privacyUrl: string;
  logo: File | null;
  removeLogo: boolean;
}

// Where the logo row's image comes from: the form's own logo, another's it
// falls back to, or none.
type LogoSource = "own" | "inherited" | "none";

// What the logo row says when no new file is picked, per logo source.
const LOGO_STATE_KEYS = {
  own: "identityProofing.branding.logoSet",
  inherited: "identityProofingFlows.branding.logoInherited",
  none: "identityProofing.branding.noLogo",
} as const satisfies Record<LogoSource, string>;

// The branding fields shared by the org's and a flow's form: display name,
// logo, colour (unless hideColor), support contact and privacy statement.
// children are further controls for the contact grid.
export function BrandingFields({
  idPrefix,
  draft,
  onChange,
  logo,
  fallbackName,
  fallbackColor = FALLBACK_PREVIEW_COLOR,
  placeholders,
  hideColor = false,
  children,
}: Readonly<{
  idPrefix: string;
  draft: BrandingDraft;
  onChange: (patch: Partial<BrandingDraft>) => void;
  logo: { uri: string; source: LogoSource };
  // The name and colour of the mark shown while there is no logo.
  fallbackName: string;
  fallbackColor?: string;
  placeholders: {
    displayName: string;
    color: string;
    support: string;
    privacy: string;
  };
  hideColor?: boolean;
  children?: React.ReactNode;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const fileInput = useRef<HTMLInputElement>(null);

  return (
    <div className="flex flex-col gap-5 px-6 py-5">
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`${idPrefix}-display-name`} className={LABEL}>
          {t("identityProofing.branding.displayName")}
        </label>
        <Input
          id={`${idPrefix}-display-name`}
          value={draft.displayName}
          placeholder={placeholders.displayName}
          onChange={(e) => onChange({ displayName: e.target.value })}
        />
        <p className={HINT}>{t("identityProofing.branding.displayNameHint")}</p>
      </div>

      <div className="flex flex-col gap-1.5">
        <span className={LABEL}>{t("identityProofing.branding.logo")}</span>
        <div className="border-line-strong flex items-center gap-3 rounded-lg border border-dashed p-3">
          {logo.uri ? (
            <img
              src={logo.uri}
              alt=""
              className="bg-surface-3 h-11 w-11 shrink-0 rounded-md object-contain"
            />
          ) : (
            <InitialMark
              name={draft.displayName.trim() || fallbackName}
              color={
                isHexColor(draft.primaryColor)
                  ? draft.primaryColor
                  : fallbackColor
              }
              className="h-11 w-11 rounded-md text-[17px]"
            />
          )}
          <div className="min-w-0 flex-1">
            <div className="text-ink text-[13px] font-semibold wrap-anywhere">
              {draft.logo?.name ?? t(LOGO_STATE_KEYS[logo.source])}
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
              onChange({ logo: file, removeLogo: false });
              e.target.value = "";
            }}
          />
          <Button
            type="button"
            size="sm"
            variant="secondary"
            onClick={() => fileInput.current?.click()}
          >
            {logo.source === "own"
              ? t("identityProofing.branding.replace")
              : t("identityProofing.branding.upload")}
          </Button>
          {logo.source === "own" && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => onChange({ logo: null, removeLogo: true })}
            >
              {t("identityProofing.branding.removeLogo")}
            </Button>
          )}
        </div>
      </div>

      {!hideColor && (
        <ColorField
          id={`${idPrefix}-color`}
          label={t("identityProofing.branding.primaryColor")}
          hint={t("identityProofing.branding.primaryColorHint")}
          hexLabel={t("identityProofing.branding.hexColor")}
          placeholder={placeholders.color}
          value={draft.primaryColor}
          onChange={(primaryColor) => onChange({ primaryColor })}
        />
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${idPrefix}-support`} className={LABEL}>
            {t("identityProofing.branding.supportContact")}
          </label>
          <Input
            id={`${idPrefix}-support`}
            value={draft.supportContact}
            placeholder={placeholders.support}
            onChange={(e) => onChange({ supportContact: e.target.value })}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${idPrefix}-privacy`} className={LABEL}>
            {t("identityProofing.branding.privacyUrl")}
          </label>
          <Input
            id={`${idPrefix}-privacy`}
            type="url"
            value={draft.privacyUrl}
            placeholder={placeholders.privacy}
            onChange={(e) => onChange({ privacyUrl: e.target.value })}
          />
        </div>
        {children}
      </div>
    </div>
  );
}

// One of the branding's colours: a few named presets and the hex itself; empty
// falls back.
export function ColorField({
  id,
  label,
  hint,
  hexLabel,
  placeholder,
  value,
  onChange,
}: Readonly<{
  id: string;
  label: string;
  hint: string;
  hexLabel: string;
  placeholder: string;
  value: string;
  onChange: (value: string) => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const invalid = isInvalidColor(value);
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
            placeholder={placeholder}
            aria-label={hexLabel}
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
