import { useEffect, useState } from "react";
import { useBlocker } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useProofingFlowsQuery,
  useProofingSettingsQuery,
  useSaveBrandingMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingBrandingInput,
  ProofingSettings,
} from "../api/identity-proofing";
import {
  useOrgThemeQuery,
  useUpdateOrgThemeMutation,
} from "../api/theme.queries";
import type { OrgTheme } from "../api/theme";
import { isHexColor, proofingErrorMessage } from "../lib/identity-proofing";
import { blockingError } from "../lib/query-state";
import { Button, Card, ConfirmDialog, Input } from "../ui";
import { BrandingPreviews } from "./branding-preview";
import { ERROR, HINT, LABEL } from "./proofing-branding-fields";
import { ThemeFields } from "./theme-settings";
import { useThemeDraft } from "../lib/use-theme-draft";
import type { ThemeDraft } from "../lib/use-theme-draft";

// Settings › Branding: the org's one look, its wallet theme, which the proofing
// mail, the hosted page and the Idem app wear too, and what they say besides
// it. One form, one save, with the preview beside it.
export function BrandingSettings({
  slug,
  orgName,
}: Readonly<{
  slug: string;
  orgName: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const theme = useOrgThemeQuery(slug);
  const settings = useProofingSettingsQuery(slug);

  const error = blockingError(theme, settings);
  if (error !== null) {
    return (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(error, t)}
      </p>
    );
  }
  if (theme.data === undefined || settings.data === undefined) {
    return <p className={HINT}>{t("common.loading")}</p>;
  }
  return (
    <BrandingForm
      // A save refetches the theme, which starts the form over from it.
      key={theme.data.updatedAt ?? "unset"}
      slug={slug}
      orgName={orgName}
      theme={theme.data}
      settings={settings.data}
    />
  );
}

function brandingFrom(settings: ProofingSettings): ProofingBrandingInput {
  const b = settings.branding;
  return {
    displayName: b.displayName,
    supportContact: b.supportContact,
    privacyUrl: b.privacyUrl,
    hidePoweredBy: b.hidePoweredBy,
    showTheme: b.showTheme,
  };
}

function sameBranding(
  a: ProofingBrandingInput,
  b: ProofingBrandingInput,
): boolean {
  return (
    a.displayName === b.displayName &&
    a.supportContact === b.supportContact &&
    a.privacyUrl === b.privacyUrl &&
    a.hidePoweredBy === b.hidePoweredBy &&
    a.showTheme === b.showTheme
  );
}

function hexOrEmpty(value: string): string {
  return isHexColor(value) ? value : "";
}

// The theme as the draft has it, for the preview.
function draftTheme(saved: OrgTheme, draft: ThemeDraft): OrgTheme {
  return {
    ...saved,
    ...Object.fromEntries(
      Object.entries(draft.colors).map(([field, value]) => [
        field,
        hexOrEmpty(value),
      ]),
    ),
    fontFamily: draft.fontFamily,
    logoUri: draft.logoPreviewSrc ?? "",
  };
}

