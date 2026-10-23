import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import type { TFunction } from "i18next";
import * as React from "react";
import {
  useNewClaimLinkMutation,
  useProofingAppQuery,
  useProofingProgressQuery,
  useYiviDisclosureQuery,
  useStartProofingYiviMutation,
  useUploadDiplomasMutation,
  useSubmitAddressMutation,
  useAddressLookupQuery,
  useUploadEmploymentMutation,
} from "../api/identity-proofing.queries";
import type {
  AddressMode,
  RegisterAddress,
  ProofingAddressInput,
  DiplomaMode,
  DiplomaVerdict,
  EmploymentMode,
  EmploymentVerdict,
  ProofingCustomer,
  ProofingEmployment,
  HostedDiploma,
  ProofingApp,
  ProofingMethod,
  ProofingProgress,
  ProofingYiviStart,
  VerifyTarget,
} from "../api/identity-proofing";
import {
  ATTRIBUTE_AGE_OVER,
  ATTRIBUTE_DOCUMENT,
  addressLookup,
  assuranceLevelLabel,
  diplomaRejectionReason,
  diplomaStepDone,
  employmentRejectionReason,
  employmentReview,
  employmentStepDone,
  formatDuration,
  isRequestedAttribute,
  matchingAddress,
  proofingErrorMessage,
  proofingRejectionReason,
  dataRequestRefusal,
  secondsUntil,
  sessionView,
  verifyErrorMessage,
} from "../lib/identity-proofing";
import type { RequestedAttribute } from "../lib/identity-proofing";
import { useDateFormatter, useMonthFormatter } from "../lib/format-when";
import { useDebouncedValue } from "../lib/use-debounced-value";
import { yiviUniversalLink } from "../lib/yivi-universal-link";
import { Button, DataDisclosure, Icon, Input, Outcome } from "../ui";
import type { DataDisclosureItem, IconName } from "../ui";

// The steps a customer's subject goes through to be verified, shared by the
// on-screen page a member shows (customer-verify) and the hosted page a link
// opens (proof): what is collected, the app to use, then that app's session.

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[13px]";
const ERROR = "text-error text-[12.5px]";
const CAPTION =
  "text-muted font-label text-[10.5px] font-medium tracking-[0.08em] uppercase";
const LINK_BUTTON =
  "rounded-yivi font-display bg-primary text-primary-fg hover:bg-primary-hover inline-flex h-11 items-center justify-center px-[18px] text-[15px] font-semibold";

const QR_SIZE = 240;
const COUNTDOWN_TICK_MS = 1000;

// Who asks: the customer as the steps show it.
export type VerifyCustomer = Pick<ProofingCustomer, "name" | "branding">;

// What the flow collects, as the steps show it.
export interface VerifyFlowInfo {
  name: string;
  requestedAttributes?: string[];
  requiredAssuranceLevel?: string;
  // Whether the subject is asked for their DUO diploma extracts afterwards.
  diplomaMode?: DiplomaMode;
  // Whether the subject is asked for their postal address afterwards.
  addressMode?: AddressMode;
  // Whether the subject is asked for their UWV Verzekeringsbericht afterwards.
  employmentMode?: EmploymentMode;
  // The most days the session's data is kept, as the server works it out:
  // the flow's retention when it sets one, else the customer's.
  retentionDays: number;
}

// Where a subject logs in with DigiD to download the extract of a diploma.
const MIJN_DUO_URL = "https://mijn.duo.nl";
// Where a subject logs in with DigiD to download their Verzekeringsbericht.
const MIJN_UWV_URL = "https://mijn.uwv.nl";

// What the player reads when their kept statement sends the check to review.
const EMPLOYMENT_REVIEW_KEYS = {
  noJob: "identityProofing.employment.review",
  benefit: "identityProofing.employment.reviewBenefit",
  otherIncome: "identityProofing.employment.reviewOtherIncome",
} as const;
// How long typing a postal code or house number pauses before the address is
// looked up.
const ADDRESS_LOOKUP_DEBOUNCE_MS = 300;

const ATTRIBUTE_ICONS: Record<RequestedAttribute, IconName> = {
  dg1: "personal",
  age_over: "birthdate",
  dg11: "personal",
  dg2: "view",
  chip_checks: "lock",
  document_image: "view",
  selfie: "view",
  biometrics: "valid",
};

const METHOD_ICONS: Record<ProofingMethod, IconName> = {
  yivi_app: "scan_qrcode",
  idem_app: "phone",
};

