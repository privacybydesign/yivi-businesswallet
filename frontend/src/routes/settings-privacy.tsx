import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useProofingSettingsQuery,
  useUpdateSettingsMutation,
} from "../api/identity-proofing.queries";
import type { ProofingSettings } from "../api/identity-proofing";
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
} from "../lib/legal-retention";
import type {
  LegalRetentionDraft,
  LegalRetentionUnit,
} from "../lib/legal-retention";
import { Button, Card, Icon, Input } from "../ui";

const LABEL = "text-ink text-[13px] font-semibold";
const HINT = "text-muted text-[12px]";
const ERROR = "text-error text-[12.5px]";
const SELECT =
  "rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 h-9 w-36 appearance-none border pr-10 pl-3 text-[13.5px] outline-none focus:ring-3";
const CHEVRON_SIZE = 12;
const AMOUNT_ID = "legal-amount";
const AMOUNT_HINT_ID = "legal-amount-hint";
const REASON_ERROR_ID = "legal-reason-error";

// Settings › Data and privacy.
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
      <LegalRetentionCard slug={slug} settings={settings.data} />
    </div>
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

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTried(true);
    if (invalid.length > 0) return;
    save.mutate(
      { legalRetention: legalDraftUpdate(draft) },
      {
        onSuccess: (next) => {
          setDraft(legalDraftFrom(next.legalRetention));
          setTried(false);
        },
      },
    );
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
              onChange={(e) => set("enabled", e.target.checked)}
            />
            <span className="flex flex-col gap-0.5">
              <span className={LABEL}>
                {t("identityProofing.legalRetention.switch")}
              </span>
              <span className={HINT}>
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
          {save.isError && (
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
