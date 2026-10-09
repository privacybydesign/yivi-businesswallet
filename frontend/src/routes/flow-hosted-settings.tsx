import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useProofingCustomerFlowsQuery,
  useProofingCustomersQuery,
  useProofingFlowHostedQuery,
  useSaveFlowHostedMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingFlow,
  ProofingFlowHosted,
} from "../api/identity-proofing";
import { SUPPORTED_LANGUAGES } from "../i18n";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { Button, Card } from "../ui";
import { CustomerMark } from "./proofing-customer-ui";
import { Overview } from "./proofing-verify-steps";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full max-w-xs border px-3 py-2 text-[13.5px] outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-10";
const COMPLETIONS: readonly ProofingFlowHosted["completion"][] = [
  "redirect",
  "done",
];

// A flow's hosted page: whether links may be made for it, the languages it
// offers, how it ends, and a preview of its first step in a customer's
// branding.
export function FlowHostedSettings({
  slug,
  flow,
}: {
  slug: string;
  flow: ProofingFlow;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useProofingFlowHostedQuery(slug, flow.id);

  if (settings.isPending) {
    return <p className={HINT}>{t("common.loading")}</p>;
  }
  if (settings.isError) {
    return <p className={ERROR}>{proofingErrorMessage(settings.error, t)}</p>;
  }
  return <HostedForm slug={slug} flow={flow} saved={settings.data} />;
}

function HostedForm({
  slug,
  flow,
  saved,
}: {
  slug: string;
  flow: ProofingFlow;
  saved: ProofingFlowHosted;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveFlowHostedMutation(slug, flow.id);
  const [draft, setDraft] = useState(saved);
  const dirty =
    draft.enabled !== saved.enabled ||
    draft.completion !== saved.completion ||
    draft.locales.join() !== saved.locales.join();

  function toggleLocale(locale: string, on: boolean): void {
    setDraft((current) => ({
      ...current,
      // Kept in the supported order; none ticked offers every language.
      locales: SUPPORTED_LANGUAGES.filter((l) =>
        l === locale ? on : current.locales.includes(l),
      ),
    }));
  }

  return (
    <div className="border-line bg-surface-2 flex flex-col gap-4 rounded-lg border p-4">
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          save.mutate(draft);
        }}
      >
        <label className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            className="h-4 w-4"
            checked={draft.enabled}
            onChange={(event) =>
              setDraft({ ...draft, enabled: event.target.checked })
            }
          />
          {t("identityProofingFlows.hosted.enabled")}
        </label>
        <fieldset className="flex flex-col gap-1.5">
          <legend className={LABEL}>
            {t("identityProofingFlows.hosted.locales")}
          </legend>
          <div className="flex gap-4">
            {SUPPORTED_LANGUAGES.map((locale) => (
              <label
                key={locale}
                className="flex items-center gap-2 text-[13px]"
              >
                <input
                  type="checkbox"
                  className="h-4 w-4"
                  checked={draft.locales.includes(locale)}
                  onChange={(event) =>
                    toggleLocale(locale, event.target.checked)
                  }
                />
                {t(`common.languageName.${locale}`)}
              </label>
            ))}
          </div>
          <p className={HINT}>
            {t("identityProofingFlows.hosted.localesHint")}
          </p>
        </fieldset>
        <fieldset className="flex flex-col gap-1.5">
          <legend className={LABEL}>
            {t("identityProofingFlows.hosted.completion")}
          </legend>
          {COMPLETIONS.map((completion) => (
            <label
              key={completion}
              className="flex items-center gap-2 text-[13px]"
            >
              <input
                type="radio"
                name={`hosted-completion-${flow.id}`}
                className="h-4 w-4"
                checked={draft.completion === completion}
                onChange={() => setDraft({ ...draft, completion })}
              />
              {t(`identityProofingFlows.hosted.completions.${completion}`)}
            </label>
          ))}
        </fieldset>
        {save.isError && (
          <p className={ERROR}>{proofingErrorMessage(save.error, t)}</p>
        )}
        <div>
          <Button
            type="submit"
            size="sm"
            loading={save.isPending}
            disabled={!dirty}
          >
            {t("identityProofingFlows.hosted.save")}
          </Button>
        </div>
      </form>
      <HostedPreview slug={slug} flow={flow} />
    </div>
  );
}

// The hosted page's first step for this flow, in the branding of a customer it
// is assigned to (or any customer): what the subject sees before starting.
function HostedPreview({
  slug,
  flow,
}: {
  slug: string;
  flow: ProofingFlow;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  const customers = useProofingCustomersQuery(slug);
  const all = customers.data ?? [];
  const assigned = all.filter((c) => c.flowIds.includes(flow.id));
  const choices = assigned.length > 0 ? assigned : all;
  const [picked, setPicked] = useState("");
  const customer = choices.find((c) => c.id === picked) ?? choices.at(0);
  // The retention a subject is told is the server's: this flow for this customer.
  const customerFlows = useProofingCustomerFlowsQuery(slug, customer?.id ?? "");
  const retentionDays = customerFlows.data?.find(
    (f) => f.id === flow.id,
  )?.retentionDays;
  const selectId = `hosted-preview-${flow.id}`;

  if (customer === undefined) {
    return (
      <p className={HINT}>{t("identityProofingFlows.hosted.noCustomer")}</p>
    );
  }
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1.5">
        <label htmlFor={selectId} className={LABEL}>
          {t("identityProofingFlows.hosted.previewAs")}
        </label>
        <select
          id={selectId}
          className={SELECT_CLASS}
          value={customer.id}
          onChange={(event) => setPicked(event.target.value)}
        >
          {choices.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      </div>
      <Card
        className="pointer-events-none flex max-w-xl flex-col gap-6 p-6"
        aria-label={t("identityProofingFlows.hosted.preview")}
      >
        <div className="flex items-center gap-3">
          <CustomerMark customer={customer} size="lg" />
          <span className="text-ink text-[16px] font-bold">
            {customer.branding.displayName || customer.name}
          </span>
        </div>
        {customerFlows.isError ? (
          <p className={ERROR}>
            {proofingErrorMessage(customerFlows.error, t)}
          </p>
        ) : customerFlows.isPending ? (
          <p className={HINT}>{t("common.loading")}</p>
        ) : (
          // A flow the customer's list lacks (not the active version) has no
          // retention to tell: no preview of it.
          retentionDays !== undefined && (
            <Overview
              customer={customer}
              flow={{ ...flow, retentionDays }}
              onContinue={() => undefined}
              starting={false}
            />
          )
        )}
      </Card>
    </section>
  );
}
