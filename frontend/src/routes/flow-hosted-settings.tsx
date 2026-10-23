import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useProofingFlowBrandingQuery,
  useProofingFlowHostedQuery,
  useProofingSettingsQuery,
  useSaveFlowHostedMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingFlow,
  ProofingFlowHosted,
} from "../api/identity-proofing";
import { SUPPORTED_LANGUAGES } from "../i18n";
import { errorCode } from "../lib/api-error";
import {
  effectiveBranding,
  hostedDraft,
  hostedDraftDirty,
  localesWithoutReasons,
  proofingErrorMessage,
  savedLocales,
} from "../lib/identity-proofing";
import { Button, Card } from "../ui";
import { CAPTION } from "./proofing-branding-fields";
import { BrandMark } from "./proofing-ui";
import { Overview } from "./proofing-verify-steps";

const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const CONTROL = "accent-ink mt-0.5 h-[18px] w-[18px] flex-none";
const COMPLETIONS: readonly ProofingFlowHosted["completion"][] = [
  "redirect",
  "done",
];
// The save's refusal of a language the flow gives no reasons in.
const REASONS_MISSING = "reasons_missing";

// A flow's hosted page: whether links may be made for it, the languages it
// offers, how it ends, and a preview of its first step in the flow's branding.
// onOpenCheck switches to the Check tab, where a missing reason is filled in.
export function FlowHostedSettings({
  slug,
  flow,
  onOpenCheck,
}: Readonly<{
  slug: string;
  flow: ProofingFlow;
  onOpenCheck: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useProofingFlowHostedQuery(slug, flow.id);

  if (settings.isPending) {
    return <p className={HINT}>{t("common.loading")}</p>;
  }
  if (settings.isError) {
    return <p className={ERROR}>{proofingErrorMessage(settings.error, t)}</p>;
  }
  return (
    <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
      <HostedForm
        slug={slug}
        flow={flow}
        saved={settings.data}
        onOpenCheck={onOpenCheck}
      />
      <HostedPreview slug={slug} flow={flow} />
    </div>
  );
}

// Cancel discards this tab's changes only: Edit flow stays open, and the
// other tabs keep their drafts.
function HostedForm({
  slug,
  flow,
  saved,
  onOpenCheck,
}: Readonly<{
  slug: string;
  flow: ProofingFlow;
  saved: ProofingFlowHosted;
  onOpenCheck: () => void;
}>): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const save = useSaveFlowHostedMutation(slug, flow.id);
  const [draft, setDraft] = useState(() => hostedDraft(saved));
  // The switch and each ending are labelled by their title and described by
  // their hint.
  const idBase = useId();
  const noLocale = draft.locales.length === 0;
  // A language the active version gives no reasons in waits for them: the
  // server refuses it too (reasons_missing).
  const unexplained = localesWithoutReasons(flow, draft.locales);
  const reasonsRefused =
    save.isError && errorCode(save.error) === REASONS_MISSING;
  // Languages are picked, but a reason is missing in one of them.
  const reasonsWanted = !noLocale && (unexplained.length > 0 || reasonsRefused);
  const dirty = hostedDraftDirty(draft, saved);

  function toggleLocale(locale: string, on: boolean): void {
    setDraft((current) => ({
      ...current,
      // Kept in the supported order.
      locales: SUPPORTED_LANGUAGES.filter((l) =>
        l === locale ? on : current.locales.includes(l),
      ),
    }));
  }

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    if (noLocale || unexplained.length > 0) {
      return;
    }
    save.mutate({ ...draft, locales: savedLocales(draft.locales) });
  }

  return (
    <Card>
      <form noValidate onSubmit={submit}>
        <div className="border-line border-b px-6 py-4">
          <h2 className="font-display text-[17px] font-bold">
            {t("identityProofingFlows.hosted.title")}
          </h2>
          <p className={`${HINT} mt-1`}>
            {t("identityProofingFlows.hosted.hint")}
          </p>
        </div>
        <div className="flex flex-col gap-5 px-6 py-5">
          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              className={CONTROL}
              checked={draft.enabled}
              aria-labelledby={`${idBase}-enabled`}
              aria-describedby={`${idBase}-enabled-hint`}
              onChange={(event) =>
                setDraft({ ...draft, enabled: event.target.checked })
              }
            />
            <span className="flex flex-col gap-0.5">
              <span
                id={`${idBase}-enabled`}
                className="text-[14px] font-semibold"
              >
                {t("identityProofingFlows.hosted.enabled")}
              </span>
              <span
                id={`${idBase}-enabled-hint`}
                className="text-ink-soft text-[13px]"
              >
                {t("identityProofingFlows.hosted.enabledHint")}
              </span>
            </span>
          </label>
          <fieldset className="flex flex-col gap-2.5">
            <legend className="mb-1 text-[14px] font-semibold">
              {t("identityProofingFlows.hosted.locales")}
            </legend>
            {SUPPORTED_LANGUAGES.map((locale) => (
              <label key={locale} className="flex items-start gap-3">
                <input
                  type="checkbox"
                  className={CONTROL}
                  checked={draft.locales.includes(locale)}
                  onChange={(event) =>
                    toggleLocale(locale, event.target.checked)
                  }
                />
                <span className="text-[14px] font-semibold">
                  {t(`common.languageName.${locale}`)}
                </span>
              </label>
            ))}
            {noLocale && (
              <p role="alert" className={ERROR}>
                {t("identityProofingFlows.hosted.localesRequired")}
              </p>
            )}
            {reasonsWanted && (
              <div role="alert" className="flex flex-col items-start gap-1.5">
                <p className={ERROR}>
                  {unexplained.length > 0
                    ? t("identityProofingFlows.hosted.reasonsMissing", {
                        languages: new Intl.ListFormat(i18n.language, {
                          type: "conjunction",
                        }).format(
                          unexplained.map((locale) =>
                            t(`common.languageName.${locale}`),
                          ),
                        ),
                      })
                    : proofingErrorMessage(save.error, t)}
                </p>
                <button
                  type="button"
                  className="text-link text-[12.5px] font-semibold hover:underline"
                  onClick={onOpenCheck}
                >
                  {t("identityProofingFlows.hosted.openCheck")}
                </button>
              </div>
            )}
            {!noLocale && !reasonsWanted && (
              <p className={HINT}>
                {t("identityProofingFlows.hosted.localesHint")}
              </p>
            )}
          </fieldset>
          <fieldset className="flex flex-col gap-2.5">
            <legend className="mb-1 text-[14px] font-semibold">
              {t("identityProofingFlows.hosted.completion")}
            </legend>
            {COMPLETIONS.map((completion) => (
              <label key={completion} className="flex items-start gap-3">
                <input
                  type="radio"
                  name={`hosted-completion-${flow.id}`}
                  className={CONTROL}
                  checked={draft.completion === completion}
                  aria-labelledby={`${idBase}-${completion}`}
                  aria-describedby={`${idBase}-${completion}-hint`}
                  onChange={() => setDraft({ ...draft, completion })}
                />
                <span className="flex flex-col gap-0.5">
                  <span
                    id={`${idBase}-${completion}`}
                    className="text-[14px] font-semibold"
                  >
                    {t(
                      `identityProofingFlows.hosted.completions.${completion}.title`,
                    )}
                  </span>
                  <span
                    id={`${idBase}-${completion}-hint`}
                    className="text-ink-soft text-[13px]"
                  >
                    {t(
                      `identityProofingFlows.hosted.completions.${completion}.hint`,
                    )}
                  </span>
                </span>
              </label>
            ))}
          </fieldset>
          {save.isError && !reasonsRefused && (
            <p role="alert" className={ERROR}>
              {proofingErrorMessage(save.error, t)}
            </p>
          )}
        </div>
        <div className="border-line flex justify-end gap-2 border-t px-6 py-4">
          <Button
            type="button"
            variant="ghost"
            disabled={!dirty || save.isPending}
            onClick={() => {
              save.reset();
              setDraft(hostedDraft(saved));
            }}
          >
            {t("common.cancel")}
          </Button>
          <Button
            type="submit"
            loading={save.isPending}
            disabled={!dirty || noLocale || unexplained.length > 0}
          >
            {t("identityProofingFlows.hosted.save")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// The hosted page's first step for this flow, in the flow's branding: what the
// subject sees before starting.
function HostedPreview({
  slug,
  flow,
}: Readonly<{
  slug: string;
  flow: ProofingFlow;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const org = useOrganizationQuery(slug);
  const settings = useProofingSettingsQuery(slug);
  const own = useProofingFlowBrandingQuery(slug, flow.id);

  if (org.isError || settings.isError || own.isError) {
    return (
      <p className={ERROR}>
        {proofingErrorMessage(settings.error ?? org.error ?? own.error, t)}
      </p>
    );
  }
  if (org.isPending || settings.isPending || own.isPending) {
    return <p className={HINT}>{t("common.loading")}</p>;
  }
  const branding = effectiveBranding(settings.data.branding, own.data);
  return (
    <section className="flex flex-col gap-2">
      <h3 className={CAPTION}>{t("identityProofingFlows.hosted.preview")}</h3>
      <Card
        inert
        className="pointer-events-none flex max-w-xl flex-col gap-6 p-6"
      >
        <div className="flex items-center gap-3">
          <BrandMark name={org.data.name} branding={branding} size="lg" />
          <span className="text-ink text-[16px] font-bold">
            {branding.displayName || org.data.name}
          </span>
        </div>
        <Overview
          org={{ name: org.data.name, branding }}
          flow={{
            ...flow,
            // The retention a subject is told is the server's, from the list.
            retentionDays:
              flow.retentionDays ?? settings.data.dataRetentionDays,
          }}
          onContinue={() => undefined}
        />
      </Card>
      <p className={HINT}>{t("identityProofingFlows.hosted.previewNote")}</p>
    </section>
  );
}
