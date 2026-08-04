import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { LogoChange, OrgTheme, ThemeColorField } from "../api/theme";
import {
  useOrgThemeQuery,
  useUpdateOrgThemeMutation,
} from "../api/theme.queries";
import {
  AA_CONTRAST,
  COLOR_FIELD_DEFAULTS,
  DEFAULT_PRIMARY,
  primaryContrastFloor,
  readableForeground,
} from "../lib/theme";
import { Button, Card } from "../ui";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const GROUP_LABEL =
  "text-ink col-span-2 mt-2 text-[12px] font-semibold first:mt-0";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

const HEX_PATTERN = /^#[0-9a-fA-F]{6}$/;

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

// Curated body-font options. Values are full font-family stacks; each is either
// a bundled font (Open Sans, Alexandria) shipped with its open licence, or a
// system/web-safe stack rendered from the client's own fonts — so nothing is
// distributed by us and there is no commercial-licence exposure. The default
// (Open Sans) stores "" so an unset theme keeps the app default. Names are
// proper nouns and stay untranslated.
const FONT_OPTIONS = [
  { name: "Open Sans", value: "" },
  { name: "Alexandria", value: '"Alexandria", system-ui, sans-serif' },
  {
    name: "System UI",
    value: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  },
  { name: "Helvetica / Arial", value: '"Helvetica Neue", Arial, sans-serif' },
  { name: "Georgia", value: 'Georgia, "Times New Roman", serif' },
  {
    name: "Monospace",
    value: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
  },
] as const;

const ALL_COLOR_FIELDS = COLOR_GROUPS.flatMap((g) =>
  g.fields.map((f) => f.field),
);

type Colors = Record<ThemeColorField, string>;

function colorsFromSettings(settings: OrgTheme): Colors {
  return ALL_COLOR_FIELDS.reduce((acc, field) => {
    acc[field] = settings[field];
    return acc;
  }, {} as Colors);
}

export function ThemeSettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const theme = useOrgThemeQuery(slug);

  if (theme.isPending) {
    return (
      <Card className="max-w-2xl p-7">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }
  if (theme.isError) {
    return (
      <Card className="max-w-2xl p-7">
        <p role="alert" className="text-error text-[14px]">
          {t("themeSettings.loadError", { message: theme.error.message })}
        </p>
      </Card>
    );
  }

  return (
    <ThemeForm
      key={theme.data.updatedAt ?? "unset"}
      slug={slug}
      settings={theme.data}
    />
  );
}

