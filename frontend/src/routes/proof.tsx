import { useCallback, useEffect, useRef, useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useDeclineHostedMutation,
  useHostedProofingQuery,
  useStartHostedMutation,
} from "../api/identity-proofing.queries";
import { hostedDataExportUrl, isDataRequest } from "../api/identity-proofing";
import type {
  FlowKind,
  HostedProofing,
  HostedStart,
  ProofingMethod,
  ProofingProgress,
} from "../api/identity-proofing";
import i18n from "../i18n";
import { errorCode } from "../lib/api-error";
import { useDateFormatter, useWhenFormatter } from "../lib/format-when";
import { erasureLeft, legalReasonIn } from "../lib/legal-retention";
import {
  canCloseDecline,
  checkEndReason,
  hostedHandBack,
  hostedLanguage,
  hostedTheme,
  postCompletion,
} from "../lib/hosted-completion";
import type { CheckEndReason } from "../lib/hosted-completion";
import { applyOrgTheme, clearOrgTheme } from "../lib/theme";
import {
  currentStage,
  dataRequestRefusal,
  hostedErrorMessage,
  isProofingLive,
  methodChoices,
  supportContactLink,
  verifyStages,
} from "../lib/identity-proofing";
import { Button, Card, ConfirmDialog, Stepper } from "../ui";
import { BrandMark } from "./proofing-ui";
import {
  CheckEnded,
  MethodChoice,
  Overview,
  Session,
  StepRegion,
} from "./proofing-verify-steps";

const HINT = "text-ink-soft text-[13px]";
const NOT_FOUND = "session_not_found";

type Step = "overview" | "method" | "session";

// An external person on their own device, from a hosted link: what the org
// collects (declining cancels the link), the app to use, then that app's
// session. Public like /vog/:token; the token in the link is the whole
// authorization, and a link starts one session. Once settled, the page posts
// the outcome to an embedding page on one of the org's origins, and redirects
// to the session's redirect when it has one. A session on a data request flow
// is the person asking the org for their data or its erasure: the page says
// so, and an approved "see my data" request's data downloads from it.
export default function Proof(): React.JSX.Element {
  const { t } = useTranslation();
  // Guaranteed by the "/p/:token" route this mounts under.
  const token = useParams().token!;
  const page = useHostedProofingQuery(token);

  return (
    // The card sits at the top, 80 px down, on every step, as VF1 to VF5
    // draw it; "Powered by Yivi" follows it.
    <div className="bg-surface-2 flex min-h-screen flex-col items-center px-4 pt-20 pb-10">
      <div className="flex w-full max-w-[560px] flex-col gap-5">
        {/* Data first: a background refetch that fails (the tab regaining
            focus on return from the Idem app) keeps the page it already has
            rather than replacing the session with an error card. */}
        {page.data !== undefined ? (
          <>
            <HostedFlow token={token} page={page.data} />
            {!page.data.organization.branding.hidePoweredBy && (
              <p className="text-muted text-center text-[12.5px]">
                {t("proofLink.poweredBy")}
              </p>
            )}
          </>
        ) : page.isError ? (
          <Card className="p-6">
            <p role="alert" className="text-error text-[13.5px]">
              {errorCode(page.error) === NOT_FOUND
                ? t("proofLink.notFound")
                : hostedErrorMessage(page.error, t)}
            </p>
          </Card>
        ) : (
          <p className={HINT}>{t("common.loading")}</p>
        )}
      </div>
    </div>
  );
}

// The step a hosted page opens on: a link started before (another tab, a
// reload) goes straight to its session.
function initialStep(page: HostedProofing): Step {
  if (page.started || !isProofingLive(page.status)) {
    return "session";
  }
  return "overview";
}

