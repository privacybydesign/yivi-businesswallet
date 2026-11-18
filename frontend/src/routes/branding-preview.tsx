import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import { useBrandingMailPreviewQuery } from "../api/identity-proofing.queries";
import type {
  BrandingMailPreviewInput,
  HostedProofing,
  HostedTheme,
  ProofingBranding,
  ProofingFlow,
} from "../api/identity-proofing";
import type { OrgTheme } from "../api/theme";
import {
  HOSTED_PREVIEW_PAGE,
  HOSTED_PREVIEW_PATH,
  HOSTED_PREVIEW_READY,
  hostedPreviewPage,
  hostedPreviewTheme,
} from "../lib/hosted-preview";
import { idemLook, isIdemBranded } from "../lib/idem-look";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { useDebouncedValue } from "../lib/use-debounced-value";
import { Icon } from "../ui";
import type { IconName } from "../ui/icon";
import { CAPTION, ERROR, HINT } from "./proofing-branding-fields";

type PreviewKind = "mail" | "app" | "hosted";

const PREVIEW_KINDS: readonly PreviewKind[] = ["mail", "app", "hosted"];

const SECONDS_PER_MINUTE = 60;

// The mail is fetched once typing pauses.
const MAIL_PREVIEW_DEBOUNCE_MS = 400;
const FRAME_HEIGHT = "h-[720px]";

// The preview beside a branding form: what an external person gets in the
// proofing mail, the Idem app or on the hosted page.
export function BrandingPreview({
  mail,
  app,
  hosted,
}: Readonly<{
  mail: React.ReactNode;
  app: React.ReactNode;
  hosted: React.ReactNode;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const [kind, setKind] = useState<PreviewKind>("mail");
  const headingId = useId();
  const shown = { mail, app, hosted }[kind];
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 id={headingId} className={CAPTION}>
          {t("identityProofingFlows.branding.preview.caption")}
        </h3>
        <fieldset
          aria-labelledby={headingId}
          className="bg-surface-3 rounded-yivi inline-flex gap-1 p-[3px]"
        >
          {PREVIEW_KINDS.map((option) => (
            <button
              key={option}
              type="button"
              aria-pressed={kind === option}
              onClick={() => setKind(option)}
              className={[
                "text-ink flex h-8 cursor-pointer items-center rounded-md px-3 text-[13px] font-semibold transition-opacity",
                kind === option
                  ? "bg-surface shadow-sm"
                  : "opacity-55 hover:opacity-100",
              ].join(" ")}
            >
              {t(`identityProofingFlows.branding.preview.${option}`)}
            </button>
          ))}
        </fieldset>
      </div>
      {shown}
    </section>
  );
}

// The three previews of a branding form, each the real thing or as the Idem
// app draws it: branding is the draft over what is saved, each look field
// left empty the wallet theme's.
export function BrandingPreviews({
  slug,
  orgName,
  name,
  branding,
  theme,
  flow,
  dataRetentionDays,
  sessionTtlSeconds,
  mail,
}: Readonly<{
  slug: string;
  orgName: string;
  // The name the branding signs with.
  name: string;
  branding: ProofingBranding;
  theme: OrgTheme;
  // The flow the page and app show; none asks for nothing in particular.
  flow: ProofingFlow | undefined;
  dataRetentionDays: number;
  sessionTtlSeconds: number;
  mail: Omit<BrandingMailPreviewInput, "locale">;
}>): React.JSX.Element {
  const { i18n } = useTranslation();
  const [now] = useState(() => Date.now());
  const page = hostedPreviewPage({
    name,
    branding,
    theme,
    flow,
    dataRetentionDays,
    language: i18n.language,
    now,
  });
  return (
    <BrandingPreview
      mail={<MailPreviewFrame slug={slug} input={mail} />}
      app={
        <AppPreviewCard
          name={name}
          orgName={orgName}
          theme={hostedPreviewTheme(branding, theme)}
          logoUri={branding.logoUri}
          requestedAttributes={flow?.requestedAttributes ?? []}
          ageOver={flow?.ageOver}
          minutes={Math.round(
            (flow?.sessionTtlSeconds ?? sessionTtlSeconds) / SECONDS_PER_MINUTE,
          )}
        />
      }
      hosted={<HostedPreviewFrame page={page} />}
    />
  );
}