export function Overview({
  customer,
  flow,
  onContinue,
  starting,
  error,
  cancel,
}: Readonly<{
  customer: VerifyCustomer;
  flow: VerifyFlowInfo;
  onContinue: () => void;
  starting: boolean;
  error?: string;
  // A way out beside Continue, e.g. back to the customer; none when absent.
  cancel?: React.ReactNode;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const name = customer.branding.displayName || customer.name;
  const items: DataDisclosureItem[] = (flow.requestedAttributes ?? [])
    .filter(isRequestedAttribute)
    .map((value) => ({
      icon: ATTRIBUTE_ICONS[value],
      label: t(`identityProofingFlows.attributes.${value}`),
      // The age check withholds the date of birth from the document data.
      detail:
        value === ATTRIBUTE_DOCUMENT &&
        flow.requestedAttributes?.includes(ATTRIBUTE_AGE_OVER)
          ? t("customers.onScreen.attributeDetails.dg1WithoutBirthDate")
          : t(`customers.onScreen.attributeDetails.${value}`),
    }));
  if (flow.addressMode !== undefined && flow.addressMode !== "off") {
    items.push({
      icon: "personal",
      label: t("identityProofing.address.overview.label"),
      detail: t("identityProofing.address.overview.detail"),
    });
  }
  if (flow.diplomaMode !== undefined && flow.diplomaMode !== "off") {
    items.push({
      icon: "flag",
      label: t("identityProofing.diplomas.overview.label"),
      detail: t("identityProofing.diplomas.overview.detail"),
    });
  }
  if (flow.employmentMode !== undefined && flow.employmentMode !== "off") {
    items.push({
      icon: "personal",
      label: t("identityProofing.employment.overview.label"),
      detail: t("identityProofing.employment.overview.detail"),
    });
  }
  const { supportContact, privacyUrl } = customer.branding;

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="text-ink text-[18px] font-bold">
          {t("customers.onScreen.overview.heading", { name })}
        </h2>
        <p className={HINT}>
          {t("customers.onScreen.overview.intro", { name })}
        </p>
      </div>
      {items.length === 0 ? (
        <p className={HINT}>{t("customers.onScreen.overview.noData")}</p>
      ) : (
        <DataDisclosure items={items} />
      )}
      <div className="grid grid-cols-2 gap-4">
        <div>
          <div className={CAPTION}>{t("customers.onScreen.overview.flow")}</div>
          <div className="text-ink mt-1 text-[13px]">{flow.name}</div>
        </div>
        <div>
          <div className={CAPTION}>
            {t("customers.onScreen.overview.assurance")}
          </div>
          <div className="text-ink mt-1 text-[13px]">
            {flow.requiredAssuranceLevel
              ? t("customers.flows.eidas", {
                  level: assuranceLevelLabel(flow.requiredAssuranceLevel, t),
                })
              : t("customers.flows.noAssurance")}
          </div>
        </div>
      </div>
      <p className={HINT}>
        {t("customers.onScreen.overview.retention", {
          count: flow.retentionDays,
        })}
      </p>
      {(supportContact !== "" || privacyUrl !== "") && (
        <div className="flex flex-col gap-1 text-[13px]">
          {supportContact !== "" && (
            <span className="text-ink-soft">
              {t("customers.onScreen.overview.support", {
                contact: supportContact,
              })}
            </span>
          )}
          {privacyUrl !== "" && (
            <a
              href={privacyUrl}
              target="_blank"
              rel="noreferrer"
              className="text-link font-semibold hover:underline"
            >
              {t("customers.onScreen.overview.privacy")}
            </a>
          )}
        </div>
      )}
      {error && (
        <p role="alert" className={ERROR}>
          {error}
        </p>
      )}
      <div className="flex justify-end gap-2">
        {cancel}
        <Button icon="arrow_front" loading={starting} onClick={onContinue}>
          {t("customers.onScreen.overview.continue")}
        </Button>
      </div>
    </div>
  );
}

