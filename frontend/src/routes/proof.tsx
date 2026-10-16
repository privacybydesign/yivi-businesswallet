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
import { useWhenFormatter } from "../lib/format-when";
import {
  hostedHandBack,
  hostedLanguage,
  hostedTheme,
  postCompletion,
} from "../lib/hosted-completion";
import { applyOrgTheme, clearOrgTheme } from "../lib/theme";
import {
  dataRequestRefusal,
  hostedErrorMessage,
  isProofingLive,
  verifyStages,
} from "../lib/identity-proofing";
import { Button, Card, ConfirmDialog, Stepper } from "../ui";
import { CustomerMark } from "./proofing-customer-ui";
import {
  MethodChoice,
  Overview,
  Session,
  StepRegion,
} from "./proofing-verify-steps";

const HINT = "text-ink-soft text-[13px]";
const NOT_FOUND = "session_not_found";

type Step = "overview" | "method" | "session";

// A customer's subject on their own device, from a hosted link: what the
// customer collects (declining cancels the link), the app to use, then that
// app's session. Public like /vog/:token; the token in the link is the whole
// authorization, and a link starts one session. Once settled, the page posts
// the outcome to an embedding page on one of the customer's origins, and
// redirects to the session's redirect when it has one. A session on a data
// request flow is the person asking the customer for their data or its
// erasure: the page says so, and an approved "see my data" request's data
// downloads from it.
export default function Proof(): React.JSX.Element {
  const { t } = useTranslation();
  // Guaranteed by the "/p/:token" route this mounts under.
  const token = useParams().token!;
  const page = useHostedProofingQuery(token);

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <div className="w-full max-w-xl">
        {/* Data first: a background refetch that fails (the tab regaining
            focus on return from the Idem app) keeps the page it already has
            rather than replacing the session with an error card. */}
        {page.data !== undefined ? (
          <>
            <HostedFlow token={token} page={page.data} />
            {!page.data.customer.branding.hidePoweredBy && (
              <p className="text-muted mt-4 text-center text-[12px]">
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

function HostedFlow({
  token,
  page,
}: {
  token: string;
  page: HostedProofing;
}): React.JSX.Element {
  const { t } = useTranslation();
  const { customer, flow } = page;
  const dataRequest = isDataRequest(flow.kind);
  const choice = flow.yiviAvailable;
  const stages = verifyStages({
    appChoice: choice,
    diplomas: flow.diplomaMode === "required",
  });
  const [inDiplomas, setInDiplomas] = useState(false);
  // A link started before (another tab, a reload) goes straight to its session.
  const [step, setStep] = useState<Step>(
    page.started || !isProofingLive(page.status) ? "session" : "overview",
  );
  const [method, setMethod] = useState<ProofingMethod>(
    page.method === "yivi_app" ? "yivi_app" : "idem_app",
  );
  const [started, setStarted] = useState<HostedStart>();
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

  // The customer's colour for as long as the page shows.
  const { primaryColor } = customer.branding;
  useEffect(() => {
    applyOrgTheme(hostedTheme(primaryColor));
    return clearOrgTheme;
  }, [primaryColor]);

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
        // Replace: back from the customer's page must not land on this link,
        // which would redirect forward again.
        window.location.replace(handBack.redirectTo);
      }
    },
    [page.sessionId, page.embedOrigins, page.redirectUrl, dataRequest],
  );

  function begin(): void {
    start.mutate(method, {
      onSuccess: (next) => {
        setStarted(next);
        setStep("session");
      },
    });
  }

  const actionError = start.isError
    ? hostedErrorMessage(start.error, t)
    : undefined;
  return (
    <Card className="flex flex-col gap-6 p-6">
      <div className="flex items-center gap-3">
        <CustomerMark customer={customer} size="lg" />
        <span className="text-ink text-[16px] font-bold">{customer.name}</span>
      </div>
      {dataRequest && (
        <p className="text-ink text-[13.5px]">
          {t(`proofLink.dataRequest.${flow.kind as DataKind}.intro`, {
            customer: customer.name,
          })}
        </p>
      )}
      <Stepper
        steps={stages.map((s) => t(`customers.onScreen.steps.${s}`))}
        current={stages.indexOf(inDiplomas ? "diplomas" : step)}
      />
      <StepRegion step={inDiplomas ? "diplomas" : step}>
        {step === "overview" && (
          <Overview
            customer={customer}
            flow={flow}
            onContinue={choice ? () => setStep("method") : begin}
            starting={start.isPending}
            error={actionError}
            cancel={
              <Button
                variant="secondary"
                disabled={start.isPending}
                onClick={() => setConfirmDecline(true)}
              >
                {t("proofLink.decline")}
              </Button>
            }
          />
        )}
        {confirmDecline && (
          <ConfirmDialog
            title={t("proofLink.declineConfirm.title")}
            message={t("proofLink.declineConfirm.message", {
              customer: customer.name,
            })}
            confirmLabel={t("proofLink.declineConfirm.confirm")}
            busy={decline.isPending}
            error={
              decline.isError ? hostedErrorMessage(decline.error, t) : undefined
            }
            onClose={() => {
              setConfirmDecline(false);
              decline.reset();
            }}
            onConfirm={() =>
              decline.mutate(undefined, {
                onSuccess: () => {
                  setConfirmDecline(false);
                  setStep("session");
                },
              })
            }
          />
        )}
        {step === "method" && (
          <MethodChoice
            method={method}
            onChange={setMethod}
            onBack={() => setStep("overview")}
            onContinue={begin}
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
            onSettled={settle}
            dataRequest={dataRequest}
            outcomeActions={
              redirecting ? (
                <p role="status" className={HINT}>
                  {t("proofLink.redirecting")}
                </p>
              ) : dataRequest ? (
                <DataRequestOutcome
                  token={token}
                  customer={customer.name}
                  kind={flow.kind as DataKind}
                  progress={settled ?? page}
                />
              ) : undefined
            }
          />
        )}
      </StepRegion>
    </Card>
  );
}

type DataKind = Exclude<FlowKind, "identity">;

// What a data request's person sees once their identity check settled: that
// the request awaits the customer's review, its outcome, and an approved "see
// my data" request's download while it lasts. An erasure that kept sessions
// says so, and a refused request says why.
function DataRequestOutcome({
  token,
  customer,
  kind,
  progress,
}: {
  token: string;
  customer: string;
  kind: DataKind;
  progress: ProofingProgress;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const {
    status,
    errorCode,
    dataExportUntil: exportUntil,
    dataMatchesKept: kept,
  } = progress;
  switch (status) {
    case "needs_review":
      return (
        <p role="status" className={HINT}>
          {t(`proofLink.dataRequest.${kind}.inReview`, { customer })}
        </p>
      );
    case "approved":
      if (kind === "data_erasure") {
        return (
          <p role="status" className={HINT}>
            {kept
              ? t("proofLink.dataRequest.erasedPartly", {
                  count: kept,
                  customer,
                })
              : t("proofLink.dataRequest.erased")}
          </p>
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
      return why === undefined ? null : (
        <p role="status" className={HINT}>
          {t(`proofLink.dataRequest.refused.${why}`, { customer })}
        </p>
      );
    }
    default:
      return null;
  }
}