// The proofing mail as the backend renders it for the form's draft, in a
// sandboxed frame: the mail paints its own page.
export function MailPreviewFrame({
  slug,
  input,
}: Readonly<{
  slug: string;
  input: Omit<BrandingMailPreviewInput, "locale">;
}>): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const settled = useDebouncedValue(input, MAIL_PREVIEW_DEBOUNCE_MS);
  const preview = useBrandingMailPreviewQuery(slug, {
    ...settled,
    locale: i18n.language,
  });
  if (preview.data === undefined) {
    return preview.isError ? (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(preview.error, t)}
      </p>
    ) : (
      <p className={HINT}>{t("common.loading")}</p>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <p className="text-ink text-[13px]">
        <span className="text-ink-soft">
          {t("mailTemplates.previewSubject")}
        </span>{" "}
        {preview.data.subject}
      </p>
      <iframe
        title={t("identityProofingFlows.branding.preview.mail")}
        sandbox=""
        srcDoc={preview.data.html}
        className={`border-line rounded-yivi w-full border ${FRAME_HEIGHT}`}
      />
    </div>
  );
}

// The hosted page itself, in a frame of the wallet's own origin that takes
// its look from page alone (routes/hosted-preview.tsx).
export function HostedPreviewFrame({
  page,
}: Readonly<{ page: HostedProofing }>): React.JSX.Element {
  const { t } = useTranslation();
  const frame = useRef<HTMLIFrameElement>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    function receive(event: MessageEvent): void {
      if (
        event.origin === window.location.origin &&
        event.source === frame.current?.contentWindow &&
        (event.data as { type?: unknown } | null)?.type === HOSTED_PREVIEW_READY
      ) {
        setReady(true);
      }
    }
    window.addEventListener("message", receive);
    return () => window.removeEventListener("message", receive);
  }, []);

  useEffect(() => {
    if (ready) {
      frame.current?.contentWindow?.postMessage(
        { type: HOSTED_PREVIEW_PAGE, page },
        window.location.origin,
      );
    }
  }, [ready, page]);

  return (
    <iframe
      ref={frame}
      title={t("identityProofingFlows.branding.preview.hosted")}
      src={HOSTED_PREVIEW_PATH}
      className={`border-line rounded-yivi w-full border ${FRAME_HEIGHT}`}
    />
  );
}

// What the Idem app lists for each requested item (proofing_session_consent_screen.dart).
type IdemItemKey =
  | "documentIdentity"
  | "additionalDetails"
  | "facePhoto"
  | "chipChecks"
  | "biometrics"
  | "ageCheck";

const IDEM_ITEMS: Partial<
  Record<string, { icon: IconName; key: IdemItemKey }>
> = {
  dg1: { icon: "personal", key: "documentIdentity" },
  dg11: { icon: "personal", key: "additionalDetails" },
  dg2: { icon: "view", key: "facePhoto" },
  face_image: { icon: "view", key: "facePhoto" },
  chip_checks: { icon: "lock", key: "chipChecks" },
  biometrics: { icon: "view", key: "biometrics" },
  age_over: { icon: "birthdate", key: "ageCheck" },
};

