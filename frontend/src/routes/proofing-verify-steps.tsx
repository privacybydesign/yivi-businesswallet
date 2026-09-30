import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import type { TFunction } from "i18next";
import * as React from "react";
import {
  useNewProofingClaimLinkMutation,
  useProofingAppQuery,
  useProofingProgressQuery,
  useProofingYiviDisclosureQuery,
  useStartProofingYiviMutation,
  useSubmitProofingFaceFrameMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingCustomer,
  ProofingFaceVerdict,
  ProofingApp,
  ProofingMethod,
  ProofingProgress,
  VerifyTarget,
} from "../api/identity-proofing";
import {
  formatDuration,
  isProofingLive,
  isRequestedAttribute,
  proofingErrorMessage,
  proofingRejectionReason,
  secondsUntil,
} from "../lib/identity-proofing";
import type { RequestedAttribute } from "../lib/identity-proofing";
import { yiviUniversalLink } from "../lib/yivi-universal-link";
import { Button, DataDisclosure, Icon, Outcome } from "../ui";
import type { DataDisclosureItem, IconName } from "../ui";

// The steps a customer's subject goes through to be verified, shared by the
// on-screen page a member shows (customer-verify) and the hosted page a link
// opens (proof): what is collected, the app to use, then that app's session.

const HINT = "text-ink-soft text-[13px]";
const ERROR = "text-error text-[12.5px]";
const CAPTION =
  "text-muted font-mono text-[10.5px] font-medium tracking-[0.08em] uppercase";
const LINK_BUTTON =
  "rounded-yivi font-display bg-primary text-primary-fg hover:bg-primary-hover inline-flex h-11 items-center justify-center px-[18px] text-[15px] font-semibold";

const QR_SIZE = 240;
const COUNTDOWN_TICK_MS = 1000;
// A face frame as IPS's own bound-login page sends it: 640 px wide, JPEG at
// 0.8, one every 400 ms.
const FACE_FRAME_WIDTH = 640;
const FACE_FRAME_QUALITY = 0.8;
const FACE_FRAME_INTERVAL_MS = 400;
const FACE_DECISION_PENDING = "pending";

// Who asks: the customer as the steps show it.
export type VerifyCustomer = Pick<
  ProofingCustomer,
  "name" | "branding" | "dataRetentionDays"
>;

// What the flow collects, as the steps show it.
export interface VerifyFlowInfo {
  name: string;
  requestedAttributes?: string[];
  requiredAssuranceLevel?: string;
}

