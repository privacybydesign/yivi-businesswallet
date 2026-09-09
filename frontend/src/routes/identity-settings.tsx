import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  useIdentitySettingsQuery,
  useSaveIdentitySettingsMutation,
} from "../api/organization.queries";
import type {
  IdentitySettings,
  IdentitySettingsInput,
  OverdueConsequence,
} from "../api/organization";
import { Button, Card, Input } from "../ui";
import * as React from "react";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const SECTION = "text-ink text-[13px] font-semibold";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3";

// The intervals an admin picks from, in months. `null` is "never": that member
// type has no deadline at all, which is the shipped default.
const INTERVALS: readonly (number | null)[] = [null, 3, 6, 12, 24, 36];

// The credential ages an admin may insist on, in days. `null` is off: any
// unexpired credential is accepted, which is the shipped default.
const CREDENTIAL_AGES: readonly (number | null)[] = [null, 7, 30, 90, 180, 365];

function parseReminderDays(raw: string): number[] | null {
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

export function IdentitySettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useIdentitySettingsQuery(slug, true);

  if (settings.isError) {
    return (
      <Card className="max-w-2xl p-6">
        <p className="text-error text-[14px]">
          {t("identitySettings.loadError", {
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
  return <IdentityForm slug={slug} settings={settings.data} />;
}

function IdentityForm({
  slug,
  settings,
}: {
  slug: string;
  settings: IdentitySettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveIdentitySettingsMutation(slug);

  const [employeeMonths, setEmployeeMonths] = useState<number | null>(
    settings.employeeIntervalMonths,
  );
  const [externalMonths, setExternalMonths] = useState<number | null>(
    settings.externalIntervalMonths,
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
  const [credentialAge, setCredentialAge] = useState<number | null>(
    settings.credentialMaxAgeDays,
  );
  const [consequence, setConsequence] = useState<OverdueConsequence>(
    settings.overdueConsequence === "block" ? "block" : "flag",
  );

  const parsedReminderDays = parseReminderDays(reminderDays);
  const overdueEveryDays = Number(overdueEvery);
  const overdueMaxCount = Number(overdueMax);
  const cadenceValid =
    Number.isInteger(overdueEveryDays) &&
    overdueEveryDays > 0 &&
    Number.isInteger(overdueMaxCount) &&
    overdueMaxCount > 0;
  const canSave = parsedReminderDays !== null && cadenceValid;

  const intervalLabel = (months: number | null): string =>
    months === null
      ? t("identitySettings.intervalOff")
      : t("identitySettings.intervalMonths", { count: months });

  const credentialAgeLabel = (days: number | null): string =>
    days === null
      ? t("identitySettings.credentialMaxAgeOff")
      : t("identitySettings.days", { count: days });

  const submit = (event: React.FormEvent): void => {
    event.preventDefault();
    if (parsedReminderDays === null || !cadenceValid) return;
    const input: IdentitySettingsInput = {
      employeeIntervalMonths: employeeMonths,
      externalIntervalMonths: externalMonths,
      reminderDaysBefore: parsedReminderDays,
      overdueReminderIntervalDays: overdueEveryDays,
      overdueReminderMaxCount: overdueMaxCount,
      credentialMaxAgeDays: credentialAge,
      overdueConsequence: consequence,
    };
    save.mutate(input);
  };

  const policyOff = employeeMonths === null && externalMonths === null;

  return (
    <Card className="max-w-2xl p-6">
      <h2 className="text-[16px] font-semibold">
        {t("identitySettings.heading")}
      </h2>
      <p className="text-ink-soft mt-2 text-[14px]">
        {t("identitySettings.description")}
      </p>

      <form onSubmit={submit} className="mt-6 flex flex-col gap-6">
        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>{t("identitySettings.intervals")}</legend>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <label htmlFor="identity-employee-interval" className={LABEL}>
                {t("identitySettings.employeeInterval")}
              </label>
              <select
                id="identity-employee-interval"
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
              <label htmlFor="identity-external-interval" className={LABEL}>
                {t("identitySettings.externalInterval")}
              </label>
              <select
                id="identity-external-interval"
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
          <p className={HINT}>{t("identitySettings.intervalHint")}</p>
          {policyOff && (
            <p className="rounded-yivi bg-surface-3 text-ink-soft px-3 py-2 text-[12.5px]">
              {t("identitySettings.unconfigured")}
            </p>
          )}
        </fieldset>

        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>{t("identitySettings.reminders")}</legend>
          <div className="flex flex-col gap-1">
            <label htmlFor="identity-reminder-days" className={LABEL}>
              {t("identitySettings.reminderDaysBefore")}
            </label>
            <Input
              id="identity-reminder-days"
              value={reminderDays}
              onChange={(event) => setReminderDays(event.target.value)}
              placeholder={t("identitySettings.reminderDaysBeforePlaceholder")}
              aria-invalid={parsedReminderDays === null}
            />
            <p className={HINT}>
              {t("identitySettings.reminderDaysBeforeHint")}
            </p>
            {parsedReminderDays === null && (
              <p role="alert" className="text-error text-[12px]">
                {t("identitySettings.reminderDaysInvalid")}
              </p>
            )}
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <label htmlFor="identity-overdue-every" className={LABEL}>
                {t("identitySettings.overdueReminderIntervalDays")}
              </label>
              <Input
                id="identity-overdue-every"
                type="number"
                min={1}
                value={overdueEvery}
                onChange={(event) => setOverdueEvery(event.target.value)}
              />
              <p className={HINT}>
                {t("identitySettings.overdueReminderIntervalDaysHint")}
              </p>
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="identity-overdue-max" className={LABEL}>
                {t("identitySettings.overdueReminderMaxCount")}
              </label>
              <Input
                id="identity-overdue-max"
                type="number"
                min={1}
                value={overdueMax}
                onChange={(event) => setOverdueMax(event.target.value)}
              />
              <p className={HINT}>
                {t("identitySettings.overdueReminderMaxCountHint")}
              </p>
            </div>
          </div>
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className={SECTION}>
            {t("identitySettings.credential")}
          </legend>
          <label htmlFor="identity-credential-age" className={LABEL}>
            {t("identitySettings.credentialMaxAgeDays")}
          </label>
          <select
            id="identity-credential-age"
            className={CONTROL}
            value={credentialAge === null ? "off" : String(credentialAge)}
            onChange={(event) =>
              setCredentialAge(
                event.target.value === "off"
                  ? null
                  : Number(event.target.value),
              )
            }
          >
            {CREDENTIAL_AGES.map((days) => (
              <option
                key={days ?? "off"}
                value={days === null ? "off" : String(days)}
              >
                {credentialAgeLabel(days)}
              </option>
            ))}
          </select>
          <p className={HINT}>{t("identitySettings.credentialMaxAgeHint")}</p>
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className={SECTION}>
            {t("identitySettings.consequence")}
          </legend>
          <label className="flex items-start gap-2 text-[13.5px]">
            <input
              type="radio"
              name="identity-consequence"
              value="flag"
              checked={consequence === "flag"}
              onChange={() => setConsequence("flag")}
              className="mt-1"
            />
            <span>
              <span className="text-ink font-medium">
                {t("identitySettings.consequenceFlag")}
              </span>
              <span className={`block ${HINT}`}>
                {t("identitySettings.consequenceFlagHint")}
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2 text-[13.5px]">
            <input
              type="radio"
              name="identity-consequence"
              value="block"
              checked={consequence === "block"}
              onChange={() => setConsequence("block")}
              className="mt-1"
            />
            <span>
              <span className="text-ink font-medium">
                {t("identitySettings.consequenceBlock")}
              </span>
              <span className={`block ${HINT}`}>
                {t("identitySettings.consequenceBlockHint")}
              </span>
            </span>
          </label>
        </fieldset>

        <div className="flex flex-col gap-2">
          <p className={HINT}>{t("identitySettings.recomputeNote")}</p>
          <div>
            <Button type="submit" loading={save.isPending} disabled={!canSave}>
              {t("identitySettings.save")}
            </Button>
          </div>
          {save.isError && (
            <p role="alert" className="text-error text-[13px]">
              {t("identitySettings.saveError", {
                message: save.error.message,
              })}
            </p>
          )}
        </div>
      </form>
    </Card>
  );
}
