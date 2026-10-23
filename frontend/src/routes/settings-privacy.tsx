import { useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useProofingSettingsQuery,
  useUpdateSettingsMutation,
} from "../api/identity-proofing.queries";
import {
  ERASURE_HANDLINGS,
  type ProofingSettings,
} from "../api/identity-proofing";
import { originLines } from "../lib/hosted-completion";
import { proofingErrorMessage } from "../lib/identity-proofing";
import {
  LEGAL_RETENTION_UNITS,
  MAX_LEGAL_REASON_CHARS,
  legalDraftFrom,
  legalDraftInvalid,
  legalDraftMonths,
  legalDraftDirty,
  legalDraftUpdate,
  legalPeriodLabel,
  legalRetentionLoss,
} from "../lib/legal-retention";
import type { ErasureHandling } from "../api/identity-proofing";
import type {
  LegalRetentionDraft,
  LegalRetentionLoss,
  LegalRetentionUnit,
} from "../lib/legal-retention";
import { Button, Card, ConfirmDialog, Icon, Input } from "../ui";

const LABEL = "text-ink text-[13px] font-semibold";
const HINT = "text-muted text-[12px]";
const ERROR = "text-error text-[12.5px]";
const SELECT =
  "rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 h-9 w-36 appearance-none border pr-10 pl-3 text-[13.5px] outline-none focus:ring-3";
const CHEVRON_SIZE = 12;
const TEXTAREA =
  "rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 min-h-20 w-full max-w-xl border px-3 py-2 font-mono text-[13px] outline-none focus:ring-3";
const AMOUNT_ID = "legal-amount";
const AMOUNT_HINT_ID = "legal-amount-hint";
const REASON_ERROR_ID = "legal-reason-error";
const LEGAL_SWITCH_ID = "legal-switch";
const LEGAL_SWITCH_HINT_ID = "legal-switch-hint";

// The ids of an erasure handling option's title and hint.
function erasureTitleId(handling: ErasureHandling): string {
  return `erasure-${handling}`;
}
function erasureHintId(handling: ErasureHandling): string {
  return `erasure-${handling}-hint`;
}

// Settings › Data and privacy: the GDPR data requests every org offers and who
// decides an erasure, the legal retention, and where a hosted page may hand
// its person back.
export function PrivacySettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useProofingSettingsQuery(slug);

  if (settings.isPending) {
    return <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>;
  }
  if (settings.isError) {
    return (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(settings.error, t)}
      </p>
    );
  }
  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <DataRequestsCard slug={slug} settings={settings.data} />
      <LegalRetentionCard slug={slug} settings={settings.data} />
      <RedirectOriginsCard slug={slug} settings={settings.data} />
    </div>
  );
}