// The Idem app's first screen for the request, its consent screen, in the
// look the app derives from the session's branding (lib/idem-look.ts).
export function AppPreviewCard({
  name,
  orgName,
  theme,
  logoUri,
  requestedAttributes,
  ageOver,
  minutes,
}: Readonly<{
  // The name the branding signs with, and the org's own.
  name: string;
  orgName: string;
  theme: HostedTheme;
  logoUri: string;
  requestedAttributes: readonly string[];
  ageOver?: number;
  // How long the session runs.
  minutes: number;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const branded = isIdemBranded(theme, logoUri);
  const look = idemLook(theme, branded);
  // Without branding the app names the org itself, by its initial.
  const party = branded ? name : orgName;
  const items = idemItems(requestedAttributes, ageOver, t);
  const heading = { fontFamily: look.headingFont };
  return (
    <div className="flex flex-col gap-2">
      <div
        className="border-line rounded-yivi flex w-full flex-col overflow-hidden border bg-white"
        style={{ fontFamily: look.bodyFont, color: look.ink }}
      >
        <div className="grid grid-cols-[44px_1fr_44px] items-center px-4 pt-4">
          <span className="flex h-11 w-11 items-center justify-center rounded-full bg-[#f5f5f6]">
            <Icon name="close" size={20} />
          </span>
          <span
            className="text-center text-[16px] font-semibold"
            style={heading}
          >
            {t("identityProofingFlows.branding.preview.idem.title")}
          </span>
        </div>
        <div className="flex flex-col px-5 pt-8 pb-5">
          <span
            className="flex h-[88px] w-[88px] items-center justify-center self-center rounded-3xl border"
            style={{
              backgroundColor: `${look.tileFill}80`,
              borderColor: look.tileBorder,
            }}
          >
            {!branded ? (
              <span
                className="text-[36px] font-bold"
                style={{ ...heading, color: look.primary }}
              >
                {party.charAt(0).toUpperCase() || "?"}
              </span>
            ) : logoUri ? (
              <img src={logoUri} alt="" className="h-14 w-14 object-contain" />
            ) : (
              <span
                className="max-w-[80px] truncate text-[28px] font-bold"
                style={heading}
              >
                {name}
              </span>
            )}
          </span>
          <h4
            className="mt-3.5 text-center text-[24px] leading-[1.25] font-bold"
            style={heading}
          >
            {t("identityProofingFlows.branding.preview.idem.heading", {
              party,
            })}
          </h4>
          {party !== orgName && (
            <p
              className="mt-2 text-center text-[14px]"
              style={{ color: look.bodyText }}
            >
              {t("identityProofingFlows.branding.preview.idem.requestedBy", {
                org: orgName,
              })}
            </p>
          )}
          <p
            className="mt-3.5 text-center text-[14px] font-bold"
            style={{ color: look.bodyText }}
          >
            {t("identityProofingFlows.branding.preview.idem.expiresIn", {
              count: minutes,
            })}
          </p>
          <p
            className="mt-7 text-[16px] font-semibold"
            style={{ ...heading, color: look.slate }}
          >
            {t("identityProofingFlows.branding.preview.idem.theyWillReceive")}
          </p>
          <ul
            className="mt-2.5 rounded-2xl border"
            style={{ borderColor: look.subtleBorder }}
          >
            {items.map((item, i) => (
              <li
                key={item.label}
                className="flex items-center gap-3 px-4 py-3.5 text-[15px] font-semibold"
                style={
                  i === 0
                    ? undefined
                    : { borderTop: `1px solid ${look.subtleBorder}` }
                }
              >
                <span style={{ color: look.primary }} className="flex">
                  <Icon name={item.icon} size={20} />
                </span>
                {item.label}
              </li>
            ))}
          </ul>
          <p className="mt-3.5 text-[14px]" style={{ color: look.bodyText }}>
            {t("identityProofingFlows.branding.preview.idem.stepByStep", {
              party,
            })}
          </p>
        </div>
        <div className="flex flex-col gap-3 px-5 pb-7">
          <span
            className="flex h-[52px] items-center justify-center text-[16px] font-bold"
            style={{
              backgroundColor: look.primary,
              color: look.onPrimary,
              borderRadius: look.buttonRadius,
            }}
          >
            {t("identityProofingFlows.branding.preview.idem.continue")}
          </span>
          <span
            className="flex h-[52px] items-center justify-center border-2 bg-white text-[16px] font-bold"
            style={{
              borderColor: look.ink,
              color: look.ink,
              borderRadius: look.buttonRadius,
            }}
          >
            {t("identityProofingFlows.branding.preview.idem.decline")}
          </span>
        </div>
      </div>
      <p className={HINT}>
        {t("identityProofingFlows.branding.preview.appNote")}
      </p>
    </div>
  );
}

// The rows the consent screen lists: one per label, an unknown item by its
// key, and one catch-all row for none.
function idemItems(
  requested: readonly string[],
  ageOver: number | undefined,
  t: TFunction,
): { icon: IconName; label: string }[] {
  const prefix = "identityProofingFlows.branding.preview.idem.items";
  if (requested.length === 0) {
    return [{ icon: "menu", label: t(`${prefix}.everything`) }];
  }
  const rows: { icon: IconName; label: string }[] = [];
  for (const key of requested) {
    const item = IDEM_ITEMS[key];
    let label = key;
    if (item) {
      label =
        key === "age_over" && ageOver !== undefined
          ? t(`${prefix}.ageOver`, { age: ageOver })
          : t(`${prefix}.${item.key}`);
    }
    if (!rows.some((row) => row.label === label)) {
      rows.push({ icon: item?.icon ?? "valid", label });
    }
  }
  return rows;
}