function BrandingForm({
  slug,
  orgName,
  theme,
  settings,
}: Readonly<{
  slug: string;
  orgName: string;
  theme: OrgTheme;
  settings: ProofingSettings;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const flows = useProofingFlowsQuery(slug);
  const saveTheme = useUpdateOrgThemeMutation(slug);
  const saveBranding = useSaveBrandingMutation(slug);
  const look = useThemeDraft(theme);
  const [branding, setBranding] = useState(() => brandingFrom(settings));
  const brandingDirty = !sameBranding(branding, brandingFrom(settings));
  const dirty = look.dirty || brandingDirty;
  const saving = saveTheme.isPending || saveBranding.isPending;
  const saveError = saveTheme.error ?? saveBranding.error;
  const change = (patch: Partial<ProofingBrandingInput>): void =>
    setBranding((b) => ({ ...b, ...patch }));
  const blocker = useBlocker(dirty && !saving);
  useLeaveWarning(dirty);

  async function submit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    if (look.invalid || !dirty) {
      return;
    }
    try {
      if (brandingDirty) {
        await saveBranding.mutateAsync(branding);
      }
      if (look.dirty) {
        await saveTheme.mutateAsync(look.input());
      }
    } catch {
      // The error shows under the form.
    }
  }

  function discard(): void {
    look.reset();
    setBranding(brandingFrom(settings));
  }

  const shownTheme = draftTheme(theme, look);
  const colors = shownTheme;
  return (
    <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
      <Card>
        <form onSubmit={(e) => void submit(e)} noValidate>
          <section className="border-line border-b px-6 py-5">
            <h2 className="font-display text-[17px] font-bold">
              {t("settings.brandingWallet")}
            </h2>
            <p className={`${HINT} mt-1 mb-4`}>
              {t("settings.brandingWalletHint")}
            </p>
            <ThemeFields draft={look} />
          </section>
          <section className="flex flex-col gap-5 px-6 py-5">
            <div>
              <h2 className="font-display text-[17px] font-bold">
                {t("settings.brandingExternal")}
              </h2>
              <p className={`${HINT} mt-1`}>
                {t("settings.brandingExternalHint")}
              </p>
            </div>
            <div className="flex flex-col gap-1.5">
              <label htmlFor="branding-display-name" className={LABEL}>
                {t("identityProofing.branding.displayName")}
              </label>
              <Input
                id="branding-display-name"
                value={branding.displayName}
                placeholder={orgName}
                onChange={(e) => change({ displayName: e.target.value })}
              />
              <p className={HINT}>
                {t("identityProofing.branding.displayNameHint")}
              </p>
            </div>
            <div className="grid gap-4 md:grid-cols-2">
              <div className="flex flex-col gap-1.5">
                <label htmlFor="branding-support" className={LABEL}>
                  {t("identityProofing.branding.supportContact")}
                </label>
                <Input
                  id="branding-support"
                  value={branding.supportContact}
                  placeholder={t(
                    "identityProofing.branding.supportPlaceholder",
                  )}
                  onChange={(e) => change({ supportContact: e.target.value })}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <label htmlFor="branding-privacy" className={LABEL}>
                  {t("identityProofing.branding.privacyUrl")}
                </label>
                <Input
                  id="branding-privacy"
                  type="url"
                  value={branding.privacyUrl}
                  placeholder={t(
                    "identityProofing.branding.privacyPlaceholder",
                  )}
                  onChange={(e) => change({ privacyUrl: e.target.value })}
                />
              </div>
            </div>
            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                className="h-4 w-4"
                checked={branding.hidePoweredBy}
                onChange={(e) => change({ hidePoweredBy: e.target.checked })}
              />
              {t("identityProofing.branding.hidePoweredBy")}
            </label>
            <div className="flex flex-col gap-1">
              <label className="flex items-center gap-2 text-[13px] font-semibold">
                <input
                  type="checkbox"
                  className="h-4 w-4"
                  checked={branding.showTheme}
                  onChange={(e) => change({ showTheme: e.target.checked })}
                />
                {t("identityProofing.branding.showTheme")}
              </label>
              <p className={`${HINT} pl-6`}>
                {t("identityProofing.branding.showThemeHint")}
              </p>
            </div>
          </section>
          {saveError && (
            <p role="alert" className={`${ERROR} px-6 pb-5`}>
              {proofingErrorMessage(saveError, t)}
            </p>
          )}
          <div className="border-line flex justify-end gap-2 border-t px-6 py-4">
            <Button
              type="button"
              variant="secondary"
              disabled={!dirty || saving}
              onClick={discard}
            >
              {t("identityProofing.branding.discard")}
            </Button>
            <Button
              type="submit"
              loading={saving}
              disabled={!dirty || look.invalid}
            >
              {t("identityProofing.branding.save")}
            </Button>
          </div>
        </form>
      </Card>
      <BrandingPreviews
        slug={slug}
        orgName={orgName}
        name={branding.displayName.trim() || orgName}
        branding={{
          ...settings.branding,
          ...branding,
          displayName: branding.displayName.trim(),
          supportContact: branding.supportContact.trim(),
          privacyUrl: branding.privacyUrl.trim(),
          primaryColor: "",
          logoUri: shownTheme.logoUri,
        }}
        theme={shownTheme}
        flow={flows.data?.find((flow) => flow.default)}
        dataRetentionDays={settings.dataRetentionDays}
        sessionTtlSeconds={settings.sessionTtlSeconds}
        mail={{
          displayName: branding.displayName,
          supportContact: branding.supportContact,
          privacyUrl: branding.privacyUrl,
          theme: {
            primaryColor: colors.primaryColor,
            textColor: colors.textColor,
            surfaceColor: colors.surfaceColor,
            borderColor: colors.borderColor,
            linkColor: colors.linkColor,
            fontFamily: look.fontFamily,
            logo: look.logoFile ?? undefined,
            removeLogo: look.removeLogo,
          },
        }}
      />
      {blocker.state === "blocked" && (
        <ConfirmDialog
          title={t("settings.unsaved.title")}
          message={t("settings.unsaved.message")}
          confirmLabel={t("settings.unsaved.leave")}
          onClose={() => blocker.reset()}
          onConfirm={() => blocker.proceed()}
        />
      )}
    </div>
  );
}

// Asks the browser to confirm closing or reloading the page while dirty.
function useLeaveWarning(dirty: boolean): void {
  useEffect(() => {
    if (!dirty) {
      return;
    }
    function warn(event: BeforeUnloadEvent): void {
      event.preventDefault();
    }
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
}