// See my data and Delete my data, which every org offers: what each does, when
// Delete my data is available under the legal retention, and who decides it
// (the org's erasure handling), saved as it is picked.
function DataRequestsCard({
  slug,
  settings,
}: {
  slug: string;
  settings: ProofingSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateSettingsMutation(slug);
  const legal = settings.legalRetention;
  return (
    <Card>
      <div className="border-line border-b px-6 py-4">
        <h2 className="font-display text-[17px] font-bold">
          {t("identityProofing.dataRequests.title")}
        </h2>
        <p className={`${HINT} mt-1`}>
          {t("identityProofing.dataRequests.intro")}
        </p>
      </div>
      <div className="border-line flex flex-wrap items-center justify-between gap-3 border-b px-6 py-4">
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className={LABEL}>
            {t("identityProofing.dataRequests.access.title")}
          </span>
          <span className={HINT}>
            {t("identityProofing.dataRequests.access.hint")}
          </span>
        </div>
        <span className="text-ink-soft text-[13px]">
          {t("identityProofing.dataRequests.access.state")}
        </span>
      </div>
      <div className="flex flex-col gap-4 px-6 py-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 flex-col gap-0.5">
            <span className={LABEL}>
              {t("identityProofing.dataRequests.erasure.title")}
            </span>
            <span className={HINT}>
              {t("identityProofing.dataRequests.erasure.hint")}
            </span>
          </div>
          <span className="text-ink-soft text-[13px]">
            {legal
              ? t("identityProofing.dataRequests.erasure.after", {
                  period: legalPeriodLabel(legal.months, t),
                })
              : t("identityProofing.dataRequests.erasure.anyTime")}
          </span>
        </div>
        <fieldset className="flex flex-col gap-3" disabled={update.isPending}>
          <legend className={`${LABEL} mb-1.5`}>
            {t("identityProofing.dataRequests.whoDecides.legend")}
          </legend>
          {ERASURE_HANDLINGS.map((handling) => (
            <label key={handling} className="flex items-start gap-3">
              <input
                type="radio"
                name="erasure-handling"
                className="mt-0.5 h-4 w-4 shrink-0"
                checked={settings.erasureHandling === handling}
                aria-labelledby={erasureTitleId(handling)}
                aria-describedby={erasureHintId(handling)}
                onChange={() => update.mutate({ erasureHandling: handling })}
              />
              <span className="flex min-w-0 flex-col gap-0.5">
                <span id={erasureTitleId(handling)} className={LABEL}>
                  {t(
                    `identityProofing.dataRequests.whoDecides.${handling}.label`,
                  )}
                </span>
                <span id={erasureHintId(handling)} className={HINT}>
                  {t(
                    `identityProofing.dataRequests.whoDecides.${handling}.hint`,
                  )}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
        {update.isError && (
          <p role="alert" className={ERROR}>
            {proofingErrorMessage(update.error, t)}
          </p>
        )}
      </div>
    </Card>
  );
}

// How long the law makes the org keep the data of every identity check, and
// why: the least any flow keeps it, and what a person is told when their
// request to delete it is declined.
function LegalRetentionCard({
  slug,
  settings,
}: {
  slug: string;
  settings: ProofingSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useUpdateSettingsMutation(slug);
  const saved = settings.legalRetention;
  const [draft, setDraft] = useState<LegalRetentionDraft>(() =>
    legalDraftFrom(saved),
  );
  // Fields are flagged once a save was tried, not while they are typed.
  const [tried, setTried] = useState(false);
  const invalid = legalDraftInvalid(draft);
  const flagged = tried ? invalid : [];
  const set = <K extends keyof LegalRetentionDraft>(
    key: K,
    value: LegalRetentionDraft[K],
  ): void => setDraft((d) => ({ ...d, [key]: value }));
  const months = legalDraftMonths(draft);
  // A removal or a shorter period waiting for its confirmation: it purges the
  // checks past what is left within the hour.
  const [loss, setLoss] = useState<LegalRetentionLoss>();

  function store(): void {
    save.mutate(
      { legalRetention: legalDraftUpdate(draft) },
      {
        onSuccess: (next) => {
          setDraft(legalDraftFrom(next.legalRetention));
          setTried(false);
          setLoss(undefined);
        },
      },
    );
  }

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTried(true);
    if (invalid.length > 0) return;
    const lost = legalRetentionLoss(draft, saved);
    if (lost) {
      setLoss(lost);
      return;
    }
    store();
  }

  function discard(): void {
    setDraft(legalDraftFrom(saved));
    setTried(false);
  }

  return (
    <Card>
      <form onSubmit={submit} noValidate>
        <div className="border-line border-b px-6 py-4">
          <h2 className="font-display text-[17px] font-bold">
            {t("identityProofing.legalRetention.title")}
          </h2>
          <p className={`${HINT} mt-1`}>
            {t("identityProofing.legalRetention.hint", {
              count: settings.dataRetentionDays,
            })}
          </p>
        </div>
        <div className="flex flex-col gap-5 px-6 py-5">
          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              className="mt-0.5 h-4 w-4"
              checked={draft.enabled}
              aria-labelledby={LEGAL_SWITCH_ID}
              aria-describedby={LEGAL_SWITCH_HINT_ID}
              onChange={(e) => set("enabled", e.target.checked)}
            />
            <span className="flex flex-col gap-0.5">
              <span id={LEGAL_SWITCH_ID} className={LABEL}>
                {t("identityProofing.legalRetention.switch")}
              </span>
              <span id={LEGAL_SWITCH_HINT_ID} className={HINT}>
                {t("identityProofing.legalRetention.switchHint")}
              </span>
            </span>
          </label>

          {draft.enabled && (
            <>
              <div className="flex flex-wrap gap-x-8 gap-y-5 pl-7">
                <div className="flex flex-col gap-1.5">
                  <label htmlFor={AMOUNT_ID} className={LABEL}>
                    {t("identityProofing.legalRetention.keepFor")}
                  </label>
                  <div className="flex gap-2">
                    <div className="w-20">
                      <Input
                        id={AMOUNT_ID}
                        type="number"
                        min={1}
                        step={1}
                        value={draft.amount}
                        aria-invalid={flagged.includes("amount")}
                        aria-describedby={AMOUNT_HINT_ID}
                        onChange={(e) => set("amount", e.target.value)}
                      />
                    </div>
                    <div className="relative">
                      <select
                        id="legal-unit"
                        aria-label={t("identityProofing.legalRetention.unit")}
                        className={SELECT}
                        value={draft.unit}
                        onChange={(e) =>
                          set("unit", e.target.value as LegalRetentionUnit)
                        }
                      >
                        {LEGAL_RETENTION_UNITS.map((unit) => (
                          <option key={unit} value={unit}>
                            {t(`identityProofing.legalRetention.units.${unit}`)}
                          </option>
                        ))}
                      </select>
                      <Icon
                        name="chevron_down"
                        size={CHEVRON_SIZE}
                        className="pointer-events-none absolute top-3 right-4"
                      />
                    </div>
                  </div>
                  {flagged.includes("amount") ? (
                    <p id={AMOUNT_HINT_ID} className={ERROR}>
                      {t("identityProofing.legalRetention.amountInvalid")}
                    </p>
                  ) : (
                    <p id={AMOUNT_HINT_ID} className={HINT}>
                      {t("identityProofing.legalRetention.countedFrom")}
                    </p>
                  )}
                </div>

                <fieldset className="flex min-w-60 flex-1 flex-col gap-2.5">
                  <legend className={`${LABEL} mb-1.5`}>
                    {t("identityProofing.legalRetention.reason")}
                  </legend>
                  <ReasonField
                    id="legal-reason-en"
                    label={t("identityProofing.legalRetention.reasonEn")}
                    value={draft.reasonEn}
                    invalid={flagged.includes("reasonEn")}
                    onChange={(value) => set("reasonEn", value)}
                  />
                  <ReasonField
                    id="legal-reason-nl"
                    label={t("identityProofing.legalRetention.reasonNl")}
                    value={draft.reasonNl}
                    invalid={flagged.includes("reasonNl")}
                    onChange={(value) => set("reasonNl", value)}
                  />
                  {(flagged.includes("reasonEn") ||
                    flagged.includes("reasonNl")) && (
                    <p id={REASON_ERROR_ID} className={ERROR}>
                      {t("identityProofing.legalRetention.reasonRequired", {
                        max: MAX_LEGAL_REASON_CHARS,
                      })}
                    </p>
                  )}
                </fieldset>
              </div>

              {months !== null && <Changes months={months} className="ml-7" />}
            </>
          )}
          {save.isError && loss === undefined && (
            <p role="alert" className={ERROR}>
              {proofingErrorMessage(save.error, t)}
            </p>
          )}
        </div>
        <div className="border-line flex justify-end gap-2 border-t px-6 py-4">
          <Button
            type="button"
            variant="secondary"
            disabled={!legalDraftDirty(draft, saved) || save.isPending}
            onClick={discard}
          >
            {t("identityProofing.legalRetention.discard")}
          </Button>
          <Button
            type="submit"
            loading={save.isPending}
            disabled={!legalDraftDirty(draft, saved)}
          >
            {t("identityProofing.legalRetention.save")}
          </Button>
        </div>
      </form>
      {loss !== undefined && (
        <ConfirmDialog
          {...lossCopy(loss, settings.dataRetentionDays, t)}
          busy={save.isPending}
          error={save.isError ? proofingErrorMessage(save.error, t) : undefined}
          onConfirm={store}
          onClose={() => {
            setLoss(undefined);
            save.reset();
          }}
        />
      )}
    </Card>
  );
}

// What the confirmation of a loss says: what is purged, and when.
function lossCopy(
  loss: LegalRetentionLoss,
  retentionDays: number,
  t: TFunction,
): { title: string; message: string; confirmLabel: string } {
  if (loss.kind === "removed") {
    return {
      title: t("identityProofing.legalRetention.removeConfirm.title"),
      message: t("identityProofing.legalRetention.removeConfirm.message", {
        count: retentionDays,
      }),
      confirmLabel: t("identityProofing.legalRetention.removeConfirm.confirm"),
    };
  }
  const period = legalPeriodLabel(loss.months, t);
  return {
    title: t("identityProofing.legalRetention.shortenConfirm.title", {
      period,
    }),
    message: t("identityProofing.legalRetention.shortenConfirm.message", {
      period,
    }),
    confirmLabel: t("identityProofing.legalRetention.shortenConfirm.confirm"),
  };
}

// The origins a hosted page may redirect its subject to and be embedded on.
function RedirectOriginsCard({
  slug,
  settings,
}: {
  slug: string;
  settings: ProofingSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateSettingsMutation(slug);
  const saved = settings.allowedRedirectOrigins.join("\n");
  const [text, setText] = useState(saved);
  const fieldId = "redirect-origins";

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    update.mutate(
      { allowedRedirectOrigins: originLines(text) },
      // Shows the origins as the backend normalised them.
      { onSuccess: (next) => setText(next.allowedRedirectOrigins.join("\n")) },
    );
  }

  return (
    <Card className="px-6 py-4">
      <label
        htmlFor={fieldId}
        className="font-display block text-[17px] font-bold"
      >
        {t("identityProofing.redirectOrigins.title")}
      </label>
      <p className={`${HINT} mt-1`}>
        {t("identityProofing.redirectOrigins.hint")}
      </p>
      <form className="mt-3 flex flex-col gap-3" onSubmit={submit} noValidate>
        <textarea
          id={fieldId}
          value={text}
          rows={3}
          spellCheck={false}
          placeholder="https://portal.example.com"
          className={TEXTAREA}
          onChange={(event) => setText(event.target.value)}
        />
        <div>
          <Button
            type="submit"
            loading={update.isPending}
            disabled={originLines(text).join("\n") === saved}
          >
            {t("identityProofing.redirectOrigins.save")}
          </Button>
        </div>
      </form>
      {update.isError && (
        <p className={`${ERROR} mt-2`}>
          {proofingErrorMessage(update.error, t)}
        </p>
      )}
    </Card>
  );
}

function ReasonField({
  id,
  label,
  value,
  invalid,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  invalid: boolean;
  onChange: (value: string) => void;
}): React.JSX.Element {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-ink-soft text-[13px]">
        {label}
      </label>
      <Input
        id={id}
        value={value}
        required
        aria-invalid={invalid}
        aria-describedby={invalid ? REASON_ERROR_ID : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}

// What a legal retention of this many months does, in the org's terms.
function Changes({
  months,
  className,
}: {
  months: number;
  className: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const period = legalPeriodLabel(months, t);
  return (
    <Card
      variant="highlight"
      className={`${className} flex flex-col gap-2 p-4`}
    >
      <h3 className={LABEL}>
        {t("identityProofing.legalRetention.changes.title")}
      </h3>
      <ul className="text-ink flex list-disc flex-col gap-1.5 pl-5 text-[13.5px] leading-[21px]">
        <li>{t("identityProofing.legalRetention.changes.keep", { period })}</li>
        <li>
          {t("identityProofing.legalRetention.changes.erase", { period })}
        </li>
        <li>{t("identityProofing.legalRetention.changes.access")}</li>
      </ul>
    </Card>
  );
}