const ATTRIBUTE_ICONS: Record<RequestedAttribute, IconName> = {
  dg1: "personal",
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
}: {
  customer: VerifyCustomer;
  flow: VerifyFlowInfo;
  onContinue: () => void;
  starting: boolean;
  error?: string;
  // A way out beside Continue, e.g. back to the customer; none when absent.
  cancel?: React.ReactNode;
}): React.JSX.Element {
  const { t } = useTranslation();
  const name = customer.branding.displayName || customer.name;
  const items: DataDisclosureItem[] = (flow.requestedAttributes ?? [])
    .filter(isRequestedAttribute)
    .map((value) => ({
      icon: ATTRIBUTE_ICONS[value],
      label: t(`identityProofingFlows.attributes.${value}`),
      detail: t(`customers.onScreen.attributeDetails.${value}`),
    }));
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
                  level:
                    flow.requiredAssuranceLevel.charAt(0).toUpperCase() +
                    flow.requiredAssuranceLevel.slice(1),
                })
              : t("customers.flows.noAssurance")}
          </div>
        </div>
      </div>
      <p className={HINT}>
        {t("customers.onScreen.overview.retention", {
          count: customer.dataRetentionDays,
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
      {error && <p className={ERROR}>{error}</p>}
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
}: {
  method: ProofingMethod;
  onChange: (method: ProofingMethod) => void;
  onBack: () => void;
  onContinue: () => void;
  starting: boolean;
  error?: string;
}): React.JSX.Element {
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
              <span className="bg-surface-3 text-ink rounded-yivi-sm flex h-9 w-9 shrink-0 items-center justify-center">
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
      {error && <p className={ERROR}>{error}</p>}
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
}: {
  target: VerifyTarget;
  initial: ProofingProgress;
  deepLink?: string;
  deepLinkExpiresAt?: string;
  method: ProofingMethod;
  onRestart?: () => void;
  outcomeActions?: React.ReactNode;
  // Called with the status once the session has an outcome.
  onSettled?: (status: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const progress = useProofingProgressQuery(target);
  const current = progress.data ?? initial;
  const settled = !isProofingLive(current.status);
  useEffect(() => {
    if (settled) {
      onSettled?.(current.status);
    }
  }, [settled, current.status, onSettled]);
  const secondsLeft = useSecondsLeft(current.linkExpiresAt);
  const [link, setLink] = useState(deepLink);
  const fresh = useNewProofingClaimLinkMutation(target);
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
        <p className={`${ERROR} text-center`}>
          {proofingErrorMessage(fresh.error, t)}
        </p>
      )}
    </div>
  );

  if (settled) {
    return <SessionOutcome progress={current} actions={outcomeActions} />;
  }
  if (secondsLeft === 0 && current.status === "pending") {
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
      {method === "yivi_app" ? (
        <YiviSession
          target={target}
          sessionSecondsLeft={secondsLeft}
          onRestart={onRestart}
        />
      ) : target.kind === "request" ? (
        <IdemOnScreen
          target={target}
          deepLink={deepLink}
          deepLinkExpiresAt={deepLinkExpiresAt}
          sessionSecondsLeft={secondsLeft}
          fallback={manualIdem}
        />
      ) : (
        manualIdem
      )}
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
}: {
  target: Extract<VerifyTarget, { kind: "request" }>;
  deepLink?: string;
  deepLinkExpiresAt?: string;
  sessionSecondsLeft: number;
  fallback: React.ReactNode;
}): React.JSX.Element {
  const { t } = useTranslation();
  const app = useProofingAppQuery(target.slug, target.requestId, true);
  const fresh = useNewProofingClaimLinkMutation(target);
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
        <span className="bg-surface-3 text-ink rounded-yivi flex h-12 w-12 items-center justify-center">
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
}: {
  heading: string;
  hint: string;
  code: IdemCode;
  seconds: number;
}): React.JSX.Element {
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
}: {
  target: VerifyTarget;
  sessionSecondsLeft: number;
  onRestart?: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const start = useStartProofingYiviMutation(target);
  const { mutate } = start;
  useEffect(() => {
    mutate();
  }, [mutate]);
  const disclosure = useProofingYiviDisclosureQuery(target, start.isSuccess);
  const yiviSecondsLeft = useSecondsLeft(start.data?.expiresAt);
  const secondsLeft = start.data
    ? Math.min(sessionSecondsLeft, yiviSecondsLeft)
    : sessionSecondsLeft;

  if (start.isError) {
    return (
      <Failed
        message={proofingErrorMessage(start.error, t)}
        onRestart={onRestart}
      />
    );
  }
  if (disclosure.isError) {
    return (
      <Failed
        message={proofingErrorMessage(disclosure.error, t)}
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
    return (
      <FaceCheck target={target} stableFrames={disclosure.data.stableFrames} />
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
  switch (code) {
    case "photo_missing":
      return t("customers.onScreen.yiviCodes.photoMissing");
    case "reference_no_face":
      return t("customers.onScreen.yiviCodes.referenceNoFace");
    default:
      return code ?? t("identityProofing.errors.generic");
  }
}

// The live face check of a Yivi session: camera frames are sent one at a time
// until IPS decides. The decision itself lands on the request, which the
// session polls; this only shows the progress.
function FaceCheck({
  target,
  stableFrames,
}: {
  target: VerifyTarget;
  stableFrames?: number;
}): React.JSX.Element {
  const { t } = useTranslation();
  const videoRef = useRef<HTMLVideoElement>(null);
  const [cameraError, setCameraError] = useState(false);
  const [ready, setReady] = useState(false);
  const [verdict, setVerdict] = useState<ProofingFaceVerdict>();
  const [frameError, setFrameError] = useState<string>();
  const submit = useSubmitProofingFaceFrameMutation(target);
  const { mutateAsync } = submit;
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let stream: MediaStream | undefined;
    let cancelled = false;
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: "user" }, audio: false })
      .then(async (media) => {
        if (cancelled) {
          media.getTracks().forEach((track) => track.stop());
          return;
        }
        stream = media;
        const video = videoRef.current;
        if (!video) return;
        video.srcObject = media;
        await video.play();
        setReady(true);
      })
      .catch(() => {
        if (!cancelled) setCameraError(true);
      });
    return () => {
      cancelled = true;
      setReady(false);
      stream?.getTracks().forEach((track) => track.stop());
    };
  }, [attempt]);

  const decided =
    verdict !== undefined && verdict.decision !== FACE_DECISION_PENDING;

  useEffect(() => {
    if (!ready || decided || frameError !== undefined) return;
    let cancelled = false;
    const timer = setTimeout(() => {
      const frame = captureFrame(videoRef.current);
      if (frame === undefined) return;
      mutateAsync(frame)
        .then((next) => {
          if (!cancelled) setVerdict(next);
        })
        .catch((error: unknown) => {
          if (!cancelled) setFrameError(proofingErrorMessage(error, t));
        });
    }, FACE_FRAME_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [ready, decided, frameError, verdict, mutateAsync, t]);

  const total = verdict?.stableFrames ?? stableFrames;
  return (
    <>
      <h2 className="text-ink text-[18px] font-bold">
        {t("customers.onScreen.face.heading")}
      </h2>
      <p className={`${HINT} text-center`}>
        {t("customers.onScreen.face.hint")}
      </p>
      {cameraError ? (
        <Failed
          message={t("customers.onScreen.face.cameraError")}
          actionLabel={t("customers.onScreen.face.retry")}
          onRestart={() => {
            setCameraError(false);
            setAttempt((a) => a + 1);
          }}
        />
      ) : (
        <video
          ref={videoRef}
          muted
          playsInline
          className="rounded-yivi border-line-strong bg-surface-3 aspect-[4/3] w-full max-w-sm -scale-x-100 border object-cover"
        />
      )}
      {frameError !== undefined ? (
        <Failed
          message={frameError}
          actionLabel={t("customers.onScreen.face.retry")}
          onRestart={() => setFrameError(undefined)}
        />
      ) : verdict && !verdict.faceDetected ? (
        <p className="text-warning-fg text-[13px]">
          {t("customers.onScreen.face.noFace")}
        </p>
      ) : (
        total !== undefined && (
          <p className="text-ink-soft text-[13px]">
            {t("customers.onScreen.face.progress", {
              count: verdict?.consecutive ?? 0,
              total,
            })}
          </p>
        )
      )}
    </>
  );
}

// One JPEG frame of the video (unmirrored), or undefined before it has one.
function captureFrame(video: HTMLVideoElement | null): string | undefined {
  if (!video || video.videoWidth === 0) return undefined;
  const canvas = document.createElement("canvas");
  canvas.width = FACE_FRAME_WIDTH;
  canvas.height = Math.round(
    (video.videoHeight / video.videoWidth) * FACE_FRAME_WIDTH,
  );
  const context = canvas.getContext("2d");
  if (!context) return undefined;
  context.drawImage(video, 0, 0, canvas.width, canvas.height);
  return canvas.toDataURL("image/jpeg", FACE_FRAME_QUALITY);
}

function SessionOutcome({
  progress,
  actions,
}: {
  progress: ProofingProgress;
  actions?: React.ReactNode;
}): React.JSX.Element {
  const { t } = useTranslation();
  switch (progress.status) {
    case "approved":
      return (
        <Outcome
          tone="success"
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
          icon="time"
          title={t("customers.onScreen.outcome.expired.title")}
          message={t("customers.onScreen.outcome.expired.message")}
          action={actions}
        />
      );
  }
}

function Expired({ onRestart }: { onRestart?: () => void }): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-4">
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
}: {
  message: string;
  actionLabel?: string;
  onRestart?: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-3 text-center">
      <p className={ERROR}>{message}</p>
      {onRestart && (
        <Button variant="secondary" onClick={onRestart}>
          {actionLabel ?? t("customers.onScreen.scan.restart")}
        </Button>
      )}
    </div>
  );
}

function Countdown({ seconds }: { seconds: number }): React.JSX.Element {
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

function QrCode({ value }: { value: string }): React.JSX.Element {
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
      className="rounded-yivi border-line-strong border"
    />
  );
}

function QrPlaceholder({
  label,
  expired = false,
}: {
  label?: string;
  expired?: boolean;
}): React.JSX.Element {
  return (
    <div
      className="border-line-strong bg-surface rounded-yivi text-muted flex flex-col items-center justify-center gap-2 border px-4 text-center"
      style={{ width: QR_SIZE, height: QR_SIZE }}
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
