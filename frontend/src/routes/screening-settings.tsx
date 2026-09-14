import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useSaveScreeningSettingsMutation,
  useScreeningSettingsQuery,
} from "../api/organization.queries";
import type {
  OverdueConsequence,
  RecheckAnchor,
  ScreeningRequiredFor,
  ScreeningSettings,
  ScreeningSettingsInput,
} from "../api/organization";
import { VOG_FUNCTION_ASPECTS } from "../lib/vog-codes";
import { Button, Card, Input } from "../ui";
import * as React from "react";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const SECTION = "text-ink text-[13px] font-semibold";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3";

const REQUIRED_FOR_VALUES: readonly ScreeningRequiredFor[] = [
  "nobody",
  "employees",
  "externals",
  "both",
];

function requiredForLabel(value: ScreeningRequiredFor, t: TFunction): string {
  switch (value) {
    case "employees":
      return t("screeningSettings.requiredForEmployees");
    case "externals":
      return t("screeningSettings.requiredForExternals");
    case "both":
      return t("screeningSettings.requiredForBoth");
    default:
      return t("screeningSettings.requiredForNobody");
  }
}

const INTERVALS: readonly (number | null)[] = [null, 3, 6, 12, 24, 36];
const MAX_AGES: readonly (number | null)[] = [null, 30, 90, 180, 365];

function parseDays(raw: string): number[] | null {
  const parts = raw
    .split(",")
    .map((part) => part.trim())
    .filter((part) => part !== "");
  const days: number[] = [];
  for (const part of parts) {
    const day = Number(part);
    if (!Number.isInteger(day) || day <= 0) return null;
    days.push(day);
  }
  return days;
}

function parseExtraCodes(raw: string): string[] | null {
  const parts = raw
    .split(",")
    .map((part) => part.trim())
    .filter((part) => part !== "");
  for (const part of parts) {
    if (!/^\d{2}$/.test(part)) return null;
  }
  return parts;
}

export function ScreeningSettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useScreeningSettingsQuery(slug, true);

  if (settings.isError) {
    return (
      <Card className="max-w-2xl p-6">
        <p className="text-error text-[14px]">
          {t("screeningSettings.loadError", {
            message: settings.error.message,
          })}
        </p>
      </Card>
    );
  }
  if (settings.isPending) {
    return (
      <Card className="max-w-2xl p-6">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }
  return <ScreeningForm slug={slug} settings={settings.data} />;
}

