import { useTranslation } from "react-i18next";
import * as React from "react";
import type { ThemeColorField } from "../api/theme";
import { COLOR_FIELD_DEFAULTS, FONT_OPTIONS } from "../lib/theme";
import {
  ACCEPTED_LOGO_TYPES,
  HEX_PATTERN,
  type ThemeDraft,
} from "../lib/use-theme-draft";
import { safeImageUrl } from "../lib/safe-image-url";
import { Button, Select } from "../ui";

const EYEBROW =
  "text-muted font-label text-[11px] font-medium tracking-[0.06em] uppercase";
const GROUP_LABEL =
  "text-ink col-span-2 mt-2 text-[12px] font-semibold first:mt-0";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

// The colour fields, grouped for the form. "brand" holds the two mode-safe fills
// (applied as-is, so the primary is gated on contrast); "palette" holds the
// mode-aware roles the theme engine derives to WCAG-AA on its own. Each entry's
// labelKey resolves to an i18n string; the fallback seeds the colour picker.
const COLOR_GROUPS = [
  {
    groupKey: "themeSettings.brandGroup",
    fields: [
      { field: "primaryColor", labelKey: "themeSettings.primaryColor" },
      { field: "accentColor", labelKey: "themeSettings.accentColor" },
    ],
  },
  {
    groupKey: "themeSettings.navGroup",
    fields: [
      { field: "sidebarColor", labelKey: "themeSettings.sidebarColor" },
      { field: "topbarColor", labelKey: "themeSettings.topbarColor" },
    ],
  },
  {
    groupKey: "themeSettings.paletteGroup",
    fields: [
      { field: "textColor", labelKey: "themeSettings.textColor" },
      { field: "surfaceColor", labelKey: "themeSettings.surfaceColor" },
      { field: "borderColor", labelKey: "themeSettings.borderColor" },
      { field: "linkColor", labelKey: "themeSettings.linkColor" },
      { field: "successColor", labelKey: "themeSettings.successColor" },
      { field: "warningColor", labelKey: "themeSettings.warningColor" },
      { field: "errorColor", labelKey: "themeSettings.errorColor" },
    ],
  },
] as const satisfies ReadonlyArray<{
  groupKey: string;
  fields: ReadonlyArray<{ field: ThemeColorField; labelKey: string }>;
}>;

// One theme colour: a picker, its hex and a reset; empty shows `fallback`.
// Laid out as two cells of a label/control grid.
function ColorField({
  label,
  value,
  fallback,
  onChange,
  onClear,
  clearLabel,
}: Readonly<{
  label: string;
  value: string;
  fallback: string;
  onChange: (next: string) => void;
  onClear: () => void;
  clearLabel: string;
}>): React.JSX.Element {
  const active = HEX_PATTERN.test(value);
  // Hex is case-insensitive; normalise to lower case so the native colour
  // picker (which only accepts lower-case #rrggbb) round-trips a saved value.
  const handleChange = (next: string): void => onChange(next.toLowerCase());
  return (
    <>
      <span className={EYEBROW}>{label}</span>
      <div className="flex items-center gap-2">
        <input
          type="color"
          value={active ? value : fallback}
          onChange={(event) => handleChange(event.target.value)}
          aria-label={label}
          className="border-line-strong rounded-yivi h-9 w-10 cursor-pointer border bg-transparent p-0.5"
        />
        <input
          className={`${CONTROL} max-w-[150px] font-mono`}
          value={value}
          placeholder={fallback}
          onChange={(event) => handleChange(event.target.value)}
          aria-label={label}
        />
        {active && (
          <Button variant="ghost" size="sm" onClick={onClear}>
            {clearLabel}
          </Button>
        )}
      </div>
    </>
  );
}

// The wallet theme's fields: its colours, font and logo.
export function ThemeFields({
  draft,
}: Readonly<{ draft: ThemeDraft }>): React.JSX.Element {
  const { t } = useTranslation();
  const showRemove = draft.logoPreviewSrc !== null;
  return (
    <>
      <div className="grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        {COLOR_GROUPS.map((group) => (
          <React.Fragment key={group.groupKey}>
            <span className={GROUP_LABEL}>{t(group.groupKey)}</span>
            {group.fields.map(({ field, labelKey }) => (
              <ColorField
                key={field}
                label={t(labelKey)}
                value={draft.colors[field]}
                fallback={COLOR_FIELD_DEFAULTS[field]}
                onChange={(next) => draft.setColor(field, next)}
                onClear={() => draft.setColor(field, "")}
                clearLabel={t("themeSettings.reset")}
              />
            ))}
          </React.Fragment>
        ))}

        <span className={GROUP_LABEL}>
          {t("themeSettings.typographyGroup")}
        </span>
        <span className={EYEBROW}>{t("themeSettings.font")}</span>
        <Select
          className="w-full max-w-[220px]"
          value={draft.fontFamily}
          onChange={(event) => draft.setFontFamily(event.target.value)}
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

        <span className={GROUP_LABEL}>{t("themeSettings.logoGroup")}</span>
        <span className={EYEBROW}>{t("themeSettings.logo")}</span>
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            {draft.logoPreviewSrc && (
              <img
                src={safeImageUrl(draft.logoPreviewSrc)}
                alt={t("themeSettings.logoPreviewAlt")}
                className="max-h-9 max-w-[120px]"
              />
            )}
            <label className="rounded-yivi border-line-strong bg-surface text-ink hover:bg-surface-3 focus-within:border-ink focus-within:ring-ink/10 inline-flex h-9 cursor-pointer items-center border px-3 text-[13px] font-medium transition-colors focus-within:ring-3">
              <input
                type="file"
                accept={ACCEPTED_LOGO_TYPES.join(",")}
                className="sr-only"
                onChange={(event) =>
                  draft.selectLogo(event.target.files?.[0] ?? null)
                }
              />
              {t("themeSettings.logoChoose")}
            </label>
            {showRemove && (
              <Button variant="ghost" size="sm" onClick={draft.clearLogo}>
                {t("themeSettings.logoRemove")}
              </Button>
            )}
          </div>
          {draft.logoFile && (
            <span className="text-ink-soft truncate text-[12px]">
              {draft.logoFile.name}
            </span>
          )}
          <span className="text-muted text-[11px]">
            {t("themeSettings.logoHint")}
          </span>
        </div>
      </div>

      {draft.anyColorInvalid && (
        <p role="alert" className="text-error mt-3 text-[12px]">
          {t("themeSettings.colorInvalid")}
        </p>
      )}
      {draft.logoError && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {draft.logoError}
        </p>
      )}
      {draft.contrastFails && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("themeSettings.contrastFail", {
            ratio: (draft.primaryContrast ?? 0).toFixed(1),
          })}
        </p>
      )}
    </>
  );
}