function HostedFlow({
  token,
  page,
}: Readonly<{
  token: string;
  page: HostedProofing;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const { organization, flow } = page;
  const dataRequest = isDataRequest(flow.kind);
  const stages = verifyStages({
    address: flow.addressMode === "required",
    diplomas: flow.diplomaMode === "required",
  });
  const [inAddress, setInAddress] = useState(false);
  const [inDiplomas, setInDiplomas] = useState(false);
  const [step, setStep] = useState<Step>(initialStep(page));
  const [method, setMethod] = useState<ProofingMethod>(
    page.method === "yivi_app" ? "yivi_app" : "idem_app",
  );
  const [started, setStarted] = useState<HostedStart>();
  const [sessionEnded, setSessionEnded] = useState(false);
  const onEnded = useCallback(() => setSessionEnded(true), []);
  const start = useStartHostedMutation(token);
  const decline = useDeclineHostedMutation(token);
  const [confirmDecline, setConfirmDecline] = useState(false);

  useEffect(() => {
    const preferred =
      typeof navigator === "undefined"
        ? []
        : (navigator.languages ?? [navigator.language]);
    // Not changeLanguage: the subject's page must not persist a wallet choice.
    void i18n.changeLanguage(
      hostedLanguage(page.language, preferred, page.locales),
    );
  }, [page.language, page.locales]);

  // The session's look for as long as the page shows.
  const { primaryColor } = organization.branding;
  const { theme } = page;
  useEffect(() => {
    applyOrgTheme(hostedTheme(theme, primaryColor));
    return clearOrgTheme;
  }, [theme, primaryColor]);

  // Handed back once, however often the outcome is rendered.
  const handedBack = useRef(false);
  const [redirecting, setRedirecting] = useState(false);
  // The latest outcome the session reported: the page's own data is as it
  // loaded, before the session settled.
  const [settled, setSettled] = useState<ProofingProgress>();
  const settle = useCallback(
    (progress: ProofingProgress) => {
      setSettled(progress);
      const handBack = hostedHandBack({
        sessionId: page.sessionId,
        status: progress.status,
        handedBack: handedBack.current,
        dataRequest,
        framed: window.parent !== window,
        redirectUrl: page.redirectUrl,
      });
      if (!handBack) {
        return;
      }
      handedBack.current = true;
      if (handBack.post) {
        postCompletion(window.parent, page.embedOrigins, handBack.message);
      }
      if (handBack.redirectTo) {
        setRedirecting(true);
        // Replace: back from the org's page must not land on this link,
        // which would redirect forward again.
        window.location.replace(handBack.redirectTo);
      }
    },
    [page.sessionId, page.embedOrigins, page.redirectUrl, dataRequest],
  );

  function begin(picked: ProofingMethod): void {
    start.mutate(picked, {
      onSuccess: (next) => {
        setStarted(next);
        setStep("session");
      },
    });
  }

  const actionError = start.isError
    ? hostedErrorMessage(start.error, t)
    : undefined;
  const stage = currentStage({ step, inAddress, inDiplomas });
  const showsEnded =
    step === "session" && (sessionEnded || page.status === "expired");
  let outcomeActions: React.ReactNode;
  if (redirecting) {
    outcomeActions = (
      <output className={`${HINT} block`}>{t("proofLink.redirecting")}</output>
    );
  } else if (dataRequest) {
    outcomeActions = (
      <DataRequestOutcome
        token={token}
        organization={organization.name}
        kind={flow.kind as DataKind}
        progress={settled ?? page}
      />
    );
  }
  return (
    <Card className="flex flex-col overflow-hidden">
      {/* The theme's header colour, as the top of the Idem app's screens. */}
      <div className="bg-topbar text-topbar-fg border-topbar-line flex items-center gap-3 border-b px-6 py-5">
        <BrandMark
          name={organization.name}
          branding={organization.branding}
          size="lg"
        />
        <span className="font-display text-[16px] font-bold">
          {organization.name}
        </span>
      </div>
      <div className="flex flex-col gap-6 p-6">
        {dataRequest && (
          <p className="text-ink text-[13.5px]">
            {t(`proofLink.dataRequest.${flow.kind as DataKind}.intro`, {
              organization: organization.name,
            })}
          </p>
        )}
        {!showsEnded && (
          <Stepper
            steps={stages.map((s) => t(`identityProofing.onScreen.steps.${s}`))}
            current={stages.indexOf(stage)}
          />
        )}
        <StepRegion step={stage} headingLevel="h1">
          {step === "overview" && (
            <Overview
              org={organization}
              flow={flow}
              // Opening the link or pressing Continue uses nothing up: the
              // link starts when the person taps an app (#304).
              onContinue={() => setStep("method")}
              cancel={
                <Button
                  variant="secondary"
                  size="lg"
                  disabled={start.isPending}
                  onClick={() => setConfirmDecline(true)}
                >
                  {t("proofLink.decline")}
                </Button>
              }
            />
          )}
          {confirmDecline && (
            <DeclineConfirm
              organization={organization.name}
              decline={decline}
              onClose={() => setConfirmDecline(false)}
              onDeclined={() => {
                setConfirmDecline(false);
                setStep("session");
              }}
            />
          )}
          {step === "method" && (
            <MethodChoice
              methods={methodChoices(flow.yiviAvailable)}
              picked={method}
              onChoose={(picked) => {
                setMethod(picked);
                begin(picked);
              }}
              onBack={() => setStep("overview")}
              starting={start.isPending}
              error={actionError}
            />
          )}
          {step === "session" && (
            <Session
              target={{ kind: "hosted", token }}
              initial={started ?? page}
              deepLink={started?.deepLink}
              method={method}
              diplomaMode={flow.diplomaMode}
              diplomas={page.diplomas}
              onDiplomaStep={setInDiplomas}
              addressMode={flow.addressMode}
              onAddressStep={setInAddress}
              renderEnded={(progress) => (
                <HostedCheckEnded
                  name={organization.name}
                  contact={organization.branding.supportContact}
                  reason={checkEndReason(progress)}
                />
              )}
              onEnded={onEnded}
              onSettled={settle}
              dataRequest={dataRequest}
              outcomeActions={outcomeActions}
            />
          )}
        </StepRegion>
      </div>
    </Card>
  );
}

type DataKind = Exclude<FlowKind, "identity">;

// Asks the person to confirm declining, which cancels the link. It cannot be
// closed while the decline runs.
function DeclineConfirm({
  organization,
  decline,
  onClose,
  onDeclined,
}: Readonly<{
  organization: string;
  decline: ReturnType<typeof useDeclineHostedMutation>;
  onClose: () => void;
  onDeclined: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <ConfirmDialog
      title={t("proofLink.declineConfirm.title")}
      message={t("proofLink.declineConfirm.message", { organization })}
      confirmLabel={t("proofLink.declineConfirm.confirm")}
      cancelLabel={t("proofLink.declineConfirm.back")}
      busy={decline.isPending}
      error={decline.isError ? hostedErrorMessage(decline.error, t) : undefined}
      onClose={() => {
        // Closing resets the mutation, which drops the onSuccess below that
        // moves the page on, though the server cancels the link all the same.
        if (!canCloseDecline(decline.status)) {
          return;
        }

        onClose();
        decline.reset();
      }}
      onConfirm={() => decline.mutate(undefined, { onSuccess: onDeclined })}
    />
  );
}

// What the person sees once the check ran out (VF5): nothing was shared, and
// who to ask for a new link. It stays on screen: a check that ran out is not
// handed back by redirect (hostedHandBack).
function HostedCheckEnded({
  name,
  contact,
  reason,
}: Readonly<{
  name: string;
  contact: string;
  reason: CheckEndReason;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const link = supportContactLink(contact);
  // The page can be framed, and a plain link would replace it inside the frame.
  const newTab = link?.kind === "web";
  let message: string;
  if (reason.kind === "notStarted") {
    message = t("proofLink.ended.notStarted", { name });
  } else if (reason.minutes === undefined) {
    message = t("proofLink.ended.timedOut", { name });
  } else {
    message = t("proofLink.ended.started", { name, count: reason.minutes });
  }
  return (
    <CheckEnded message={message}>
      <div className="rounded-yivi bg-surface-2 flex flex-col gap-2 p-4">
        <span className="text-ink font-semibold">
          {t("proofLink.ended.retryTitle")}
        </span>
        <span className={`${HINT} text-[14px]`}>
          {t("proofLink.ended.retryHint", { name })}
        </span>
        {link !== undefined ? (
          <a
            href={link.href}
            target={newTab ? "_blank" : undefined}
            rel={newTab ? "noreferrer" : undefined}
            className="text-link font-semibold hover:underline"
          >
            {contact}
          </a>
        ) : (
          contact !== "" && (
            <span className="text-ink font-semibold">{contact}</span>
          )
        )}
      </div>
    </CheckEnded>
  );
}

// What a data request's person sees once their identity check settled: that
// the request awaits the org's review, its outcome, and an approved "see
// my data" request's download while it lasts. An erasure that kept sessions
// says so, what the law keeps until when and why among them (GDPR Art. 12(4)),
// and a refused request says why; one the law declined says it in its
// outcome (LegalRetentionOutcome).
function DataRequestOutcome({
  token,
  organization,
  kind,
  progress,
}: Readonly<{
  token: string;
  organization: string;
  kind: DataKind;
  progress: ProofingProgress;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const formatDate = useDateFormatter();
  const {
    status,
    errorCode,
    dataExportUntil: exportUntil,
    dataMatchesKept: kept,
    legalRetention: legal,
  } = progress;
  switch (status) {
    case "needs_review":
      return (
        <p role="status" className={HINT}>
          {t(`proofLink.dataRequest.${kind}.inReview`, { organization })}
        </p>
      );
    case "approved":
      if (kind === "data_erasure") {
        const { unsure, held } = erasureLeft(kept, legal);
        return (
          <output className="flex flex-col items-center gap-2">
            <span className={HINT}>
              {unsure > 0
                ? t("proofLink.dataRequest.erasedPartly", {
                    count: unsure,
                    organization,
                  })
                : t("proofLink.dataRequest.erased")}
            </span>
            {held && (
              <>
                <span className={HINT}>
                  {t("proofLink.dataRequest.keptByLaw", {
                    count: held.sessions,
                    organization,
                    date: formatDate(held.until),
                  })}
                </span>
                <span className={HINT}>
                  {legalReasonIn(held.reason, i18n.language)}
                </span>
              </>
            )}
          </output>
        );
      }
      return exportUntil ? (
        <div role="status" className="flex flex-col items-center gap-2">
          <a
            href={hostedDataExportUrl(token)}
            download
            className="bg-ink text-surface inline-flex h-9 items-center rounded-md px-3.5 text-[13.5px] font-semibold transition-opacity hover:opacity-90"
          >
            {t("proofLink.dataRequest.download")}
          </a>
          <p role="status" className={HINT}>
            {t("proofLink.dataRequest.downloadUntil", {
              date: formatWhen(exportUntil),
            })}
          </p>
        </div>
      ) : (
        <p role="status" className={HINT}>
          {t("proofLink.dataRequest.downloadExpired")}
        </p>
      );
    case "rejected": {
      const why = dataRequestRefusal(errorCode);
      // The outcome itself gives the law's reason and date.
      if (legal) return null;
      return why === undefined ? null : (
        <p role="status" className={HINT}>
          {t(`proofLink.dataRequest.refused.${why}`, { organization })}
        </p>
      );
    }
    default:
      return null;
  }
}
