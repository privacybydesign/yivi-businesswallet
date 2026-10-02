import { useCallback, useEffect, useRef, useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useDeclineHostedProofingMutation,
  useHostedProofingQuery,
  useStartHostedProofingMutation,
} from "../api/identity-proofing.queries";
import type {
  HostedProofing,
  HostedStart,
  ProofingMethod,
} from "../api/identity-proofing";
import i18n from "../i18n";
import { errorCode } from "../lib/api-error";
import {
  completedMessage,
  completionRedirect,
  hostedLanguage,
  hostedTheme,
  postCompletion,
} from "../lib/hosted-completion";
import { applyOrgTheme, clearOrgTheme } from "../lib/theme";
import {
  isProofingLive,
  proofingErrorMessage,
  verifyStages,
} from "../lib/identity-proofing";
import { Button, Card, Stepper } from "../ui";
import { CustomerMark } from "./proofing-customer-ui";
import { MethodChoice, Overview, Session } from "./proofing-verify-steps";

const HINT = "text-ink-soft text-[13px]";
const NOT_FOUND = "session_not_found";

type Step = "overview" | "method" | "session";

// A customer's subject on their own device, from a hosted link: what the
// customer collects (declining cancels the link), the app to use, then that
// app's session. Public like /vog/:token; the token in the link is the whole
// authorization, and a link starts one session. Once settled, the page posts
// the outcome to an embedding page on one of the customer's origins, and
// redirects to the session's redirect when it has one.
export default function Proof(): React.JSX.Element {
  const { t } = useTranslation();
  // Guaranteed by the "/p/:token" route this mounts under.
  const token = useParams().token!;
  const page = useHostedProofingQuery(token);

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <div className="w-full max-w-xl">
        {page.isPending ? (
          <p className={HINT}>{t("common.loading")}</p>
        ) : page.isError ? (
          <Card className="p-6">
            <p role="alert" className="text-error text-[13.5px]">
              {errorCode(page.error) === NOT_FOUND
                ? t("proofLink.notFound")
                : proofingErrorMessage(page.error, t)}
            </p>
          </Card>
        ) : (
          <>
            <HostedFlow token={token} page={page.data} />
            {!page.data.customer.branding.hidePoweredBy && (
              <p className="text-muted mt-4 text-center text-[12px]">
                {t("proofLink.poweredBy")}
              </p>
            )}
          </>
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
  const choice = flow.yiviAvailable;
  const stages = verifyStages(choice, flow.diplomaMode === "required");
  const [inDiplomas, setInDiplomas] = useState(false);
  // A link started before (another tab, a reload) goes straight to its session.
  const [step, setStep] = useState<Step>(
    page.started || !isProofingLive(page.status) ? "session" : "overview",
  );
  const [method, setMethod] = useState<ProofingMethod>(
    page.method === "yivi_app" ? "yivi_app" : "idem_app",
  );
  const [started, setStarted] = useState<HostedStart>();
  const start = useStartHostedProofingMutation(token);
  const decline = useDeclineHostedProofingMutation(token);

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
  const settle = useCallback(
    (status: string) => {
      if (handedBack.current) {
        return;
      }
      handedBack.current = true;
      const message = completedMessage(page.sessionId, status);
      if (window.parent !== window) {
        postCompletion(window.parent, page.embedOrigins, message);
      }
      const to = page.redirectUrl
        ? completionRedirect(page.redirectUrl, message)
        : undefined;
      if (to) {
        setRedirecting(true);
        window.location.assign(to);
      }
    },
    [page.sessionId, page.embedOrigins, page.redirectUrl],
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
    ? proofingErrorMessage(start.error, t)
    : decline.isError
      ? proofingErrorMessage(decline.error, t)
      : undefined;
  return (
    <Card className="flex flex-col gap-6 p-6">
      <div className="flex items-center gap-3">
        <CustomerMark customer={customer} size="lg" />
        <span className="text-ink text-[16px] font-bold">{customer.name}</span>
      </div>
      <Stepper
        steps={stages.map((s) => t(`customers.onScreen.steps.${s}`))}
        current={stages.indexOf(inDiplomas ? "diplomas" : step)}
      />
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
              loading={decline.isPending}
              disabled={start.isPending}
              onClick={() =>
                decline.mutate(undefined, {
                  onSuccess: () => setStep("session"),
                })
              }
            >
              {t("proofLink.decline")}
            </Button>
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
          outcomeActions={
            redirecting ? (
              <p role="status" className={HINT}>
                {t("proofLink.redirecting")}
              </p>
            ) : undefined
          }
        />
      )}
    </Card>
  );
}
