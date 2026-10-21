import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useUpdateSettingsMutation } from "../api/identity-proofing.queries";
import {
  ERASURE_HANDLINGS,
  type ProofingSettings,
} from "../api/identity-proofing";
import {
  DATA_RETENTION_DAY_OPTIONS,
  SESSION_TTL_OPTIONS_SECONDS,
  proofingErrorMessage,
  ttlMinutes,
} from "../lib/identity-proofing";
import { originLines } from "../lib/hosted-completion";
import { Button, Card, ConfirmDialog } from "../ui";

const LABEL = "text-ink text-[13px] font-semibold";
const HINT = "text-muted text-[12px]";
const ERROR = "text-error text-[12.5px]";

// The org's session settings: how long a mailed session runs, how long an
// approved person's name is kept, how its erasure requests are decided, and
// where its hosted pages may hand the person back.
export function SettingsTab({
  slug,
  settings,
}: {
  slug: string;
  settings: ProofingSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateSettingsMutation(slug);
  // A shorter retention waiting for its confirmation: it applies to every
  // existing session, so older ones lose their personal data within the hour.
  const [shorter, setShorter] = useState<number>();

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
              onChange={(days) =>
                days < settings.dataRetentionDays
                  ? setShorter(days)
                  : update.mutate({ dataRetentionDays: days })
              }
            />
          </SettingRow>
          <SettingRow
            label={t("identityProofing.settings.erasure")}
            hint={t("identityProofing.settings.erasureHint")}
          >
            <div className="flex flex-col gap-2">
              <Segmented
                label={t("identityProofing.settings.erasure")}
                options={ERASURE_HANDLINGS.map((handling) => ({
                  value: handling,
                  label: t(
                    `identityProofing.settings.erasureOptions.${handling}`,
                  ),
                }))}
                value={settings.erasureHandling}
                disabled={update.isPending}
                onChange={(handling) =>
                  update.mutate({ erasureHandling: handling })
                }
              />
              <p className={HINT}>
                {t(
                  `identityProofing.settings.erasureOptionHints.${settings.erasureHandling}`,
                )}
              </p>
            </div>
          </SettingRow>
        </div>
        {update.isError && shorter === undefined && (
          <p className={`${ERROR} px-5 pb-4`}>
            {proofingErrorMessage(update.error, t)}
          </p>
        )}
      </Card>
      {shorter !== undefined && (
        <ConfirmDialog
          title={t("identityProofing.settings.shortenConfirm.title", {
            days: t("identityProofing.settings.days", { count: shorter }),
          })}
          message={t("identityProofing.settings.shortenConfirm.message", {
            days: t("identityProofing.settings.days", { count: shorter }),
          })}
          confirmLabel={t("identityProofing.settings.shortenConfirm.confirm")}
          busy={update.isPending}
          error={
            update.isError ? proofingErrorMessage(update.error, t) : undefined
          }
          onConfirm={() =>
            update.mutate(
              { dataRetentionDays: shorter },
              { onSuccess: () => setShorter(undefined) },
            )
          }
          onClose={() => {
            setShorter(undefined);
            update.reset();
          }}
        />
      )}
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

function Segmented<T extends string | number>({
  label,
  options,
  value,
  disabled,
  onChange,
}: {
  label: string;
  options: { value: T; label: string }[];
  value: T;
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
