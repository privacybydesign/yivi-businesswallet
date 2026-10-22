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
  verifyStages,
  yiviAppAvailable,
} from "../lib/identity-proofing";
import { Button, Card, Stepper, TopBar } from "../ui";
import { BrandMark } from "./proofing-ui";
import {
  CheckEnded,
  MethodChoice,
  Overview,
  Session,
} from "./proofing-verify-steps";
import type { VerifyOrg } from "./proofing-verify-steps";

const HINT = "text-ink-soft text-[13px]";
const ERROR = "text-error text-[12.5px]";

type Step = "overview" | "method" | "session";

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

  let body: React.JSX.Element;
  if (settings.isPending || flows.isPending || org.isPending) {
    body = <p className={HINT}>{t("common.loading")}</p>;
  } else if (settings.isError || flows.isError || org.isError) {
    body = (
      <p className={ERROR}>
        {proofingErrorMessage(settings.error ?? flows.error ?? org.error, t)}
      </p>
    );
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
      <div className="mx-auto w-full max-w-xl px-4 py-6">{body}</div>
    </>
  );
}

function VerifyFlow({
  slug,
  org,
  settings,
  flow,
  subject,
}: {
  slug: string;
  org: VerifyOrg;
  settings: ProofingSettings;
  flow: ProofingFlow;
  subject: VerifyState;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>("overview");
  const [method, setMethod] = useState<ProofingMethod>("idem_app");
  // A flow the Yivi app cannot run leaves the Idem app only, still tapped on
  // the app step as on a hosted link.
  const methods = methodChoices(yiviAppAvailable(flow));
  const stages = verifyStages({
    address: flow.addressMode === "required",
    diplomas: flow.diplomaMode === "required",
  });
  const [inAddress, setInAddress] = useState(false);
  const [inDiplomas, setInDiplomas] = useState(false);
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
    setStep("method");
  }

  return (
    <Card className="flex flex-col gap-6 p-6">
      <div className="flex items-center gap-3">
        <BrandMark name={org.name} branding={org.branding} size="lg" />
        <span className="text-ink text-[16px] font-bold">{name}</span>
      </div>
      <Stepper
        steps={stages.map((s) => t(`identityProofing.onScreen.steps.${s}`))}
        current={stages.indexOf(currentStage({ step, inAddress, inDiplomas }))}
      />
      {step === "overview" && (
        <Overview
          org={org}
          flow={{
            ...flow,
            retentionDays: flow.retentionDays ?? settings.dataRetentionDays,
          }}
          onContinue={() => setStep("method")}
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
      {step === "method" && (
        <MethodChoice
          methods={methods}
          picked={method}
          onChoose={(picked) => {
            setMethod(picked);
            start(picked);
          }}
          onBack={() => setStep("overview")}
          starting={create.isPending}
          error={
            create.isError ? proofingErrorMessage(create.error, t) : undefined
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
          diplomaMode={sent.diplomaMode}
          diplomas={sent.diplomas}
          onDiplomaStep={setInDiplomas}
          addressMode={sent.addressMode}
          onAddressStep={setInAddress}
          onRestart={restart}
          ended={() => (
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
