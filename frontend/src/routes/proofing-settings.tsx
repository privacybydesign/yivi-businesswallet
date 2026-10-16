import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useUpdateSettingsMutation } from "../api/identity-proofing.queries";
import type { ProofingSettings } from "../api/identity-proofing";
import {
  DATA_RETENTION_DAY_OPTIONS,
  SESSION_TTL_OPTIONS_SECONDS,
  proofingErrorMessage,
  ttlMinutes,
} from "../lib/identity-proofing";
import { originLines } from "../lib/hosted-completion";
import { Button, Card } from "../ui";

const LABEL = "text-ink text-[13px] font-semibold";
const HINT = "text-muted text-[12px]";
const ERROR = "text-error text-[12.5px]";

// The org's session settings: how long a mailed session runs, how long an
// approved person's name is kept, and where its hosted pages may hand the
// person back.
export function SettingsTab({
  slug,
  settings,
}: {
  slug: string;
  settings: ProofingSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateSettingsMutation(slug);

  return (
    <div className="flex max-w-3xl flex-col gap-5">
      <Card>
        <h2 className="font-display border-line border-b px-5 py-4 text-[17px] font-bold">
          {t("identityProofing.settings.sessionsTitle")}
        </h2>
        <div className="divide-line divide-y px-5">
          <SettingRow
            label={t("identityProofing.settings.qrLifetime")}
            hint={t("identityProofing.settings.qrLifetimeHint")}
          >
            <Segmented
              label={t("identityProofing.settings.qrLifetime")}
              options={SESSION_TTL_OPTIONS_SECONDS.map((seconds) => ({
                value: seconds,
                label: t("identityProofing.settings.minutes", {
                  count: ttlMinutes(seconds),
                }),
              }))}
              value={settings.sessionTtlSeconds}
              disabled={update.isPending}
              onChange={(seconds) =>
                update.mutate({ sessionTtlSeconds: seconds })
              }
            />
          </SettingRow>
          <SettingRow
            label={t("identityProofing.settings.retention")}
            hint={t("identityProofing.settings.retentionHint")}
          >
            <Segmented
              label={t("identityProofing.settings.retention")}
              options={DATA_RETENTION_DAY_OPTIONS.map((days) => ({
                value: days,
                label: t("identityProofing.settings.days", { count: days }),
              }))}
              value={settings.dataRetentionDays}
              disabled={update.isPending}
              onChange={(days) => update.mutate({ dataRetentionDays: days })}
            />
          </SettingRow>
        </div>
        {update.isError && (
          <p className={`${ERROR} px-5 pb-4`}>
            {proofingErrorMessage(update.error, t)}
          </p>
        )}
      </Card>
      <RedirectOriginsCard slug={slug} settings={settings} />
    </div>
  );
}

function SettingRow({
  label,
  hint,
  children,
}: {
  label: string;
  hint: string;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <div className="grid gap-3 py-4 md:grid-cols-[240px_minmax(0,1fr)] md:items-center">
      <div>
        <div className={LABEL}>{label}</div>
        <div className={`${HINT} mt-0.5`}>{hint}</div>
      </div>
      {children}
    </div>
  );
}

function Segmented<T extends number>({
  label,
  options,
  value,
  disabled,
  onChange,
}: {
  label: string;
  options: { value: T; label: string }[];
  value: number;
  disabled: boolean;
  onChange: (value: T) => void;
}): React.JSX.Element {
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-2">
      {options.map((option) => {
        const active = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={disabled}
            onClick={() => !active && onChange(option.value)}
            className={[
              "h-9 rounded-md border px-3.5 text-[13px] font-semibold whitespace-nowrap transition-colors disabled:opacity-60",
              active
                ? "border-ink bg-ink text-surface"
                : "border-line-strong bg-surface text-ink-soft hover:text-ink",
            ].join(" ")}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

const TEXTAREA =
  "rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 min-h-20 w-full max-w-xl border px-3 py-2 font-mono text-[13px] outline-none focus:ring-3";

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
    <Card className="px-5 py-4">
      <label
        htmlFor={fieldId}
        className="font-display block text-[15px] font-bold"
      >
        {t("identityProofing.settings.redirectOriginsTitle")}
      </label>
      <p className={`${HINT} mt-0.5`}>
        {t("identityProofing.settings.redirectOriginsHint")}
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
            {t("identityProofing.settings.save")}
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