function ColorField({
  label,
  value,
  fallback,
  onChange,
  onClear,
  clearLabel,
}: {
  label: string;
  value: string;
  fallback: string;
  onChange: (next: string) => void;
  onClear: () => void;
  clearLabel: string;
}): React.JSX.Element {
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

function ThemeForm({
  slug,
  settings,
}: {
  slug: string;
  settings: OrgTheme;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useUpdateOrgThemeMutation(slug);

  const [colors, setColors] = useState<Colors>(() =>
    colorsFromSettings(settings),
  );
  const [fontFamily, setFontFamily] = useState(settings.fontFamily);
  const [logoFile, setLogoFile] = useState<File | null>(null);
  const [newLogoUrl, setNewLogoUrl] = useState<string | null>(null);
  const [removeLogo, setRemoveLogo] = useState(false);
  const [logoError, setLogoError] = useState<string | null>(null);

  const setColor = (field: ThemeColorField, next: string): void =>
    setColors((prev) => ({ ...prev, [field]: next }));

  // The object URL for a freshly picked file is created in the change handler
  // (not render), so this effect only revokes the previous one when it changes
  // or the form unmounts.
  useEffect(() => {
    if (!newLogoUrl) {
      return;
    }
    return () => URL.revokeObjectURL(newLogoUrl);
  }, [newLogoUrl]);

  const primary = colors.primaryColor;
  const primaryActive = HEX_PATTERN.test(primary);
  const primaryContrast = primaryActive
    ? (primaryContrastFloor(primary) ?? 0)
    : null;
  const contrastFails =
    primaryContrast !== null && primaryContrast < AA_CONTRAST;

  // A field is invalid only when it is non-empty and malformed (empty clears it
  // back to the default look). The extra palette roles are auto-corrected to AA
  // by the theme engine, so only the as-applied primary is gated on contrast.
  const anyColorInvalid = ALL_COLOR_FIELDS.some(
    (field) => colors[field] !== "" && !HEX_PATTERN.test(colors[field]),
  );
  const invalid = anyColorInvalid || contrastFails;

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
      setLogoError(t("themeSettings.logoTypeInvalid"));
      return;
    }
    if (file.size > MAX_LOGO_BYTES) {
      setLogoError(t("themeSettings.logoTooLarge"));
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
    if (invalid || save.isPending) {
      return;
    }
    const logo: LogoChange = logoFile ?? (removeLogo ? "remove" : "keep");
    const trimmed = ALL_COLOR_FIELDS.reduce((acc, field) => {
      acc[field] = colors[field].trim();
      return acc;
    }, {} as Colors);
    save.mutate({ ...trimmed, fontFamily, logo });
  }

  const previewPrimary = primaryActive ? primary : DEFAULT_PRIMARY;
  const accent = colors.accentColor;

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">{t("themeSettings.title")}</h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("themeSettings.intro")}
      </p>

      <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        {COLOR_GROUPS.map((group) => (
          <React.Fragment key={group.groupKey}>
            <span className={GROUP_LABEL}>{t(group.groupKey)}</span>
            {group.fields.map(({ field, labelKey }) => (
              <ColorField
                key={field}
                label={t(labelKey)}
                value={colors[field]}
                fallback={COLOR_FIELD_DEFAULTS[field]}
                onChange={(next) => setColor(field, next)}
                onClear={() => setColor(field, "")}
                clearLabel={t("themeSettings.reset")}
              />
            ))}
          </React.Fragment>
        ))}

        <span className={GROUP_LABEL}>
          {t("themeSettings.typographyGroup")}
        </span>
        <span className={EYEBROW}>{t("themeSettings.font")}</span>
        <select
          className={`${CONTROL} max-w-[220px]`}
          value={fontFamily}
          onChange={(event) => setFontFamily(event.target.value)}
          aria-label={t("themeSettings.font")}
          style={{ fontFamily: fontFamily || undefined }}
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

        <span className={GROUP_LABEL}>{t("themeSettings.logoGroup")}</span>
        <span className={EYEBROW}>{t("themeSettings.logo")}</span>
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
              {t("themeSettings.logoChoose")}
            </label>
            {(logoFile !== null || showStoredLogo) && (
              <Button variant="ghost" size="sm" onClick={handleLogoRemove}>
                {t("themeSettings.logoRemove")}
              </Button>
            )}
          </div>
          {logoFile && (
            <span className="text-ink-soft truncate text-[12px]">
              {logoFile.name}
            </span>
          )}
          <span className="text-muted text-[11px]">
            {t("themeSettings.logoHint")}
          </span>
        </div>
      </div>

      {anyColorInvalid && (
        <p role="alert" className="text-error mt-3 text-[12px]">
          {t("themeSettings.colorInvalid")}
        </p>
      )}
      {logoError && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {logoError}
        </p>
      )}
      {contrastFails && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("themeSettings.contrastFail", {
            ratio: primaryContrast?.toFixed(1),
          })}
        </p>
      )}

      <div className="border-line mt-5 border-t pt-5">
        <span className={EYEBROW}>{t("themeSettings.preview")}</span>
        <div className="mt-2.5 flex items-center gap-3">
          <span
            className="rounded-yivi font-display inline-flex h-9 items-center px-3.5 text-[13.5px] font-semibold"
            style={{
              backgroundColor: previewPrimary,
              color: readableForeground(previewPrimary),
            }}
          >
            {t("themeSettings.previewButton")}
          </span>
          {HEX_PATTERN.test(accent) && (
            <span
              className="rounded-yivi inline-block h-9 w-9"
              style={{ backgroundColor: accent }}
              aria-hidden="true"
            />
          )}
          {logoPreviewSrc && (
            <img
              src={logoPreviewSrc}
              alt={t("themeSettings.logoPreviewAlt")}
              className="max-h-9 max-w-[160px]"
            />
          )}
        </div>
      </div>

      <div className="mt-5">
        <Button onClick={handleSave} disabled={invalid || save.isPending}>
          {save.isPending ? t("common.saving") : t("common.save")}
        </Button>
      </div>
      {save.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {t("themeSettings.saveError", { message: save.error.message })}
        </p>
      )}
    </Card>
  );
}
