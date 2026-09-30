import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useSaveProofingBrandingMutation } from "../api/identity-proofing.queries";
import type { ProofingCustomer } from "../api/identity-proofing";
import {
  isHexColor,
  proofingErrorMessage,
  readableTextOn,
} from "../lib/identity-proofing";
import { absoluteApiUrl } from "../api/http";
import { Button, Card, Icon, Input } from "../ui";
import { InitialMark } from "./proofing-customer-ui";

const LABEL = "text-ink text-[13px] font-semibold";
const HINT = "text-muted text-[12px]";
const ERROR = "text-error text-[12.5px]";
const CAPTION =
  "text-muted font-mono text-[10.5px] font-medium tracking-[0.08em] uppercase";
// A few starting points; any #rrggbb works.
const PRESET_COLORS = ["#1F5B4A", "#26307A", "#8B2332", "#1A1A1A"] as const;
// Shown when the customer has no colour: the Yivi default the mail falls back to.
const FALLBACK_PREVIEW_COLOR = "#1A1A1A";
const LOGO_ACCEPT = "image/png,image/jpeg,image/gif,image/webp";
const PREVIEW_QR_ICON = 40;
const PREVIEW_MARK = "h-6 w-6 rounded text-[11px]";

interface Draft {
  displayName: string;
  primaryColor: string;
  supportContact: string;
  privacyUrl: string;
  hidePoweredBy: boolean;
  logo: File | null;
  removeLogo: boolean;
}

function draftFrom(customer: ProofingCustomer): Draft {
  const b = customer.branding;
  return {
    displayName: b.displayName,
    primaryColor: b.primaryColor,
    supportContact: b.supportContact,
    privacyUrl: b.privacyUrl,
    hidePoweredBy: b.hidePoweredBy,
    logo: null,
    removeLogo: false,
  };
}

