import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import type { TFunction } from "i18next";
import * as React from "react";
import {
  useCreateProofingRequestMutation,
  useProofingCustomerFlowsQuery,
  useProofingCustomerQuery,
  useProofingRequestQuery,
  useProofingYiviDisclosureQuery,
  useStartProofingYiviMutation,
  useSubmitProofingFaceFrameMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingCustomer,
  ProofingCustomerFlow,
  ProofingFaceVerdict,
  ProofingMethod,
  ProofingRequest,
  ProofingSent,
} from "../api/identity-proofing";
import {
  formatDuration,
  isProofingLive,
  isRequestedAttribute,
  proofingErrorMessage,
  proofingRejectionReason,
  secondsUntil,
  yiviSessionLink,
  yiviSessionQrPayload,
} from "../lib/identity-proofing";
import type { RequestedAttribute } from "../lib/identity-proofing";
import {
  Button,
  Card,
  DataDisclosure,
  Icon,
  Outcome,
  Stepper,
  TopBar,
} from "../ui";
import type { DataDisclosureItem, IconName } from "../ui";
import { CustomerMark } from "./proofing-customer-ui";

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

type Step = "overview" | "method" | "session";
const STEPS: readonly Step[] = ["overview", "method", "session"];

// What the send form hands over: the subject as the sender typed it. Kept out
// of the URL, so a reload runs the flow without them.
interface VerifyState {
  name?: string;
  email?: string;
}

function verifyState(state: unknown): VerifyState {
  if (typeof state !== "object" || state === null) return {};
  const { name, email } = state as Record<string, unknown>;
  return {
    name: typeof name === "string" ? name : undefined,
    email: typeof email === "string" ? email : undefined,
  };
}

const ATTRIBUTE_ICONS: Record<RequestedAttribute, IconName> = {
  dg1: "personal",
  dg11: "personal",
  dg2: "view",
  chip_checks: "lock",
  selfie: "view",
  biometrics: "valid",
};

const METHOD_ICONS: Record<ProofingMethod, IconName> = {
  yivi_app: "scan_qrcode",
  idem_app: "phone",
};

// A customer's subject verified on the sender's screen: what the customer
// collects, the app they pick, then that app's QR code for as long as the
// session runs. Nothing is mailed; the session starts when the app is picked.
export default function CustomerVerify(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, customerId } = useParams();
  // Guaranteed by the ":orgSlug/…/:customerId/verify" route this mounts under.
  const slug = orgSlug!;
  const id = customerId!;
  const [searchParams] = useSearchParams();
  const subject = verifyState(useLocation().state);
  const customer = useProofingCustomerQuery(slug, id);
  const flows = useProofingCustomerFlowsQuery(slug, id);
  const flowId = searchParams.get("flow") ?? customer.data?.defaultFlowId;
  const flow = flows.data?.find((f) => f.assigned && f.id === flowId);

  return (
    <>
      <TopBar
        title={t("customers.onScreen.title")}
        subtitle={customer.data?.name}
      />
      <div className="mx-auto w-full max-w-xl px-4 py-6">
        {customer.isPending || flows.isPending ? (
          <p className={HINT}>{t("common.loading")}</p>
        ) : customer.isError || flows.isError ? (
          <p className={ERROR}>
            {proofingErrorMessage(customer.error ?? flows.error, t)}
          </p>
        ) : flow === undefined ? (
          <p className={ERROR}>{t("customers.onScreen.missingFlow")}</p>
        ) : (
          <VerifyFlow
            slug={slug}
            customer={customer.data}
            flow={flow}
            subject={subject}
          />
        )}
      </div>
    </>
  );
}

