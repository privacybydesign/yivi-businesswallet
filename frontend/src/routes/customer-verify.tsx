import { useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateProofingRequestMutation,
  useProofingCustomerFlowsQuery,
  useProofingCustomerQuery,
} from "../api/identity-proofing.queries";
import type {
  ProofingCustomer,
  ProofingCustomerFlow,
  ProofingMethod,
  ProofingSent,
} from "../api/identity-proofing";
import {
  proofingErrorMessage,
  yiviAppAvailable,
} from "../lib/identity-proofing";
import { Button, Card, Stepper, TopBar } from "../ui";
import { CustomerMark } from "./proofing-customer-ui";
import { MethodChoice, Overview, Session } from "./proofing-verify-steps";

const HINT = "text-ink-soft text-[13px]";
const ERROR = "text-error text-[12.5px]";

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
  // A flow the Yivi app cannot run leaves the Idem app only: no choice to make.
  const choice = yiviAppAvailable(flow);
  const steps = choice ? STEPS : STEPS.filter((s) => s !== "method");
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
    setStep(choice ? "method" : "overview");
  }

  return (
    <Card className="flex flex-col gap-6 p-6">
      <div className="flex items-center gap-3">
        <CustomerMark customer={customer} size="lg" />
        <span className="text-ink text-[16px] font-bold">{name}</span>
      </div>
      <Stepper
        steps={steps.map((s) => t(`customers.onScreen.steps.${s}`))}
        current={steps.indexOf(step)}
      />
      {step === "overview" && (
        <Overview
          customer={customer}
          flow={flow}
          onContinue={choice ? () => setStep("method") : start}
          starting={create.isPending}
          error={
            create.isError ? proofingErrorMessage(create.error, t) : undefined
          }
          cancel={
            <Link
              to=".."
              relative="path"
              className="text-ink-soft self-center text-[13px] font-semibold hover:underline"
            >
              {t("customers.onScreen.overview.cancel")}
            </Link>
          }
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
          target={{ kind: "request", slug, requestId: sent.id }}
          initial={sent}
          deepLink={sent.deepLink}
          method={method}
          onRestart={restart}
          outcomeActions={
            <div className="flex flex-wrap justify-center gap-2">
              <Link
                to=".."
                relative="path"
                className="text-ink-soft self-center text-[13px] font-semibold hover:underline"
              >
                {t("customers.onScreen.outcome.back", { name: customer.name })}
              </Link>
              <Button variant="secondary" onClick={restart}>
                {t("customers.onScreen.outcome.again")}
              </Button>
            </div>
          }
        />
      )}
    </Card>
  );
}