export function MethodChoice({
  method,
  onChange,
  onBack,
  onContinue,
  starting,
  error,
}: Readonly<{
  method: ProofingMethod;
  onChange: (method: ProofingMethod) => void;
  onBack: () => void;
  onContinue: () => void;
  starting: boolean;
  error?: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const options: { value: ProofingMethod; key: "yivi" | "idem" }[] = [
    { value: "yivi_app", key: "yivi" },
    { value: "idem_app", key: "idem" },
  ];

  return (
    <div className="flex flex-col gap-4">
      <h2 className="text-ink text-[18px] font-bold">
        {t("customers.onScreen.method.heading")}
      </h2>
      <fieldset className="flex flex-col gap-3">
        <legend className="sr-only">
          {t("customers.onScreen.method.heading")}
        </legend>
        {options.map((option) => {
          const checked = method === option.value;
          return (
            <label
              key={option.value}
              className={[
                "rounded-yivi flex cursor-pointer items-start gap-3 border p-4",
                checked
                  ? "border-primary bg-highlight"
                  : "border-line-strong bg-surface",
              ].join(" ")}
            >
              <input
                type="radio"
                name="proofing-method"
                value={option.value}
                checked={checked}
                onChange={() => onChange(option.value)}
                className="mt-1"
              />
              <span className="bg-tile text-tile-fg rounded-yivi-sm flex h-9 w-9 shrink-0 items-center justify-center">
                <Icon name={METHOD_ICONS[option.value]} />
              </span>
              <span className="flex flex-col gap-0.5">
                <span className="text-ink text-[14.5px] font-bold">
                  {t(`customers.onScreen.method.${option.key}.title`)}
                </span>
                <span className="text-ink-soft text-[13px]">
                  {t(`customers.onScreen.method.${option.key}.detail`)}
                </span>
              </span>
            </label>
          );
        })}
      </fieldset>
      {error && (
        <p role="alert" className={ERROR}>
          {error}
        </p>
      )}
      <div className="flex justify-between gap-2">
        <Button variant="secondary" icon="arrow_back" onClick={onBack}>
          {t("customers.onScreen.method.back")}
        </Button>
        <Button icon="scan_qrcode" loading={starting} onClick={onContinue}>
          {t("customers.onScreen.method.continue")}
        </Button>
      </div>
    </div>
  );
}

// A verify page's current step. When step changes after the first render,
// focus moves to the step's heading, so a screen reader reads the new step
// instead of focus dropping to the page.
export function StepRegion({
  step,
  children,
}: Readonly<{
  step: string;
  children: React.ReactNode;
}>): React.JSX.Element {
  const ref = useRef<HTMLDivElement>(null);
  const shown = useRef(step);
  useEffect(() => {
    if (shown.current === step) return;
    shown.current = step;
    const heading = ref.current?.querySelector<HTMLElement>("h2, h3");
    const target = heading ?? ref.current;
    if (heading) heading.tabIndex = -1;
    target?.focus();
  }, [step]);
  return (
    <div
      ref={ref}
      tabIndex={-1}
      className="flex flex-col gap-6 outline-none [&_h2]:outline-none [&_h3]:outline-none"
    >
      {children}
    </div>
  );
}

// The running session: the app's QR code and link with a countdown, until
// the request settles or the session runs out. onRestart, when set, offers a
// new session after one ended; outcomeActions go under the outcome.
export function Session({
  target,
  initial,
  deepLink,
  deepLinkExpiresAt,
  method,
  onRestart,
  outcomeActions,
  onSettled,
  diplomaMode = "off",
  diplomas = [],
  onDiplomaStep,
  dataRequest = false,
  addressMode = "off",
  onAddressStep,
  employmentMode = "off",
  onEmploymentStep,
}: Readonly<{
  target: VerifyTarget;
  initial: ProofingProgress;
  deepLink?: string;
  deepLinkExpiresAt?: string;
  method: ProofingMethod;
  onRestart?: () => void;
  outcomeActions?: React.ReactNode;
  // Called with the progress once the session has an outcome, and the subject
  // is done adding diploma extracts when it asks for them; again whenever
  // that outcome changes (a data request's review decided).
  onSettled?: (progress: ProofingProgress) => void;
  // Whether an approved session goes on to the diploma step, and the extracts
  // already added (a hosted page reopened).
  diplomaMode?: DiplomaMode;
  diplomas?: HostedDiploma[];
  // Told whether the diploma step shows, for the page's stepper.
  onDiplomaStep?: (active: boolean) => void;
  // A data request: a refusal reads as the request refused, not the person
  // unverified.
  dataRequest?: boolean;
  // Whether an approved session goes on to the address step, before the
  // diploma step; onAddressStep is told whether it shows.
  addressMode?: AddressMode;
  onAddressStep?: (active: boolean) => void;
  // Whether an approved session goes on to the employment step, after the
  // diploma step; onEmploymentStep is told whether it shows.
  employmentMode?: EmploymentMode;
  onEmploymentStep?: (active: boolean) => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const progress = useProofingProgressQuery(target);
  const current = progress.data ?? initial;
  const [addressDone, setAddressDone] = useState(false);
  const [diplomasDone, setDiplomasDone] = useState(false);
  const [employmentDone, setEmploymentDone] = useState(false);
  // The window of a statement just kept: the progress drops employmentUntil
  // once one is held, and a poll would otherwise leave the step before the
  // subject has seen what was accepted.
  const [keptUntil, setKeptUntil] = useState<string>();
  const employmentUntil = current.employmentUntil ?? keptUntil;
  const secondsLeft = useSecondsLeft(current.linkExpiresAt);
  const view = sessionView({
    status: current.status,
    addressMode,
    addressUntil: current.addressUntil,
    addressDone,
    diplomaMode,
    diplomasUntil: current.diplomasUntil,
    diplomasDone,
    employmentMode,
    employmentUntil,
    employmentDone,
    secondsLeft,
  });
  const askAddress = view === "address";
  const askDiplomas = view === "diplomas";
  const askEmployment = view === "employment";
  const showsOutcome = view === "outcome";
  useEffect(() => {
    if (showsOutcome) {
      onSettled?.(current);
    }
  }, [showsOutcome, current, onSettled]);
  useEffect(() => {
    onAddressStep?.(askAddress);
  }, [askAddress, onAddressStep]);
  useEffect(() => {
    onDiplomaStep?.(askDiplomas);
  }, [askDiplomas, onDiplomaStep]);
  useEffect(() => {
    onEmploymentStep?.(askEmployment);
  }, [askEmployment, onEmploymentStep]);
  const [link, setLink] = useState(deepLink);
  const fresh = useNewClaimLinkMutation(target);
  const newCode = (
    <div className="flex flex-col items-center gap-1">
      <Button
        size="sm"
        variant="secondary"
        icon="scan_qrcode"
        loading={fresh.isPending}
        onClick={() =>
          fresh.mutate(undefined, {
            onSuccess: (claim) => setLink(claim.deepLink),
          })
        }
      >
        {t("customers.onScreen.scan.newCode")}
      </Button>
      <p className={`${HINT} text-center`}>
        {t("customers.onScreen.scan.newCodeHint")}
      </p>
      {fresh.isError && (
        <p role="alert" className={`${ERROR} text-center`}>
          {verifyErrorMessage(target, fresh.error, t)}
        </p>
      )}
    </div>
  );

  if (askAddress && current.addressUntil !== undefined) {
    return (
      <AddressStep
        target={target}
        until={current.addressUntil}
        onDone={() => setAddressDone(true)}
      />
    );
  }
  if (askDiplomas && current.diplomasUntil !== undefined) {
    return (
      <DiplomaStep
        target={target}
        until={current.diplomasUntil}
        initial={diplomas}
        onDone={() => setDiplomasDone(true)}
      />
    );
  }
  if (askEmployment && employmentUntil !== undefined) {
    return (
      <EmploymentStep
        target={target}
        until={employmentUntil}
        onKept={() => setKeptUntil(employmentUntil)}
        onDone={() => setEmploymentDone(true)}
      />
    );
  }
  if (showsOutcome) {
    return (
      <SessionOutcome
        progress={current}
        actions={outcomeActions}
        dataRequest={dataRequest}
      />
    );
  }
  if (view === "expired") {
    return <Expired onRestart={onRestart} />;
  }

  // The page's own link with the manual "new code": the hosted page's view,
  // and the on-screen one's when where the phone is cannot be read.
  const manualIdem = link ? (
    <>
      <h2 className="text-ink text-[18px] font-bold">
        {t("customers.onScreen.scan.idemHeading")}
      </h2>
      <QrCode value={link} />
      <p className={`${HINT} text-center`}>
        {t("customers.onScreen.scan.idemHint")}
      </p>
      <a href={link} className={LINK_BUTTON}>
        {t("customers.onScreen.scan.openIdem")}
      </a>
      <Countdown seconds={secondsLeft} />
      {newCode}
    </>
  ) : (
    // The link was shown in another window: the app carries on there,
    // unless it was closed and a new code hands the session over.
    <>
      <p className={`${HINT} text-center`}>
        {t("customers.onScreen.scan.startedElsewhere")}
      </p>
      {newCode}
    </>
  );

  return (
    <div className="flex flex-col items-center gap-4">
      {method === "yivi_app" && (
        <YiviSession
          target={target}
          sessionSecondsLeft={secondsLeft}
          onRestart={onRestart}
        />
      )}
      {method !== "yivi_app" && target.kind === "request" && (
        <IdemOnScreen
          target={target}
          deepLink={deepLink}
          deepLinkExpiresAt={deepLinkExpiresAt}
          sessionSecondsLeft={secondsLeft}
          fallback={manualIdem}
        />
      )}
      {method !== "yivi_app" && target.kind !== "request" && manualIdem}
      {current.status === "in_progress" &&
        method === "idem_app" &&
        target.kind === "hosted" && (
          <p className="text-link text-[13px] font-semibold">
            {t("customers.onScreen.scan.inProgress")}
          </p>
        )}
    </div>
  );
}

// The opening of an upload step: the identity done, what to upload and the
// three steps to get it, the first linking to where it is downloaded.
function UploadStepIntro({
  section,
  link,
}: Readonly<{
  section: "diplomas" | "employment";
  link: { href: string; label: string };
}>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <>
      <p className="bg-success-bg text-ink rounded-yivi flex items-center gap-2 px-3 py-2 text-[13px] font-semibold">
        <Icon name="valid" className="text-success shrink-0" />
        {t(`identityProofing.${section}.identityDone`)}
      </p>

      <div className="flex flex-col gap-1">
        <h2 className="text-ink text-[18px] font-bold">
          {t(`identityProofing.${section}.heading`)}
        </h2>
        <p className={HINT}>{t(`identityProofing.${section}.intro`)}</p>
      </div>

      <ol className="flex flex-col gap-2.5">
        {(["login", "download", "upload"] as const).map((step, i) => (
          <li key={step} className="flex items-start gap-3 text-[13.5px]">
            <span className="bg-tile text-tile-fg flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[12px] font-bold">
              {i + 1}
            </span>
            <span className="text-ink pt-0.5">
              {t(`identityProofing.${section}.how.${step}`)}
              {step === "login" && (
                <>
                  {" "}
                  <a
                    href={link.href}
                    target="_blank"
                    rel="noreferrer"
                    className="text-link font-semibold hover:underline"
                  >
                    {link.label}
                  </a>
                </>
              )}
            </span>
          </li>
        ))}
      </ol>
    </>
  );
}

// After an approved identity: the subject uploads the extracts of their
// diplomas they downloaded from DUO, each checked at once against DUO's
// signature and their proven name and date of birth. The step ends once one
// is held, or the time to add one is up.
function DiplomaStep({
  target,
  until,
  initial,
  onDone,
}: Readonly<{
  target: VerifyTarget;
  until: string;
  initial: HostedDiploma[];
  onDone: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatDate = useDateFormatter();
  const upload = useUploadDiplomasMutation(target);
  const fileInput = useRef<HTMLInputElement>(null);
  const [held, setHeld] = useState<HostedDiploma[]>(initial);
  const [refused, setRefused] = useState<DiplomaVerdict[]>([]);
  // What the last upload decided, for a screen reader: the lists below
  // appear without moving focus.
  const [announced, setAnnounced] = useState("");
  const secondsLeft = useSecondsLeft(until);
  const open = secondsLeft > 0;
  const canFinish = diplomaStepDone(held.length, secondsLeft);

  function check(files: File[]): void {
    if (files.length === 0) return;
    upload.mutate(files, {
      onSuccess: (verdicts) => {
        setHeld((current) => [
          ...current,
          ...verdicts.flatMap((v) => (v.diploma ? [v.diploma] : [])),
        ]);
        const refusedNow = verdicts.filter((v) => !v.accepted);
        setRefused(refusedNow);
        setAnnounced(
          t("identityProofing.diplomas.verdictsAnnounced", {
            accepted: verdicts.length - refusedNow.length,
            refused: refusedNow.length,
          }),
        );
      },
    });
  }

  let diplomaUploadLabel: string = t("identityProofing.diplomas.chooseAnother");
  if (upload.isPending) {
    diplomaUploadLabel = t("identityProofing.diplomas.checking");
  } else if (held.length === 0) {
    diplomaUploadLabel = t("identityProofing.diplomas.choose");
  }

  return (
    <div className="flex w-full flex-col gap-5">
      {/* Mounted throughout, so each upload's verdicts are announced. */}
      <output className="sr-only">{announced}</output>
      <UploadStepIntro
        section="diplomas"
        link={{
          href: MIJN_DUO_URL,
          label: t("identityProofing.diplomas.how.openDuo"),
        }}
      />

      {held.length > 0 && (
        <ul className="flex flex-col gap-2">
          {held.map((d, i) => (
            <li
              key={`${d.qualification}-${i}`}
              className="bg-success-bg rounded-yivi flex items-start gap-3 px-4 py-3"
            >
              <Icon name="valid" className="text-success mt-0.5 shrink-0" />
              <span className="flex min-w-0 flex-col gap-0.5">
                <span className="text-ink text-[14px] font-semibold">
                  {d.qualification}
                </span>
                <span className="text-ink-soft text-[12.5px]">
                  {diplomaDetail(d, formatDate, t)}
                </span>
              </span>
            </li>
          ))}
        </ul>
      )}

      {refused.length > 0 && (
        <ul className="flex flex-col gap-2">
          {refused.map((v, i) => (
            <li
              key={`${v.fileName}-${i}`}
              className="bg-error-bg rounded-yivi flex items-start gap-3 px-4 py-3"
            >
              <Icon name="invalid" className="text-error mt-0.5 shrink-0" />
              <span className="flex min-w-0 flex-col gap-0.5">
                <span className="text-ink truncate text-[13.5px] font-semibold">
                  {v.fileName}
                </span>
                <span className="text-ink-soft text-[12.5px]">
                  {diplomaRejectionReason(v.reason ?? "", t)}
                </span>
              </span>
            </li>
          ))}
        </ul>
      )}

      {open ? (
        <div className="flex flex-col gap-2">
          <input
            ref={fileInput}
            type="file"
            accept="application/pdf"
            multiple
            className="hidden"
            onChange={(event) => {
              const files = Array.from(event.target.files ?? []);
              // Reset so picking the same file again fires onChange.
              event.target.value = "";
              check(files);
            }}
          />
          <Button
            variant={held.length === 0 ? "primary" : "secondary"}
            icon="add"
            className="w-full"
            loading={upload.isPending}
            onClick={() => fileInput.current?.click()}
          >
            {diplomaUploadLabel}
          </Button>
          {upload.isError && (
            <p role="alert" className={ERROR}>
              {verifyErrorMessage(target, upload.error, t)}
            </p>
          )}
        </div>
      ) : (
        <p className={HINT}>{t("identityProofing.diplomas.closed")}</p>
      )}

      <div className="border-line flex items-center justify-between gap-3 border-t pt-4">
        {open ? <Countdown seconds={secondsLeft} /> : <span />}
        <Button
          variant={held.length === 0 ? "secondary" : "primary"}
          icon="arrow_front"
          disabled={!canFinish || upload.isPending}
          onClick={onDone}
        >
          {t("identityProofing.diplomas.done")}
        </Button>
      </div>
      {!canFinish && (
        <p className={`${HINT} -mt-3 text-right`}>
          {t("identityProofing.diplomas.needOne")}
        </p>
      )}
    </div>
  );
}

// After an approved identity: the subject uploads the Verzekeringsbericht
// they downloaded from UWV, checked at once against UWV's seal and their
// proven name and date of birth. Once one is kept the step shows what was
// accepted and ends when the subject continues, or when the time is up.
function EmploymentStep({
  target,
  until,
  onKept,
  onDone,
}: Readonly<{
  target: VerifyTarget;
  until: string;
  onKept: () => void;
  onDone: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatDate = useDateFormatter();
  const formatMonth = useMonthFormatter();
  const upload = useUploadEmploymentMutation(target);
  const fileInput = useRef<HTMLInputElement>(null);
  const [refused, setRefused] = useState<EmploymentVerdict>();
  const [kept, setKept] = useState<ProofingEmployment>();
  const keptReview = kept === undefined ? undefined : employmentReview(kept);
  const secondsLeft = useSecondsLeft(until);
  const done = employmentStepDone(kept, secondsLeft);

  function check(file: File | undefined): void {
    if (file === undefined) return;
    upload.mutate(file, {
      onSuccess: (verdict) => {
        if (verdict.accepted && verdict.employment !== undefined) {
          setKept(verdict.employment);
          setRefused(undefined);
          onKept();
          return;
        }
        setRefused(verdict);
      },
    });
  }

  let employmentUploadLabel: string = t(
    "identityProofing.employment.chooseAgain",
  );
  if (upload.isPending) {
    employmentUploadLabel = t("identityProofing.employment.checking");
  } else if (refused === undefined) {
    employmentUploadLabel = t("identityProofing.employment.choose");
  }

  return (
    <div className="flex w-full flex-col gap-5">
      <UploadStepIntro
        section="employment"
        link={{
          href: MIJN_UWV_URL,
          label: t("identityProofing.employment.how.openUwv"),
        }}
      />

      {kept !== undefined && (
        <div
          aria-live="polite"
          aria-atomic="true"
          className="bg-success-bg rounded-yivi flex items-start gap-3 px-4 py-3"
        >
          <Icon name="valid" className="text-success mt-0.5 shrink-0" />
          <span className="flex min-w-0 flex-col gap-1">
            <span className="text-ink text-[14px] font-semibold">
              {t("identityProofing.employment.kept", {
                date: formatDate(kept.statementDate),
              })}
            </span>
            <span className="text-ink-soft text-[12.5px]">
              {t("identityProofing.employment.keptChecks")}
            </span>
            {keptReview !== undefined && (
              <span className="text-ink text-[12.5px] font-semibold">
                {t(EMPLOYMENT_REVIEW_KEYS[keptReview])}
              </span>
            )}
            {kept.relations.length === 0 ? (
              <span className="text-ink-soft text-[12.5px]">
                {t("identityProofing.employment.noRelations")}
              </span>
            ) : (
              <ul className="flex flex-col gap-0.5">
                {kept.relations.map((r, i) => (
                  <li
                    key={`${r.payrollTaxNumber}-${i}`}
                    className="text-ink text-[12.5px]"
                  >
                    <span className="font-semibold">{r.employer}</span>{" "}
                    {r.kind === "benefit" && (
                      <span className="text-ink-soft">
                        {t("identityProofing.employment.benefit")}{" "}
                      </span>
                    )}
                    {r.kind === "other_income" && (
                      <span className="text-ink-soft">
                        {t("identityProofing.employment.otherIncome")}{" "}
                      </span>
                    )}
                    <span className="text-ink-soft">
                      {t("identityProofing.employment.period", {
                        from: formatMonth(r.from),
                        to: formatMonth(r.to),
                      })}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </span>
        </div>
      )}

      {refused !== undefined && (
        <p
          role="alert"
          className="bg-error-bg rounded-yivi flex items-start gap-3 px-4 py-3"
        >
          <Icon name="invalid" className="text-error mt-0.5 shrink-0" />
          <span className="flex min-w-0 flex-col gap-0.5">
            <span className="text-ink truncate text-[13.5px] font-semibold">
              {refused.fileName}
            </span>
            <span className="text-ink-soft text-[12.5px]">
              {employmentRejectionReason(refused.reason ?? "", t)}
            </span>
          </span>
        </p>
      )}

      {done ? (
        kept === undefined && (
          <p className={HINT}>{t("identityProofing.employment.closed")}</p>
        )
      ) : (
        <div className="flex flex-col gap-2">
          <input
            ref={fileInput}
            type="file"
            accept="application/pdf"
            className="hidden"
            onChange={(event) => {
              const file = event.target.files?.[0];
              // Reset so picking the same file again fires onChange.
              event.target.value = "";
              check(file);
            }}
          />
          <Button
            icon="add"
            className="w-full"
            loading={upload.isPending}
            onClick={() => fileInput.current?.click()}
          >
            {employmentUploadLabel}
          </Button>
          {upload.isError && (
            <p role="alert" className={ERROR}>
              {verifyErrorMessage(target, upload.error, t)}
            </p>
          )}
        </div>
      )}

      <div className="border-line flex items-center justify-between gap-3 border-t pt-4">
        {done ? <span /> : <Countdown seconds={secondsLeft} />}
        {done && (
          <Button
            variant={kept === undefined ? "secondary" : "primary"}
            icon="arrow_front"
            onClick={onDone}
          >
            {t("identityProofing.employment.continue")}
          </Button>
        )}
      </div>
    </div>
  );
}

// Postal code and house number first: in the Netherlands they fill in the
// street and city.
const ADDRESS_FIELDS = [
  "postalCode",
  "houseNumber",
  "street",
  "city",
  "country",
] as const satisfies readonly (keyof ProofingAddressInput)[];

const ADDRESS_AUTOCOMPLETE: Record<keyof ProofingAddressInput, string> = {
  street: "address-line1",
  houseNumber: "off",
  postalCode: "postal-code",
  city: "address-level2",
  country: "country-name",
};

const EMPTY_ADDRESS: ProofingAddressInput = {
  street: "",
  houseNumber: "",
  postalCode: "",
  city: "",
  country: "",
};

// After an approved identity: the subject enters their postal address. It is
// what they declare, kept beside the proven identity. The step ends once it
// is kept, or the time to enter it is up.
function AddressStep({
  target,
  until,
  onDone,
}: Readonly<{
  target: VerifyTarget;
  until: string;
  onDone: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const submit = useSubmitAddressMutation(target);
  const [address, setAddress] = useState<ProofingAddressInput>(EMPTY_ADDRESS);
  const [touched, setTouched] = useState(false);
  const postalCode = useDebouncedValue(
    address.postalCode,
    ADDRESS_LOOKUP_DEBOUNCE_MS,
  );
  const houseNumber = useDebouncedValue(
    address.houseNumber,
    ADDRESS_LOOKUP_DEBOUNCE_MS,
  );
  const lookupInput = addressLookup(postalCode, houseNumber);
  const lookup = useAddressLookupQuery(target, lookupInput);
  const match = lookup.data && matchingAddress(lookup.data, houseNumber);

  // The address the form was last filled in from, so a field edited after it
  // is left as typed.
  const [filledFor, setFilledFor] = useState("");
  const matchKey = `${lookupInput?.postalCode} ${match?.houseNumber}`;
  if (match !== undefined && matchKey !== filledFor) {
    setFilledFor(matchKey);
    fill(match);
  }

  const secondsLeft = useSecondsLeft(until);
  const open = secondsLeft > 0;
  const missing = ADDRESS_FIELDS.filter((f) => address[f].trim() === "");

  function save(event: React.SubmitEvent<HTMLFormElement>): void {
    event.preventDefault();
    setTouched(true);
    if (missing.length > 0) return;
    submit.mutate(address, { onSuccess: onDone });
  }

  // The register's address fills the form as the register writes it; the
  // house number is kept as typed until it is picked.
  function fill(found: RegisterAddress, houseNumber?: string): void {
    setAddress((current) => ({
      ...found,
      houseNumber: houseNumber ?? current.houseNumber,
      country: t("identityProofing.address.netherlands"),
    }));
  }

  return (
    <form className="flex w-full flex-col gap-5" onSubmit={save} noValidate>
      <p className="bg-success-bg text-ink rounded-yivi flex items-center gap-2 px-3 py-2 text-[13px] font-semibold">
        <Icon name="valid" className="text-success shrink-0" />
        {t("identityProofing.address.identityDone")}
      </p>

      <div className="flex flex-col gap-1">
        <h2 className="text-ink text-[18px] font-bold">
          {t("identityProofing.address.heading")}
        </h2>
        <p className={HINT}>{t("identityProofing.address.intro")}</p>
      </div>

      {open ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[2fr_1fr]">
          {ADDRESS_FIELDS.map((field) => {
            const invalid = touched && missing.includes(field);
            return (
              <div
                key={field}
                className={`flex flex-col gap-1 ${field === "country" ? "sm:col-span-2" : ""}`}
              >
                <label htmlFor={`proofing-address-${field}`} className={LABEL}>
                  {t(`identityProofing.address.fields.${field}`)}
                </label>
                <Input
                  id={`proofing-address-${field}`}
                  autoComplete={ADDRESS_AUTOCOMPLETE[field]}
                  value={address[field]}
                  aria-invalid={invalid}
                  aria-describedby={
                    invalid ? `proofing-address-${field}-error` : undefined
                  }
                  onChange={(event) =>
                    setAddress((current) => ({
                      ...current,
                      [field]: event.target.value,
                    }))
                  }
                />
                {invalid && (
                  <p id={`proofing-address-${field}-error`} className={ERROR}>
                    {t("identityProofing.address.required")}
                  </p>
                )}
              </div>
            );
          })}
          <div aria-live="polite" className="sm:col-span-2">
            {match === undefined && lookup.isFetched && (
              <AddressLookupResult
                found={lookup.data}
                unavailable={lookup.isError}
                onPick={(a) => fill(a, a.houseNumber)}
              />
            )}
          </div>
        </div>
      ) : (
        <p className={HINT}>{t("identityProofing.address.closed")}</p>
      )}
      {submit.isError && (
        <p role="alert" className={ERROR}>
          {proofingErrorMessage(submit.error, t)}
        </p>
      )}

      <div className="border-line flex items-center justify-between gap-3 border-t pt-4">
        {open ? <Countdown seconds={secondsLeft} /> : <span />}
        {open ? (
          <Button type="submit" icon="arrow_front" loading={submit.isPending}>
            {t("identityProofing.address.save")}
          </Button>
        ) : (
          <Button variant="secondary" icon="arrow_front" onClick={onDone}>
            {t("identityProofing.address.continue")}
          </Button>
        )}
      </div>
    </form>
  );
}

// What the lookup found at a postal code and house number that is none of the
// register's addresses: the addresses at that number to pick from, or why
// there are none.
function AddressLookupResult({
  found,
  unavailable,
  onPick,
}: Readonly<{
  found: RegisterAddress[] | undefined;
  unavailable: boolean;
  onPick: (address: RegisterAddress) => void;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  if (unavailable) {
    return (
      <p className={HINT}>{t("identityProofing.address.lookup.unavailable")}</p>
    );
  }
  if (found === undefined) {
    return null;
  }
  if (found.length === 0) {
    return <p className={HINT}>{t("identityProofing.address.lookup.none")}</p>;
  }
  return (
    <div className="flex flex-col gap-1">
      <p className={HINT}>{t("identityProofing.address.lookup.choose")}</p>
      <ul className="border-line rounded-yivi divide-line flex flex-col divide-y border">
        {found.map((a) => (
          <li key={a.houseNumber}>
            <button
              type="button"
              className="text-ink hover:bg-surface-3 w-full px-3 py-2 text-left text-[13px]"
              onClick={() => onPick(a)}
            >
              {`${a.street} ${a.houseNumber}, ${a.postalCode} ${a.city}`}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// The line under a diploma's qualification: who awarded it, when, its level.
function diplomaDetail(
  d: HostedDiploma,
  formatDate: (iso: string) => string,
  t: TFunction,
): string {
  const parts = [d.institution, formatDate(d.dateAwarded)];
  if (d.nlqfLevel) {
    parts.push(t("identityProofing.diplomas.level", { level: d.nlqfLevel }));
  }
  return parts.join(" · ");
}

// A code the page shows for the Idem app: a claim while no phone scanned
// (for "waiting"), a handover once the app left (for "away"); expiresAt is
// absent when unknown.
interface IdemCode {
  deepLink: string;
  expiresAt?: string;
  for: ProofingApp;
}

// The Idem app's part on the member's screen, following the phone: its QR
// while nobody scanned (renewed as it lapses), hidden while the app holds the
// session, and a handover QR the moment the app is closed, so whoever is beside
// the subject scans it without asking. fallback is the manual "new code" for
// when where the phone is cannot be read.
function IdemOnScreen({
  target,
  deepLink,
  deepLinkExpiresAt,
  sessionSecondsLeft,
  fallback,
}: Readonly<{
  target: Extract<VerifyTarget, { kind: "request" }>;
  deepLink?: string;
  deepLinkExpiresAt?: string;
  sessionSecondsLeft: number;
  fallback: React.ReactNode;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const app = useProofingAppQuery(target.slug, target.requestId, true);
  const fresh = useNewClaimLinkMutation(target);
  const { mutate, isPending } = fresh;
  const [code, setCode] = useState<IdemCode | undefined>(
    deepLink
      ? { deepLink, expiresAt: deepLinkExpiresAt, for: "waiting" }
      : undefined,
  );
  const codeSecondsLeft = useSecondsLeft(code?.expiresAt);
  const codeLapsed = code?.expiresAt !== undefined && codeSecondsLeft === 0;
  // Until the first read, the page's own claim is what shows.
  const state = app.data ?? "waiting";
  const shown = code?.for === state && !codeLapsed ? code : undefined;

  // Once the app holds the session its claim was used, or its return
  // cancelled the handover: neither code may show again.
  const [seen, setSeen] = useState(app.data);
  if (app.data !== seen) {
    setSeen(app.data);
    if (app.data === "connected") setCode(undefined);
  }
  // The latest read, for a mint that answers after the app came back.
  const latest = useRef(app.data);
  // The poll the last mint was for: a refused one waits for the next poll.
  const mintedFor = useRef(0);
  useEffect(() => {
    latest.current = app.data;
    if (app.data === undefined || app.data === "connected") return;
    if (shown || isPending || mintedFor.current === app.dataUpdatedAt) return;
    mintedFor.current = app.dataUpdatedAt;
    const wanted = app.data;
    mutate(undefined, {
      onSuccess: (claim) => {
        if (latest.current === wanted) setCode({ ...claim, for: wanted });
      },
    });
  }, [app.data, app.dataUpdatedAt, shown, isPending, mutate]);

  if (app.data === undefined && app.isError) {
    return <>{fallback}</>;
  }
  if (state === "connected") {
    return (
      <div className="flex flex-col items-center gap-2 text-center">
        <span className="bg-tile text-tile-fg rounded-yivi flex h-12 w-12 items-center justify-center">
          <Icon name="phone" size={24} />
        </span>
        <h2 className="text-ink text-[18px] font-bold">
          {t("customers.onScreen.scan.connectedHeading")}
        </h2>
        <p className={HINT}>{t("customers.onScreen.scan.connectedHint")}</p>
        <Countdown seconds={sessionSecondsLeft} />
      </div>
    );
  }
  const away = state === "away";
  const heading = t(
    away
      ? "customers.onScreen.scan.awayHeading"
      : "customers.onScreen.scan.idemHeading",
  );
  const hint = t(
    away
      ? "customers.onScreen.scan.awayHint"
      : "customers.onScreen.scan.idemHint",
  );
  if (!shown) {
    return (
      <div className="flex flex-col items-center gap-4">
        <h2 className="text-ink text-[18px] font-bold">{heading}</h2>
        <QrPlaceholder label={t("customers.onScreen.scan.newCodeLoading")} />
        {fresh.isError && (
          <p className={`${ERROR} text-center`}>
            {proofingErrorMessage(fresh.error, t)}
          </p>
        )}
      </div>
    );
  }
  return (
    <IdemQr
      heading={heading}
      hint={hint}
      code={shown}
      seconds={sessionSecondsLeft}
    />
  );
}

function IdemQr({
  heading,
  hint,
  code,
  seconds,
}: Readonly<{
  heading: string;
  hint: string;
  code: IdemCode;
  seconds: number;
}>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-4">
      <h2 className="text-ink text-[18px] font-bold">{heading}</h2>
      <QrCode value={code.deepLink} />
      <p className={`${HINT} text-center`}>{hint}</p>
      <a href={code.deepLink} className={LINK_BUTTON}>
        {t("customers.onScreen.scan.openIdem")}
      </a>
      <Countdown seconds={seconds} />
    </div>
  );
}

// The Yivi app's part: start the disclosure, show its QR until the subject
// has shared, then the face check against the disclosed photo.
function YiviSession({
  target,
  sessionSecondsLeft,
  onRestart,
}: Readonly<{
  target: VerifyTarget;
  sessionSecondsLeft: number;
  onRestart?: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const { mutateAsync } = useStartProofingYiviMutation(target);
  // The start's own outcome, not the mutation's state: StrictMode's remount
  // detaches the mutation from this component, which then never hears back.
  const [start, setStart] = useState<{
    data?: ProofingYiviStart;
    error?: Error;
  }>({});
  // Once per mount: StrictMode runs an effect twice in development, and each
  // run would start a Yivi session of its own.
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    mutateAsync().then(
      (data) => setStart({ data }),
      (error: unknown) =>
        setStart({
          error: error instanceof Error ? error : new Error(String(error)),
        }),
    );
  }, [mutateAsync]);
  const yiviSecondsLeft = useSecondsLeft(start.data?.expiresAt);
  const secondsLeft = start.data
    ? Math.min(sessionSecondsLeft, yiviSecondsLeft)
    : sessionSecondsLeft;
  // Polling stops once the session has expired: nothing can finish it then.
  const disclosure = useYiviDisclosureQuery(
    target,
    start.data !== undefined && secondsLeft > 0,
  );

  if (start.error) {
    return (
      <Failed
        message={verifyErrorMessage(target, start.error, t)}
        onRestart={onRestart}
      />
    );
  }
  if (disclosure.isError) {
    return (
      <Failed
        message={verifyErrorMessage(target, disclosure.error, t)}
        onRestart={onRestart}
      />
    );
  }
  if (disclosure.data?.done) {
    if (!disclosure.data.ok) {
      return (
        <Failed
          message={t("customers.onScreen.yiviEnded", {
            reason: yiviEndReason(disclosure.data.code, t),
          })}
          onRestart={onRestart}
        />
      );
    }
    // The disclosure decided the session: the request's own poll shows the
    // outcome once it is recorded.
    return (
      <output className={`${HINT} block text-center`}>
        {t("customers.onScreen.yiviShared")}
      </output>
    );
  }
  if (start.data && secondsLeft === 0) {
    return <Expired onRestart={onRestart} />;
  }

  const link =
    start.data === undefined
      ? undefined
      : yiviUniversalLink(start.data.walletLink);
  return (
    <>
      <h2 className="text-ink text-[18px] font-bold">
        {t("customers.onScreen.scan.yiviHeading")}
      </h2>
      {link === undefined ? (
        <QrPlaceholder label={t("customers.onScreen.scan.starting")} />
      ) : (
        <QrCode value={link} />
      )}
      <p className={`${HINT} text-center`}>
        {t("customers.onScreen.scan.yiviHint")}
      </p>
      {link !== undefined && (
        <a href={link} className={LINK_BUTTON}>
          {t("customers.onScreen.scan.openYivi")}
        </a>
      )}
      {start.data && <Countdown seconds={secondsLeft} />}
    </>
  );
}

function yiviEndReason(code: string | undefined, t: TFunction): string {
  if (code === "document_not_accepted") {
    return t("customers.onScreen.yiviCodes.documentNotAccepted");
  }
  if (code === "age_not_disclosed") {
    return t("customers.onScreen.yiviCodes.ageNotDisclosed");
  }
  return code ?? t("identityProofing.errors.generic");
}

function SessionOutcome({
  progress,
  actions,
  dataRequest,
}: Readonly<{
  progress: ProofingProgress;
  actions?: React.ReactNode;
  dataRequest: boolean;
}>): React.JSX.Element {
  const { t } = useTranslation();
  if (
    progress.status === "rejected" &&
    dataRequest &&
    dataRequestRefusal(progress.errorCode) !== undefined
  ) {
    return (
      <Outcome
        tone="info"
        role="status"
        icon="invalid"
        title={t("customers.onScreen.outcome.refused.title")}
        message={t("customers.onScreen.outcome.refused.message")}
        action={actions}
      />
    );
  }
  switch (progress.status) {
    case "approved":
      return (
        <Outcome
          tone="success"
          role="status"
          icon="valid"
          title={t("customers.onScreen.outcome.approved.title")}
          message={t("customers.onScreen.outcome.approved.message")}
          action={actions}
        />
      );
    case "rejected":
      return (
        <Outcome
          tone="error"
          icon="invalid"
          role="alert"
          title={t("customers.onScreen.outcome.rejected.title")}
          message={
            progress.errorCode
              ? proofingRejectionReason(progress.errorCode, t)
              : t("customers.onScreen.outcome.rejected.message")
          }
          action={actions}
        />
      );
    case "needs_review":
      return (
        <Outcome
          tone="info"
          role="status"
          icon="time"
          title={t("customers.onScreen.outcome.review.title")}
          message={t("customers.onScreen.outcome.review.message")}
          action={actions}
        />
      );
    case "cancelled":
      return (
        <Outcome
          tone="info"
          role="status"
          icon="invalid"
          title={t("customers.onScreen.outcome.cancelled.title")}
          message={t("customers.onScreen.outcome.cancelled.message")}
          action={actions}
        />
      );
    default:
      return (
        <Outcome
          tone="info"
          role="status"
          icon="time"
          title={t("customers.onScreen.outcome.expired.title")}
          message={t("customers.onScreen.outcome.expired.message")}
          action={actions}
        />
      );
  }
}

function Expired({
  onRestart,
}: Readonly<{ onRestart?: () => void }>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div
      aria-live="polite"
      aria-atomic="true"
      className="flex flex-col items-center gap-4"
    >
      <QrPlaceholder label={t("customers.onScreen.scan.expired")} expired />
      {onRestart && (
        <Button variant="secondary" icon="scan_qrcode" onClick={onRestart}>
          {t("customers.onScreen.scan.restart")}
        </Button>
      )}
    </div>
  );
}

function Failed({
  message,
  actionLabel,
  onRestart,
}: Readonly<{
  message: string;
  actionLabel?: string;
  onRestart?: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-3 text-center">
      <p role="alert" className={ERROR}>
        {message}
      </p>
      {onRestart && (
        <Button variant="secondary" onClick={onRestart}>
          {actionLabel ?? t("customers.onScreen.scan.restart")}
        </Button>
      )}
    </div>
  );
}

function Countdown({
  seconds,
}: Readonly<{ seconds: number }>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <p className="text-ink-soft flex items-center gap-1.5 text-[13px]">
      <Icon name="time" />
      <span role="timer" aria-live="off">
        {t("customers.onScreen.scan.expiresIn", {
          time: formatDuration(seconds),
        })}
      </span>
    </p>
  );
}

function QrCode({ value }: Readonly<{ value: string }>): React.JSX.Element {
  const [dataUrl, setDataUrl] = useState("");
  useEffect(() => {
    let cancelled = false;
    QRCode.toDataURL(value, { margin: 1, width: QR_SIZE })
      .then((url) => {
        if (!cancelled) setDataUrl(url);
      })
      .catch(() => {
        // The link button beside it still works without the picture.
        if (!cancelled) setDataUrl("");
      });
    return () => {
      cancelled = true;
    };
  }, [value]);

  if (dataUrl === "") return <QrPlaceholder />;
  return (
    <img
      src={dataUrl}
      alt=""
      width={QR_SIZE}
      height={QR_SIZE}
      className="rounded-yivi border-line-strong h-auto max-w-full border"
    />
  );
}

function QrPlaceholder({
  label,
  expired = false,
}: Readonly<{
  label?: string;
  expired?: boolean;
}>): React.JSX.Element {
  return (
    <div
      className="border-line-strong bg-surface rounded-yivi text-muted flex flex-col items-center justify-center gap-2 border px-4 text-center"
      style={{ width: QR_SIZE, maxWidth: "100%", aspectRatio: "1" }}
    >
      {expired ? (
        <Icon name="time" size={32} />
      ) : (
        <span
          aria-hidden="true"
          className="h-8 w-8 animate-spin rounded-full border-2 border-current border-t-transparent"
        />
      )}
      {label && <span className="text-[13px]">{label}</span>}
    </div>
  );
}

// Seconds left until expiresAt, re-read every second; 0 without one.
function useSecondsLeft(expiresAt: string | undefined): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (expiresAt === undefined) return;
    const timer = setInterval(() => setNow(Date.now()), COUNTDOWN_TICK_MS);
    return () => clearInterval(timer);
  }, [expiresAt]);
  return expiresAt === undefined ? 0 : secondsUntil(expiresAt, now);
}
