import { useEffect, useState } from "react";
import {
  Link,
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useCreateRequestMutation,
  useDataRequestFlowsQuery,
  useProofingFlowsQuery,
  useProofingSettingsQuery,
} from "../api/identity-proofing.queries";
import type {
  ProofingFlow,
  ProofingMethod,
  ProofingSent,
  ProofingSettings,
} from "../api/identity-proofing";
import {
  currentStage,
  dataRequestKindOf,
  methodChoices,
  proofingErrorMessage,
  sendKindFlows,
  stageIndex,
  STEP_DOCUMENT_PHOTO,
  verifyStages,
  firstMethod,
  yiviAppAvailable,
  idemAppAvailable,
  acceptsPersonalData,
  stepAsked,
} from "../lib/identity-proofing";
import { blockingError } from "../lib/query-state";
import { Button, Card, Stepper, TopBar } from "../ui";
import { BrandMark } from "./proofing-ui";
import {
  CheckEnded,
  DOCUMENT_SCAN_WIDE_PAGE,
  Overview,
  Session,
} from "./proofing-verify-steps";
import type { VerifyOrg } from "./proofing-verify-steps";

const HINT = "text-ink-soft text-[13px]";
const ERROR = "text-error text-[12.5px]";

type Step = "overview" | "session";

// What the send form hands over: the subject as the sender typed it. Kept out
// of the URL, and taken out of the history entry once read (the browser keeps
// history state, the birth date included, after the page is gone), so a reload
// runs the flow without them.
interface VerifyState {
  name?: string;
  email?: string;
  birthDate?: string;
}

function verifyState(state: unknown): VerifyState {
  if (typeof state !== "object" || state === null) return {};
  const { name, email, birthDate } = state as Record<string, unknown>;
  return {
    name: typeof name === "string" ? name : undefined,
    email: typeof email === "string" ? email : undefined,
    birthDate: typeof birthDate === "string" ? birthDate : undefined,
  };
}

// An external person verified on the sender's screen: what the org collects,
// the app they pick, then that app's QR code for as long as the session runs.
// Nothing is mailed; the session starts when the app is picked. ?flow= is the
// flow it runs, or ?kind= a data request, on the org's own flow of that kind.
export default function ProofingVerify(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug/…/verify" route this mounts under.
  const slug = orgSlug!;
  const [searchParams] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const [subject] = useState(() => verifyState(location.state));
  const hasState = location.state !== null;
  useEffect(() => {
    if (!hasState) return;
    void navigate(
      { pathname: location.pathname, search: location.search },
      { replace: true, state: null },
    );
  }, [hasState, navigate, location.pathname, location.search]);
  const org = useOrganizationQuery(slug);
  const settings = useProofingSettingsQuery(slug);
  const dataRequest = dataRequestKindOf(searchParams.get("kind"));
  const identityFlows = useProofingFlowsQuery(slug, dataRequest === undefined);
  const dataFlows = useDataRequestFlowsQuery(slug, dataRequest !== undefined);
  const flows = dataRequest === undefined ? identityFlows : dataFlows;
  const { sendable, initial } = sendKindFlows(
    dataRequest ?? "identity",
    identityFlows.data ?? [],
    dataFlows.data,
  );
  const flowId =
    dataRequest === undefined
      ? (searchParams.get("flow") ?? initial?.id)
      : initial?.id;
  const found = sendable.find((f) => f.id === flowId);
  // A data request's flow is named as the person asked for it.
  const flow =
    found && dataRequest
      ? {
          ...found,
          name: t(`identityProofing.send.what.${dataRequest}.title`),
        }
      : found;

  const error = blockingError(settings, flows, org);
  let body: React.JSX.Element;
  if (error !== null) {
    body = <p className={ERROR}>{proofingErrorMessage(error, t)}</p>;
  } else if (
    settings.data === undefined ||
    flows.data === undefined ||
    org.data === undefined
  ) {
    body = <p className={HINT}>{t("common.loading")}</p>;
  } else if (flow === undefined) {
    body = (
      <p className={ERROR}>{t("identityProofing.onScreen.missingFlow")}</p>
    );
  } else {
    body = (
      <VerifyFlow
        slug={slug}
        org={{ name: org.data.name, branding: settings.data.branding }}
        settings={settings.data}
        flow={flow}
        subject={subject}
      />
    );
  }

  return (
    <>
      <TopBar title={t("identityProofing.onScreen.title")} />
      <div
        className={`mx-auto w-full max-w-xl px-4 py-6 ${DOCUMENT_SCAN_WIDE_PAGE}`}
      >
        {body}
      </div>
    </>
  );
}

