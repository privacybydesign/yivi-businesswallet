import { useId, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useProofingFlowBrandingQuery,
  useProofingSettingsQuery,
  useSaveFlowBrandingMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingBranding,
  ProofingFlowBranding,
} from "../api/identity-proofing";
import { absoluteApiUrl } from "../api/http";
import {
  effectiveBranding,
  isHexColor,
  isInvalidColor,
  proofingErrorMessage,
} from "../lib/identity-proofing";
import { useLogoPreview } from "../lib/use-logo-preview";
import { Button, Card } from "../ui";
import {
  BrandingFields,
  ERROR,
  FALLBACK_PREVIEW_COLOR,
  HINT,
  MailPreview,
} from "./proofing-branding-fields";
import type { BrandingDraft } from "./proofing-branding-fields";

const SWATCH = "h-3.5 w-3.5 shrink-0 rounded-sm";

type Mode = "org" | "custom";

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
}: {
  slug: string;
  flowId: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const own = useProofingFlowBrandingQuery(slug, flowId);
  const settings = useProofingSettingsQuery(slug);
  const org = useOrganizationQuery(slug);

  if (own.isError || settings.isError || org.isError) {
    return (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(own.error ?? settings.error ?? org.error, t)}
      </p>
    );
  }
  if (own.isPending || settings.isPending || org.isPending) {
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
      saved={own.data}
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
  saved,
}: {
  slug: string;
  flowId: string;
  orgName: string;
  // The org's branding, its logo address already absolute.
  orgBranding: ProofingBranding;
  saved: ProofingFlowBranding;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveFlowBrandingMutation(slug, flowId);
  const [mode, setMode] = useState<Mode>(modeOf(saved));
  const [draft, setDraft] = useState<BrandingDraft>(() => draftFrom(saved));
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
  const change = (patch: Partial<BrandingDraft>): void =>
    setDraft((d) => ({ ...d, ...patch }));

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    if (colorInvalid) {
      return;
    }
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
                source: ownLogoUri
                  ? "own"
                  : orgBranding.logoUri
                    ? "inherited"
                    : "none",
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
      <MailPreview
        name={shown.displayName || orgName}
        color={isHexColor(shown.primaryColor) ? shown.primaryColor : ""}
        logoUri={shown.logoUri}
        supportContact={shown.supportContact}
        privacyUrl={shown.privacyUrl}
      />
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
}: {
  mode: Mode;
  selected: boolean;
  onSelect: (mode: Mode) => void;
  title: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
}): React.JSX.Element {
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