function ScreeningForm({
  slug,
  settings,
}: {
  slug: string;
  settings: ScreeningSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveScreeningSettingsMutation(slug);

  const [requiredFor, setRequiredFor] = useState<ScreeningRequiredFor>(
    REQUIRED_FOR_VALUES.includes(settings.requiredFor as ScreeningRequiredFor)
      ? (settings.requiredFor as ScreeningRequiredFor)
      : "nobody",
  );
  const [aspectCodes, setAspectCodes] = useState<ReadonlySet<string>>(
    () =>
      new Set(
        settings.requiredCodes.filter((code) =>
          (VOG_FUNCTION_ASPECTS as readonly string[]).includes(code),
        ),
      ),
  );
  const [extraCodes, setExtraCodes] = useState(
    settings.requiredCodes
      .filter(
        (code) => !(VOG_FUNCTION_ASPECTS as readonly string[]).includes(code),
      )
      .join(", "),
  );
  const [maxAge, setMaxAge] = useState<number | null>(
    settings.maxAgeAtUploadDays,
  );
  const [employeeMonths, setEmployeeMonths] = useState<number | null>(
    settings.employeeRecheckIntervalMonths,
  );
  const [externalMonths, setExternalMonths] = useState<number | null>(
    settings.externalRecheckIntervalMonths,
  );
  const [recheckAnchor, setRecheckAnchor] = useState<RecheckAnchor>(
    settings.recheckAnchor === "checked_at" ? "checked_at" : "issue_date",
  );
  const [reminderDays, setReminderDays] = useState(
    settings.reminderDaysBefore.join(", "),
  );
  const [overdueEvery, setOverdueEvery] = useState(
    String(settings.overdueReminderIntervalDays),
  );
  const [overdueMax, setOverdueMax] = useState(
    String(settings.overdueReminderMaxCount),
  );
  const [acceptCredential, setAcceptCredential] = useState(
    settings.acceptYiviCredential,
  );
  const [consequence, setConsequence] = useState<OverdueConsequence>(
    settings.overdueConsequence === "block" ? "block" : "flag",
  );

  const parsedReminderDays = parseDays(reminderDays);
  const parsedExtraCodes = parseExtraCodes(extraCodes);
  const overdueEveryDays = Number(overdueEvery);
  const overdueMaxCount = Number(overdueMax);
  const cadenceValid =
    Number.isInteger(overdueEveryDays) &&
    overdueEveryDays > 0 &&
    Number.isInteger(overdueMaxCount) &&
    overdueMaxCount > 0;
  const canSave =
    parsedReminderDays !== null && parsedExtraCodes !== null && cadenceValid;

  const intervalLabel = (months: number | null): string =>
    months === null
      ? t("screeningSettings.intervalOff")
      : t("screeningSettings.intervalMonths", { count: months });

  const maxAgeLabel = (days: number | null): string =>
    days === null
      ? t("screeningSettings.maxAgeOff")
      : t("screeningSettings.days", { count: days });

  const toggleAspectCode = (code: string): void => {
    setAspectCodes((prev) => {
      const next = new Set(prev);
      if (next.has(code)) next.delete(code);
      else next.add(code);
      return next;
    });
  };

  const submit = (event: React.FormEvent): void => {
    event.preventDefault();
    if (
      parsedReminderDays === null ||
      parsedExtraCodes === null ||
      !cadenceValid
    )
      return;
    const input: ScreeningSettingsInput = {
      requiredFor,
      requiredCodes: [...aspectCodes, ...parsedExtraCodes],
      maxAgeAtUploadDays: maxAge,
      employeeRecheckIntervalMonths: employeeMonths,
      externalRecheckIntervalMonths: externalMonths,
      recheckAnchor,
      reminderDaysBefore: parsedReminderDays,
      overdueReminderIntervalDays: overdueEveryDays,
      overdueReminderMaxCount: overdueMaxCount,
      overdueConsequence: consequence,
      acceptYiviCredential: acceptCredential,
    };
    save.mutate(input);
  };

  return (
    <Card className="max-w-2xl p-6">
      <h2 className="text-[16px] font-semibold">
        {t("screeningSettings.heading")}
      </h2>
      <p className="text-ink-soft mt-2 text-[14px]">
        {t("screeningSettings.description")}
      </p>

      <form onSubmit={submit} className="mt-6 flex flex-col gap-6">
        <fieldset className="flex flex-col gap-2">
          <legend className={SECTION}>
            {t("screeningSettings.requiredFor")}
          </legend>
          <select
            className={CONTROL}
            value={requiredFor}
            onChange={(event) =>
              setRequiredFor(event.target.value as ScreeningRequiredFor)
            }
          >
            {REQUIRED_FOR_VALUES.map((value) => (
              <option key={value} value={value}>
                {requiredForLabel(value, t)}
              </option>
            ))}
          </select>
        </fieldset>

        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>
            {t("screeningSettings.requiredCodes")}
          </legend>
          <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
            {VOG_FUNCTION_ASPECTS.map((code) => (
              <label
                key={code}
                className="flex items-center gap-1.5 text-[13px]"
              >
                <input
                  type="checkbox"
                  checked={aspectCodes.has(code)}
                  onChange={() => toggleAspectCode(code)}
                />
                {t("screeningSettings.aspectCode", { code })}
              </label>
            ))}
          </div>
          <p className={HINT}>{t("screeningSettings.requiredCodesHint")}</p>
          <div className="flex flex-col gap-1">
            <label htmlFor="screening-extra-codes" className={LABEL}>
              {t("screeningSettings.extraCodes")}
            </label>
            <Input
              id="screening-extra-codes"
              value={extraCodes}
              onChange={(event) => setExtraCodes(event.target.value)}
              placeholder={t("screeningSettings.extraCodesPlaceholder")}
              aria-invalid={parsedExtraCodes === null}
            />
            <p className={HINT}>{t("screeningSettings.extraCodesHint")}</p>
            {parsedExtraCodes === null && (
              <p role="alert" className="text-error text-[12px]">
                {t("screeningSettings.extraCodesInvalid")}
              </p>
            )}
          </div>
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className={SECTION}>{t("screeningSettings.maxAge")}</legend>
          <select
            className={CONTROL}
            value={maxAge === null ? "off" : String(maxAge)}
            onChange={(event) =>
              setMaxAge(
                event.target.value === "off"
                  ? null
                  : Number(event.target.value),
              )
            }
          >
            {MAX_AGES.map((days) => (
              <option
                key={days ?? "off"}
                value={days === null ? "off" : String(days)}
              >
                {maxAgeLabel(days)}
              </option>
            ))}
          </select>
          <p className={HINT}>{t("screeningSettings.maxAgeHint")}</p>
        </fieldset>

        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>{t("screeningSettings.recheck")}</legend>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <label htmlFor="screening-employee-interval" className={LABEL}>
                {t("screeningSettings.employeeInterval")}
              </label>
              <select
                id="screening-employee-interval"
                className={CONTROL}
                value={employeeMonths === null ? "off" : String(employeeMonths)}
                onChange={(event) =>
                  setEmployeeMonths(
                    event.target.value === "off"
                      ? null
                      : Number(event.target.value),
                  )
                }
              >
                {INTERVALS.map((months) => (
                  <option
                    key={months ?? "off"}
                    value={months === null ? "off" : String(months)}
                  >
                    {intervalLabel(months)}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="screening-external-interval" className={LABEL}>
                {t("screeningSettings.externalInterval")}
              </label>
              <select
                id="screening-external-interval"
                className={CONTROL}
                value={externalMonths === null ? "off" : String(externalMonths)}
                onChange={(event) =>
                  setExternalMonths(
                    event.target.value === "off"
                      ? null
                      : Number(event.target.value),
                  )
                }
              >
                {INTERVALS.map((months) => (
                  <option
                    key={months ?? "off"}
                    value={months === null ? "off" : String(months)}
                  >
                    {intervalLabel(months)}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="screening-recheck-anchor" className={LABEL}>
              {t("screeningSettings.recheckAnchor")}
            </label>
            <select
              id="screening-recheck-anchor"
              className={CONTROL}
              value={recheckAnchor}
              onChange={(event) =>
                setRecheckAnchor(event.target.value as RecheckAnchor)
              }
            >
              <option value="issue_date">
                {t("screeningSettings.recheckAnchorIssueDate")}
              </option>
              <option value="checked_at">
                {t("screeningSettings.recheckAnchorCheckedAt")}
              </option>
            </select>
          </div>
        </fieldset>

        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>
            {t("screeningSettings.reminders")}
          </legend>
          <div className="flex flex-col gap-1">
            <label htmlFor="screening-reminder-days" className={LABEL}>
              {t("screeningSettings.reminderDaysBefore")}
            </label>
            <Input
              id="screening-reminder-days"
              value={reminderDays}
              onChange={(event) => setReminderDays(event.target.value)}
              placeholder={t("screeningSettings.reminderDaysBeforePlaceholder")}
              aria-invalid={parsedReminderDays === null}
            />
            <p className={HINT}>
              {t("screeningSettings.reminderDaysBeforeHint")}
            </p>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <label htmlFor="screening-overdue-every" className={LABEL}>
                {t("screeningSettings.overdueReminderIntervalDays")}
              </label>
              <Input
                id="screening-overdue-every"
                type="number"
                min={1}
                value={overdueEvery}
                onChange={(event) => setOverdueEvery(event.target.value)}
              />
              <p className={HINT}>
                {t("screeningSettings.overdueReminderIntervalDaysHint")}
              </p>
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="screening-overdue-max" className={LABEL}>
                {t("screeningSettings.overdueReminderMaxCount")}
              </label>
              <Input
                id="screening-overdue-max"
                type="number"
                min={1}
                value={overdueMax}
                onChange={(event) => setOverdueMax(event.target.value)}
              />
              <p className={HINT}>
                {t("screeningSettings.overdueReminderMaxCountHint")}
              </p>
            </div>
          </div>
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className={SECTION}>
            {t("screeningSettings.consequence")}
          </legend>
          <label className="flex items-start gap-2 text-[13.5px]">
            <input
              type="radio"
              name="screening-consequence"
              value="flag"
              checked={consequence === "flag"}
              onChange={() => setConsequence("flag")}
              className="mt-1"
            />
            <span>
              <span className="text-ink font-medium">
                {t("screeningSettings.consequenceFlag")}
              </span>
              <span className={`block ${HINT}`}>
                {t("screeningSettings.consequenceFlagHint")}
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2 text-[13.5px]">
            <input
              type="radio"
              name="screening-consequence"
              value="block"
              checked={consequence === "block"}
              onChange={() => setConsequence("block")}
              className="mt-1"
            />
            <span>
              <span className="text-ink font-medium">
                {t("screeningSettings.consequenceBlock")}
              </span>
              <span className={`block ${HINT}`}>
                {t("screeningSettings.consequenceBlockHint")}
              </span>
            </span>
          </label>
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className={SECTION}>
            {t("screeningSettings.credential")}
          </legend>
          <label className="flex items-start gap-2 text-[13.5px]">
            <input
              type="checkbox"
              checked={acceptCredential}
              onChange={(event) => setAcceptCredential(event.target.checked)}
              className="mt-1"
            />
            <span>
              <span className="text-ink font-medium">
                {t("screeningSettings.acceptCredential")}
              </span>
              <span className={`block ${HINT}`}>
                {t("screeningSettings.acceptCredentialHint")}
              </span>
            </span>
          </label>
        </fieldset>

        <div className="flex flex-col gap-2">
          <p className={HINT}>{t("screeningSettings.recomputeNote")}</p>
          <div>
            <Button type="submit" loading={save.isPending} disabled={!canSave}>
              {t("screeningSettings.save")}
            </Button>
          </div>
          {save.isError && (
            <p role="alert" className="text-error text-[13px]">
              {t("screeningSettings.saveError", {
                message: save.error.message,
              })}
            </p>
          )}
        </div>
      </form>
    </Card>
  );
}