function VerifyFlow({
  slug,
  org,
  settings,
  flow,
  subject,
}: Readonly<{
  slug: string;
  org: VerifyOrg;
  settings: ProofingSettings;
  flow: ProofingFlow;
  subject: VerifyState;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>("overview");
  const methods = methodChoices(yiviAppAvailable(flow), idemAppAvailable(flow));
  const browserOnly = flow.browserOnly ?? false;
  // The app Continue will start in, so the stepper is right before it.
  const [method, setMethod] = useState<ProofingMethod>(() =>
    firstMethod(browserOnly, methods),
  );
  // Only a choice of apps is a step: one app starts at once, none is the
  // browser alone.
  const stages = verifyStages({
    app: !browserOnly && methods.length > 1,
    document: flow.steps.includes(STEP_DOCUMENT_PHOTO),
    // The Yivi app shares the registered address: the person enters none.
    address: stepAsked(flow.addressMode) && method !== "yivi_app",
    diplomas: stepAsked(flow.diplomaMode),
    employment: stepAsked(flow.employmentMode),
  });
  const [sessionStage, setSessionStage] = useState<
    "document" | "method" | "session" | "done"
  >("session");
  const [inAddress, setInAddress] = useState(false);
  const [inDiplomas, setInDiplomas] = useState(false);
  const [inEmployment, setInEmployment] = useState(false);
  const [sent, setSent] = useState<ProofingSent>();
  const create = useCreateRequestMutation(slug);
  const name = org.branding.displayName || org.name;

  function start(picked: ProofingMethod): void {
    create.mutate(
      {
        email: subject.email ?? "",
        name: subject.name ?? "",
        birthDate: subject.birthDate,
        flowId: flow.id,
        method: picked,
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
    setStep("overview");
  }

  return (
    <Card className="flex flex-col gap-6 p-6">
      <div className="flex items-center gap-3">
        <BrandMark name={org.name} branding={org.branding} size="lg" />
        <span className="text-ink text-[16px] font-bold">{name}</span>
      </div>
      <Stepper
        steps={stages.map((s) => t(`identityProofing.onScreen.steps.${s}`))}
        current={stageIndex(
          stages,
          currentStage({
            step: step === "session" ? sessionStage : step,
            inAddress,
            inDiplomas,
            inEmployment,
          }),
        )}
      />
      {step === "overview" && (
        <Overview
          org={org}
          flow={{
            ...flow,
            retentionDays: flow.retentionDays ?? settings.dataRetentionDays,
          }}
          onlyApp={
            !browserOnly && methods.length === 1 ? methods[0] : undefined
          }
          // Continue starts the session; with a choice of apps, the app is
          // chosen in it.
          onContinue={() => {
            const first = firstMethod(browserOnly, methods);
            setMethod(first);
            start(first);
          }}
          starting={create.isPending}
          error={
            create.isError ? proofingErrorMessage(create.error, t) : undefined
          }
          cancel={
            <Link
              to=".."
              relative="path"
              className="rounded-button font-button text-ink hover:bg-surface-3 inline-flex h-11 items-center px-[18px] text-[15px] font-semibold"
            >
              {t("identityProofing.onScreen.overview.cancel")}
            </Link>
          }
        />
      )}
      {step === "session" && sent && (
        <Session
          target={{ kind: "request", slug, requestId: sent.id }}
          initial={sent}
          deepLink={sent.deepLink}
          deepLinkExpiresAt={sent.deepLinkExpiresAt}
          method={method}
          methods={methods}
          yiviPersonalData={acceptsPersonalData(flow)}
          onStage={setSessionStage}
          onMethod={setMethod}
          diplomaMode={sent.diplomaMode}
          diplomas={sent.diplomas}
          onDiplomaStep={setInDiplomas}
          addressMode={sent.addressMode}
          onAddressStep={setInAddress}
          employmentMode={sent.employmentMode}
          onEmploymentStep={setInEmployment}
          onRestart={restart}
          renderEnded={() => (
            <CheckEnded message={t("identityProofing.onScreen.ended.message")}>
              <div>
                <Button
                  variant="secondary"
                  size="lg"
                  icon="scan_qrcode"
                  onClick={restart}
                >
                  {t("identityProofing.onScreen.scan.restart")}
                </Button>
              </div>
            </CheckEnded>
          )}
          outcomeActions={
            <div className="flex flex-wrap justify-center gap-2">
              <Link
                to=".."
                relative="path"
                className="text-ink-soft self-center text-[13px] font-semibold hover:underline"
              >
                {t("identityProofing.onScreen.outcome.back")}
              </Link>
              <Button variant="secondary" onClick={restart}>
                {t("identityProofing.onScreen.outcome.again")}
              </Button>
            </div>
          }
        />
      )}
    </Card>
  );
}
