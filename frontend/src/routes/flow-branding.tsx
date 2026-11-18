import { useId, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import { useOrgThemeQuery } from "../api/theme.queries";
import {
  useProofingFlowBrandingQuery,
  useProofingFlowsQuery,
  useProofingSettingsQuery,
  useSaveFlowBrandingMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingBranding,
  ProofingFlow,
  ProofingFlowBranding,
  ProofingSettings,
} from "../api/identity-proofing";
import { absoluteApiUrl } from "../api/http";
import type { OrgTheme } from "../api/theme";
import {
  dropsOwnBranding,
  effectiveBranding,
  isHexColor,
  isInvalidColor,
  proofingErrorMessage,
} from "../lib/identity-proofing";
import { blockingError } from "../lib/query-state";
import { useLogoPreview } from "../lib/use-logo-preview";
import { Button, Card, ConfirmDialog } from "../ui";
import {
  BrandingFields,
  ERROR,
  FALLBACK_PREVIEW_COLOR,
  HINT,
} from "./proofing-branding-fields";
import type { BrandingDraft } from "./proofing-branding-fields";
import { BrandingPreviews } from "./branding-preview";

const SWATCH = "h-3.5 w-3.5 shrink-0 rounded-sm";

type Mode = "org" | "custom";

// Where the logo the branding fields show comes from: the flow's own, else
// the org's it inherits, else none.
function logoSource(
  ownUri: string,
  orgUri: string,
): "own" | "inherited" | "none" {
  if (ownUri) {
    return "own";
  }
  return orgUri ? "inherited" : "none";
}

function modeOf(saved: ProofingFlowBranding): Mode {
  return saved.custom ? "custom" : "org";
}

function draftFrom(saved: ProofingFlowBranding): BrandingDraft {
  return {
    displayName: saved.displayName,
    primaryColor: saved.primaryColor,
    supportContact: saved.supportContact,
    privacyUrl: saved.privacyUrl,
    logo: null,
    removeLogo: false,
  };
}

// A flow's Branding tab: the org's branding, or its own over the org's, and
// a preview of the proofing mail it gives.
export function FlowBrandingSettings({
  slug,
  flowId,
}: Readonly<{
  slug: string;
  flowId: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const own = useProofingFlowBrandingQuery(slug, flowId);
  const settings = useProofingSettingsQuery(slug);
  const org = useOrganizationQuery(slug);
  const theme = useOrgThemeQuery(slug);
  const flows = useProofingFlowsQuery(slug);

  const error = blockingError(own, settings, org, theme);
  if (error !== null) {
    return (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(error, t)}
      </p>
    );
  }
  if (
    own.data === undefined ||
    settings.data === undefined ||
    org.data === undefined ||
    theme.data === undefined
  ) {
    return <p className={HINT}>{t("common.loading")}</p>;
  }
  return (
    <BrandingForm
      slug={slug}
      flowId={flowId}
      orgName={org.data.name}
      orgBranding={{
        ...settings.data.branding,
        logoUri: absoluteLogo(settings.data.branding.logoUri),
      }}
      orgTheme={theme.data}
      saved={own.data}
      settings={settings.data}
      flow={flows.data?.find((flow) => flow.id === flowId)}
    />
  );
}

function absoluteLogo(uri: string): string {
  return uri ? absoluteApiUrl(uri) : "";
}

function BrandingForm({
  slug,
  flowId,
  orgName,
  orgBranding,
  orgTheme,
  saved,
  settings,
  flow,
}: Readonly<{
  slug: string;
  flowId: string;
  orgName: string;
  settings: ProofingSettings;
  flow: ProofingFlow | undefined;
  // The org's branding, its logo address already absolute.
  orgBranding: ProofingBranding;
  // The org's theme, the Idem app's look without a proofing look of its own.
  orgTheme: OrgTheme;
  saved: ProofingFlowBranding;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveFlowBrandingMutation(slug, flowId);
  const [mode, setMode] = useState<Mode>(modeOf(saved));
  const [draft, setDraft] = useState<BrandingDraft>(() => draftFrom(saved));
  // Going back to the organisation's deletes the flow's own fields and logo,
  // so that save waits for a confirmation.
  const [confirmDrop, setConfirmDrop] = useState(false);
  const logoPreview = useLogoPreview(draft.logo);
  const custom = mode === "custom";

  const savedDraft = draftFrom(saved);
  const fieldsDirty =
    draft.logo !== null ||
    draft.removeLogo ||
    draft.displayName !== savedDraft.displayName ||
    draft.primaryColor !== savedDraft.primaryColor ||
    draft.supportContact !== savedDraft.supportContact ||
    draft.privacyUrl !== savedDraft.privacyUrl;
  const dirty = mode !== modeOf(saved) || (custom && fieldsDirty);
  const colorInvalid = custom && isInvalidColor(draft.primaryColor);
  const ownLogoUri =
    logoPreview ?? (draft.removeLogo ? "" : absoluteLogo(saved.logoUri));
  const orgLabel = orgBranding.displayName || orgName;
  const shown = effectiveBranding(orgBranding, {
    custom,
    displayName: draft.displayName.trim(),
    primaryColor: isHexColor(draft.primaryColor) ? draft.primaryColor : "",
    supportContact: draft.supportContact.trim(),
    privacyUrl: draft.privacyUrl.trim(),
    logoUri: ownLogoUri,
  });
  const name = shown.displayName || orgName;
  const change = (patch: Partial<BrandingDraft>): void =>
    setDraft((d) => ({ ...d, ...patch }));

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    if (colorInvalid) {
      return;
    }
    if (dropsOwnBranding(saved, custom)) {
      setConfirmDrop(true);
      return;
    }
    persist();
  }

  function persist(): void {
    save.mutate(
      {
        custom,
        displayName: draft.displayName,
        primaryColor: draft.primaryColor,
        supportContact: draft.supportContact,
        privacyUrl: draft.privacyUrl,
        logo: draft.logo ?? undefined,
        removeLogo: draft.removeLogo,
      },
      {
        onSuccess: (next) => {
          setMode(modeOf(next));
          setDraft(draftFrom(next));
          setConfirmDrop(false);
        },
      },
    );
  }

  return (
    <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
      <Card>
        <form onSubmit={submit} noValidate>
          <div className="border-line border-b px-6 py-4">
            <h2 className="font-display text-[17px] font-bold">
              {t("identityProofingFlows.branding.title")}
            </h2>
            <p className={`${HINT} mt-1`}>
              {t("identityProofingFlows.branding.hint")}
            </p>
          </div>
          <fieldset
            className={`flex flex-col gap-2.5 px-6 pt-5 ${custom ? "" : "pb-5"}`}
          >
            <legend className="sr-only">
              {t("identityProofingFlows.branding.which")}
            </legend>
            <ModeOption
              mode="org"
              selected={!custom}
              onSelect={setMode}
              title={t("identityProofingFlows.branding.same.title")}
              footer={
                <Link
                  to={`/${slug}/settings?tab=branding`}
                  className="text-link underline"
                >
                  {t("identityProofingFlows.branding.same.change")}
                </Link>
              }
            >
              <span className="text-ink-soft flex flex-wrap items-center gap-2 text-[13px]">
                {orgBranding.logoUri ? (
                  <img
                    src={orgBranding.logoUri}
                    alt=""
                    className="h-4 w-4 shrink-0 object-contain"
                  />
                ) : (
                  isHexColor(orgBranding.primaryColor) && (
                    <span
                      aria-hidden="true"
                      className={SWATCH}
                      style={{ backgroundColor: orgBranding.primaryColor }}
                    />
                  )
                )}
                {orgLabel}
                {!orgBranding.logoUri && (
                  <>
                    {" · "}
                    {t("identityProofingFlows.branding.same.noLogo")}
                  </>
                )}
              </span>
            </ModeOption>
            <ModeOption
              mode="custom"
              selected={custom}
              onSelect={setMode}
              title={t("identityProofingFlows.branding.custom.title")}
            >
              <span className="text-ink-soft text-[13px]">
                {t("identityProofingFlows.branding.custom.hint")}
              </span>
            </ModeOption>
          </fieldset>
          {custom && (
            <BrandingFields
              idPrefix="flow-branding"
              draft={draft}
              onChange={change}
              logo={{
                uri: ownLogoUri || orgBranding.logoUri,
                source: logoSource(ownLogoUri, orgBranding.logoUri),
              }}
              fallbackName={orgLabel}
              fallbackColor={
                isHexColor(orgBranding.primaryColor)
                  ? orgBranding.primaryColor
                  : FALLBACK_PREVIEW_COLOR
              }
              placeholders={{
                displayName: orgLabel,
                color:
                  orgBranding.primaryColor ||
                  t("identityProofing.branding.orgColor"),
                support:
                  orgBranding.supportContact ||
                  t("identityProofingFlows.branding.orgEmpty"),
                privacy:
                  orgBranding.privacyUrl ||
                  t("identityProofingFlows.branding.orgEmpty"),
              }}
            />
          )}
          {save.isError && (
            <p role="alert" className={`${ERROR} px-6 pb-5`}>
              {proofingErrorMessage(save.error, t)}
            </p>
          )}
          <div className="border-line flex justify-end gap-2 border-t px-6 py-4">
            <Button
              type="button"
              variant="secondary"
              disabled={!dirty || save.isPending}
              onClick={() => {
                setMode(modeOf(saved));
                setDraft(savedDraft);
              }}
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
        branding={{ ...shown, logoUri: shown.logoUri || orgTheme.logoUri }}
        theme={orgTheme}
        flow={flow}
        dataRetentionDays={settings.dataRetentionDays}
        sessionTtlSeconds={settings.sessionTtlSeconds}
        mail={{
          flowId,
          custom,
          displayName: draft.displayName,
          primaryColor: isHexColor(draft.primaryColor)
            ? draft.primaryColor
            : "",
          supportContact: draft.supportContact,
          privacyUrl: draft.privacyUrl,
          logo: draft.logo ?? undefined,
          removeLogo: draft.removeLogo,
        }}
      />
      {confirmDrop && (
        <ConfirmDialog
          title={t("identityProofingFlows.branding.dropConfirm.title")}
          message={t("identityProofingFlows.branding.dropConfirm.message")}
          confirmLabel={t("identityProofingFlows.branding.dropConfirm.confirm")}
          busy={save.isPending}
          error={save.isError ? proofingErrorMessage(save.error, t) : undefined}
          onClose={() => setConfirmDrop(false)}
          onConfirm={persist}
        />
      )}
    </div>
  );
}

// One of the two brandings as a card with a radio: what it is, and under it
// what it shows. The radio is named by the title and described by what it
// shows; footer sits outside the label so a link in it is not part of the name.
function ModeOption({
  mode,
  selected,
  onSelect,
  title,
  children,
  footer,
}: Readonly<{
  mode: Mode;
  selected: boolean;
  onSelect: (mode: Mode) => void;
  title: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
}>): React.JSX.Element {
  const titleId = useId();
  const shownId = useId();
  return (
    <div
      className={`rounded-yivi has-[input:focus-visible]:ring-ink/10 border has-[input:focus-visible]:ring-3 ${
        selected ? "border-ink bg-surface-2" : "border-line-strong bg-surface"
      }`}
    >
      <label className="flex cursor-pointer items-start gap-3 px-4 py-3.5">
        <input
          type="radio"
          name="flow-branding-mode"
          className="accent-ink mt-0.5 h-4 w-4 flex-none"
          aria-labelledby={titleId}
          aria-describedby={shownId}
          checked={selected}
          onChange={() => onSelect(mode)}
        />
        <span className="flex min-w-0 flex-col gap-1">
          <span id={titleId} className="text-[14px] font-semibold">
            {title}
          </span>
          <span id={shownId}>{children}</span>
        </span>
      </label>
      {footer && (
        <p className="-mt-1.5 pr-4 pb-3.5 pl-11 text-[13px]">{footer}</p>
      )}
    </div>
  );
}