// The look of the proofing mail a customer's subjects get: the name it signs
// with, its logo and colour, and its support contact and privacy statement,
// with a live preview beside the form.
export function BrandingTab({
  slug,
  customer,
}: {
  slug: string;
  customer: ProofingCustomer;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveProofingBrandingMutation(slug, customer.id);
  const [draft, setDraft] = useState<Draft>(() => draftFrom(customer));
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

  const saved = draftFrom(customer);
  const dirty =
    draft.logo !== null ||
    draft.removeLogo ||
    draft.displayName !== saved.displayName ||
    draft.primaryColor !== saved.primaryColor ||
    draft.supportContact !== saved.supportContact ||
    draft.privacyUrl !== saved.privacyUrl ||
    draft.hidePoweredBy !== saved.hidePoweredBy;
  const colorInvalid =
    draft.primaryColor !== "" && !isHexColor(draft.primaryColor);
  const logoUri =
    logoPreview ??
    (draft.removeLogo || !customer.branding.logoUri
      ? ""
      : absoluteApiUrl(customer.branding.logoUri));
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
    <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
      <Card>
        <form onSubmit={submit} noValidate>
          <div className="border-line border-b px-6 py-4">
            <h2 className="font-display text-[17px] font-bold">
              {t("customers.branding.title")}
            </h2>
            <p className={`${HINT} mt-1`}>{t("customers.branding.hint")}</p>
          </div>
          <div className="flex flex-col gap-5 px-6 py-5">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="branding-display-name" className={LABEL}>
                {t("customers.branding.displayName")}
              </label>
              <Input
                id="branding-display-name"
                value={draft.displayName}
                placeholder={customer.name}
                onChange={(e) => set("displayName", e.target.value)}
              />
              <p className={HINT}>{t("customers.branding.displayNameHint")}</p>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className={LABEL}>{t("customers.branding.logo")}</span>
              <div className="border-line-strong flex items-center gap-3 rounded-lg border border-dashed p-3">
                {logoUri ? (
                  <img
                    src={logoUri}
                    alt=""
                    className="bg-surface-3 h-11 w-11 shrink-0 rounded-md object-contain"
                  />
                ) : (
                  <InitialMark
                    name={draft.displayName.trim() || customer.name}
                    color={
                      isHexColor(draft.primaryColor)
                        ? draft.primaryColor
                        : FALLBACK_PREVIEW_COLOR
                    }
                    className="h-11 w-11 rounded-md text-[17px]"
                  />
                )}
                <div className="min-w-0 flex-1">
                  <div className="text-ink truncate text-[13px] font-semibold">
                    {draft.logo?.name ??
                      (logoUri
                        ? t("customers.branding.logoSet")
                        : t("customers.branding.noLogo"))}
                  </div>
                  <div className={HINT}>{t("customers.branding.logoHint")}</div>
                </div>
                <input
                  ref={fileInput}
                  type="file"
                  accept={LOGO_ACCEPT}
                  className="sr-only"
                  aria-label={t("customers.branding.logo")}
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
                    ? t("customers.branding.replace")
                    : t("customers.branding.upload")}
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
                    {t("customers.branding.removeLogo")}
                  </Button>
                )}
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <span id="branding-color-label" className={LABEL}>
                {t("customers.branding.primaryColor")}
              </span>
              <div
                role="group"
                aria-labelledby="branding-color-label"
                className="flex flex-wrap items-center gap-2"
              >
                {PRESET_COLORS.map((color) => {
                  const active =
                    draft.primaryColor.toLowerCase() === color.toLowerCase();
                  return (
                    <button
                      key={color}
                      type="button"
                      aria-pressed={active}
                      aria-label={color}
                      onClick={() => set("primaryColor", color)}
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
                <input
                  type="color"
                  aria-label={t("customers.branding.customColor")}
                  value={
                    isHexColor(draft.primaryColor)
                      ? draft.primaryColor
                      : FALLBACK_PREVIEW_COLOR
                  }
                  onChange={(e) => set("primaryColor", e.target.value)}
                  className="border-line-strong h-9 w-9 cursor-pointer rounded-md border bg-transparent"
                />
                <div className="ml-2 w-32">
                  <Input
                    value={draft.primaryColor}
                    placeholder={t("customers.branding.orgColor")}
                    aria-label={t("customers.branding.primaryColor")}
                    aria-invalid={colorInvalid}
                    className="font-mono text-[12.5px]"
                    onChange={(e) => set("primaryColor", e.target.value.trim())}
                  />
                </div>
              </div>
              {colorInvalid ? (
                <p className={ERROR}>{t("customers.branding.colorInvalid")}</p>
              ) : (
                <p className={HINT}>
                  {t("customers.branding.primaryColorHint")}
                </p>
              )}
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <div className="flex flex-col gap-1.5">
                <label htmlFor="branding-support" className={LABEL}>
                  {t("customers.branding.supportContact")}
                </label>
                <Input
                  id="branding-support"
                  value={draft.supportContact}
                  placeholder={t("customers.branding.supportPlaceholder")}
                  onChange={(e) => set("supportContact", e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <label htmlFor="branding-privacy" className={LABEL}>
                  {t("customers.branding.privacyUrl")}
                </label>
                <Input
                  id="branding-privacy"
                  type="url"
                  value={draft.privacyUrl}
                  placeholder={t("customers.branding.privacyPlaceholder")}
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
                {t("customers.branding.hidePoweredBy")}
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
              onClick={() => setDraft(draftFrom(customer))}
            >
              {t("customers.branding.discard")}
            </Button>
            <Button
              type="submit"
              loading={save.isPending}
              disabled={!dirty || colorInvalid}
            >
              {t("customers.branding.save")}
            </Button>
          </div>
        </form>
      </Card>
      <MailPreview
        name={draft.displayName.trim() || customer.name}
        color={isHexColor(draft.primaryColor) ? draft.primaryColor : ""}
        logoUri={logoUri}
        supportContact={draft.supportContact.trim()}
        privacyUrl={draft.privacyUrl.trim()}
      />
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
        {t("customers.branding.preview.caption")}
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
            {t("customers.branding.preview.heading", { name })}
          </h3>
          <p className="text-ink-soft mt-2 text-[12.5px]">
            {t("customers.branding.preview.body", { name })}
          </p>
          <div className="border-line text-muted mx-auto my-4 flex h-24 w-24 items-center justify-center rounded-md border">
            <Icon name="scan_qrcode" size={PREVIEW_QR_ICON} />
          </div>
          <div
            className="rounded-md py-2.5 text-center text-[13px] font-semibold"
            style={{ backgroundColor: fill, color: onFill }}
          >
            {t("customers.branding.preview.button")}
          </div>
          {supportContact && (
            <p className="text-ink-soft mt-3 text-[12px]">
              {t("customers.branding.preview.support", {
                contact: supportContact,
              })}
            </p>
          )}
          {privacyUrl && (
            <p className="text-ink-soft mt-1 text-[12px] break-all">
              {t("customers.branding.preview.privacy", { url: privacyUrl })}
            </p>
          )}
        </div>
        <p className="text-muted mt-3 text-center text-[11px]">
          {t("customers.branding.preview.footer")}
        </p>
      </div>
    </div>
  );
}