function VerifyFlow({
  slug,
  customer,
  flow,
  subject,
}: {
  slug: string;
  customer: ProofingCustomer;
  flow: ProofingCustomerFlow;
  subject: VerifyState;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>("overview");
  const [method, setMethod] = useState<ProofingMethod>("idem_app");
  const [sent, setSent] = useState<ProofingSent>();
  const create = useCreateProofingRequestMutation(slug);
  const name = customer.branding.displayName || customer.name;

  function start(): void {
    create.mutate(
      {
        customerId: customer.id,
        email: subject.email ?? "",
        name: subject.name ?? "",
        flowId: flow.id,
        method,
        channel: "on_screen",
      },
      {
        onSuccess: (created) => {
          setSent(created);
          setStep("session");
        },
      },
    );
  }

  function restart(): void {
    setSent(undefined);
    create.reset();
    setStep("method");
  }

  return (
    <Card className="flex flex-col gap-6 p-6">
      <div className="flex items-center gap-3">
        <CustomerMark customer={customer} size="lg" />
        <span className="text-ink text-[16px] font-bold">{name}</span>
      </div>
      <Stepper
        steps={STEPS.map((s) => t(`customers.onScreen.steps.${s}`))}
        current={STEPS.indexOf(step)}
      />
      {step === "overview" && (
        <Overview
          customer={customer}
          flow={flow}
          onContinue={() => setStep("method")}
        />
      )}
      {step === "method" && (
        <MethodChoice
          method={method}
          onChange={setMethod}
          onBack={() => setStep("overview")}
          onContinue={start}
          starting={create.isPending}
          error={
            create.isError ? proofingErrorMessage(create.error, t) : undefined
          }
        />
      )}
      {step === "session" && sent && (
        <Session
          slug={slug}
          customer={customer}
          sent={sent}
          method={method}
          onRestart={restart}
        />
      )}
    </Card>
  );
}

function Overview({
  customer,
  flow,
  onContinue,
}: {
  customer: ProofingCustomer;
  flow: ProofingCustomerFlow;
  onContinue: () => void;
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
      <div className="flex justify-end gap-2">
        <Link
          to=".."
          relative="path"
          className="text-ink-soft self-center text-[13px] font-semibold hover:underline"
        >
          {t("customers.onScreen.overview.cancel")}
        </Link>
        <Button icon="arrow_front" onClick={onContinue}>
          {t("customers.onScreen.overview.continue")}
        </Button>
      </div>
    </div>
  );
}

function MethodChoice({
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
// the request settles or the session runs out.
function Session({
  slug,
  customer,
  sent,
  method,
  onRestart,
}: {
  slug: string;
  customer: ProofingCustomer;
  sent: ProofingSent;
  method: ProofingMethod;
  onRestart: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const request = useProofingRequestQuery(slug, sent.id);
  const current: ProofingRequest = request.data ?? sent;
  const secondsLeft = useSecondsLeft(current.linkExpiresAt);

  if (!isProofingLive(current.status)) {
    return (
      <SessionOutcome
        request={current}
        customer={customer}
        onRestart={onRestart}
      />
    );
  }
  if (secondsLeft === 0 && current.status === "pending") {
    return <Expired onRestart={onRestart} />;
  }

  return (
    <div className="flex flex-col items-center gap-4">
      {method === "yivi_app" ? (
        <YiviSession
          slug={slug}
          requestId={sent.id}
          sessionSecondsLeft={secondsLeft}
          onRestart={onRestart}
        />
      ) : sent.deepLink ? (
        <>
          <h2 className="text-ink text-[18px] font-bold">
            {t("customers.onScreen.scan.idemHeading")}
          </h2>
          <QrCode value={sent.deepLink} />
          <p className={`${HINT} text-center`}>
            {t("customers.onScreen.scan.idemHint")}
          </p>
          <a href={sent.deepLink} className={LINK_BUTTON}>
            {t("customers.onScreen.scan.openIdem")}
          </a>
          <Countdown seconds={secondsLeft} />
        </>
      ) : (
        <p className={ERROR}>{t("identityProofing.errors.generic")}</p>
      )}
      {current.status === "in_progress" && method === "idem_app" && (
        <p className="text-link text-[13px] font-semibold">
          {t("customers.onScreen.scan.inProgress")}
        </p>
      )}
    </div>
  );
}

// The Yivi app's part: start the disclosure, show its QR until the subject
// has shared, then the face check against the disclosed photo.
function YiviSession({
  slug,
  requestId,
  sessionSecondsLeft,
  onRestart,
}: {
  slug: string;
  requestId: string;
  sessionSecondsLeft: number;
  onRestart: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const start = useStartProofingYiviMutation(slug);
  const { mutate } = start;
  useEffect(() => {
    mutate(requestId);
  }, [mutate, requestId]);
  const disclosure = useProofingYiviDisclosureQuery(
    slug,
    requestId,
    start.isSuccess,
  );
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
      <FaceCheck
        slug={slug}
        requestId={requestId}
        stableFrames={disclosure.data.stableFrames}
      />
    );
  }
  if (start.data && secondsLeft === 0) {
    return <Expired onRestart={onRestart} />;
  }

  const ptr = start.data?.sessionPtr;
  return (
    <>
      <h2 className="text-ink text-[18px] font-bold">
        {t("customers.onScreen.scan.yiviHeading")}
      </h2>
      {ptr === undefined ? (
        <QrPlaceholder label={t("customers.onScreen.scan.starting")} />
      ) : (
        <QrCode value={yiviSessionQrPayload(ptr)} />
      )}
      <p className={`${HINT} text-center`}>
        {t("customers.onScreen.scan.yiviHint")}
      </p>
      {ptr !== undefined && (
        <a href={yiviSessionLink(ptr)} className={LINK_BUTTON}>
          {t("customers.onScreen.scan.openYivi")}
        </a>
      )}
      {start.data && <Countdown seconds={secondsLeft} />}
    </>
  );
}

function yiviEndReason(code: string | undefined, t: TFunction): string {
  switch (code) {
    case "cancelled":
      return t("customers.onScreen.yiviCodes.cancelled");
    case "timeout":
      return t("customers.onScreen.yiviCodes.timeout");
    case "invalid_proof":
      return t("customers.onScreen.yiviCodes.invalidProof");
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
  slug,
  requestId,
  stableFrames,
}: {
  slug: string;
  requestId: string;
  stableFrames?: number;
}): React.JSX.Element {
  const { t } = useTranslation();
  const videoRef = useRef<HTMLVideoElement>(null);
  const [cameraError, setCameraError] = useState(false);
  const [ready, setReady] = useState(false);
  const [verdict, setVerdict] = useState<ProofingFaceVerdict>();
  const [frameError, setFrameError] = useState<string>();
  const submit = useSubmitProofingFaceFrameMutation(slug, requestId);
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
  request,
  customer,
  onRestart,
}: {
  request: ProofingRequest;
  customer: ProofingCustomer;
  onRestart: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const actions = (
    <div className="flex flex-wrap justify-center gap-2">
      <Link
        to=".."
        relative="path"
        className="text-ink-soft self-center text-[13px] font-semibold hover:underline"
      >
        {t("customers.onScreen.outcome.back", { name: customer.name })}
      </Link>
      <Button variant="secondary" onClick={onRestart}>
        {t("customers.onScreen.outcome.again")}
      </Button>
    </div>
  );
  switch (request.status) {
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
            request.errorCode
              ? proofingRejectionReason(request.errorCode, t)
              : t("customers.onScreen.outcome.rejected.message")
          }
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

function Expired({ onRestart }: { onRestart: () => void }): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-4">
      <QrPlaceholder label={t("customers.onScreen.scan.expired")} expired />
      <Button variant="secondary" icon="scan_qrcode" onClick={onRestart}>
        {t("customers.onScreen.scan.restart")}
      </Button>
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
  onRestart: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-3 text-center">
      <p className={ERROR}>{message}</p>
      <Button variant="secondary" onClick={onRestart}>
        {actionLabel ?? t("customers.onScreen.scan.restart")}
      </Button>
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
