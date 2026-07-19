import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { OrgTheme } from "../api/theme";
import {
  useOrgThemeQuery,
  useUpdateOrgThemeMutation,
} from "../api/theme.queries";
import {
  AA_CONTRAST,
  DEFAULT_ACCENT,
  DEFAULT_PRIMARY,
  primaryContrastFloor,
  readableForeground,
} from "../lib/theme";
import { Button, Card } from "../ui";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

const HEX_PATTERN = /^#[0-9a-fA-F]{6}$/;
// Keep the logo constraint in step with the backend isSafeLogoURI allowlist.
const LOGO_PATTERN = /^(https?:\/\/|data:image\/)/;

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

  const [primary, setPrimary] = useState(settings.primaryColor);
  const [accent, setAccent] = useState(settings.accentColor);
  const [logo, setLogo] = useState(settings.logoUri);

  const primaryActive = HEX_PATTERN.test(primary);
  const primaryContrast = primaryActive
    ? (primaryContrastFloor(primary) ?? 0)
    : null;
  const contrastFails =
    primaryContrast !== null && primaryContrast < AA_CONTRAST;

  const primaryInvalid = primary !== "" && !primaryActive;
  const accentInvalid = accent !== "" && !HEX_PATTERN.test(accent);
  const logoInvalid = logo !== "" && !LOGO_PATTERN.test(logo);
  const invalid =
    primaryInvalid || accentInvalid || logoInvalid || contrastFails;

  function handleSave(): void {
    if (invalid || save.isPending) {
      return;
    }
    save.mutate({
      primaryColor: primary.trim(),
      accentColor: accent.trim(),
      logoUri: logo.trim(),
    });
  }

  const previewPrimary = primaryActive ? primary : DEFAULT_PRIMARY;

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">{t("themeSettings.title")}</h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("themeSettings.intro")}
      </p>

      <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        <ColorField
          label={t("themeSettings.primaryColor")}
          value={primary}
          fallback={DEFAULT_PRIMARY}
          onChange={setPrimary}
          onClear={() => setPrimary("")}
          clearLabel={t("themeSettings.reset")}
        />
        <ColorField
          label={t("themeSettings.accentColor")}
          value={accent}
          fallback={DEFAULT_ACCENT}
          onChange={setAccent}
          onClear={() => setAccent("")}
          clearLabel={t("themeSettings.reset")}
        />
        <span className={EYEBROW}>{t("themeSettings.logoUri")}</span>
        <input
          className={CONTROL}
          value={logo}
          onChange={(event) => setLogo(event.target.value)}
          placeholder={t("themeSettings.logoUriPlaceholder")}
          aria-label={t("themeSettings.logoUri")}
        />
      </div>

      {(primaryInvalid || accentInvalid) && (
        <p role="alert" className="text-error mt-3 text-[12px]">
          {t("themeSettings.colorInvalid")}
        </p>
      )}
      {logoInvalid && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("themeSettings.logoInvalid")}
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
          {logo !== "" && LOGO_PATTERN.test(logo) && (
            <img
              src={logo}
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
